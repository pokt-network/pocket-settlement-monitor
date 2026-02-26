package subscriber

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	comettypes "github.com/cometbft/cometbft/types"
	"github.com/rs/zerolog"
)

// defaultHeartbeatTimeout is the duration to wait for new block events before
// treating the WebSocket connection as dead. CometBFT subscription channels do
// not close when the underlying TCP connection drops, so this timeout is the
// primary mechanism for detecting silently lost connections.
//
// The value must be significantly longer than the slowest expected block time.
// On Pocket mainnet, normal blocks are ~60-90s but settlement blocks (which run
// 5000+ EndBlocker events) can take 5-10 minutes. The network has also seen
// blocks exceeding 11 minutes during congestion. A 15-minute default provides
// ample headroom while still detecting genuinely dead connections.
const defaultHeartbeatTimeout = 15 * time.Minute

// defaultHeartbeatLogInterval is how often the subscriber logs a heartbeat
// showing it is still receiving blocks from the WebSocket. This provides
// visibility between settlement blocks (which can be 30+ minutes apart).
const defaultHeartbeatLogInterval = 60 * time.Second

// eventChannelBufferSize is the capacity of the BlockEvents output channel.
const eventChannelBufferSize = 8

// EventSource abstracts the CometBFT subscription for testability.
// Production: wraps rpchttp.HTTP. Tests: mock channel.
type EventSource interface {
	Subscribe(ctx context.Context) (<-chan coretypes.ResultEvent, error)
	Header(ctx context.Context, height int64) (time.Time, error)
	Close() error
}

// CometBFTSource wraps a CometBFT rpchttp.HTTP client.
type CometBFTSource struct {
	rpcURL string
	client *rpchttp.HTTP
}

// NewCometBFTSource creates a new CometBFTSource for the given RPC URL.
func NewCometBFTSource(rpcURL string) *CometBFTSource {
	return &CometBFTSource{rpcURL: rpcURL}
}

// Subscribe creates a new CometBFT client, starts it, and subscribes to
// NewBlockEvents. Each call creates a fresh client instance, which is required
// for clean reconnection (the old client must be stopped and discarded).
func (s *CometBFTSource) Subscribe(ctx context.Context) (<-chan coretypes.ResultEvent, error) {
	client, err := rpchttp.New(s.rpcURL, "/websocket")
	if err != nil {
		return nil, err
	}

	if startErr := client.Start(); startErr != nil {
		return nil, startErr
	}

	ch, err := client.Subscribe(ctx, "settlement-monitor", "tm.event = 'NewBlockEvents'")
	if err != nil {
		_ = client.Stop()
		return nil, err
	}

	s.client = client
	return ch, nil
}

// Header queries the block header for the given height and returns its timestamp.
func (s *CometBFTSource) Header(ctx context.Context, height int64) (time.Time, error) {
	if s.client == nil {
		return time.Time{}, fmt.Errorf("querying block header: client not connected")
	}
	h := height
	result, err := s.client.Header(ctx, &h)
	if err != nil {
		return time.Time{}, fmt.Errorf("querying block header for height %d: %w", height, err)
	}
	if result == nil || result.Header == nil {
		return time.Time{}, fmt.Errorf("querying block header for height %d: nil result", height)
	}
	return result.Header.Time, nil
}

// Close stops the underlying CometBFT client if it exists.
func (s *CometBFTSource) Close() error {
	if s.client != nil {
		return s.client.Stop()
	}
	return nil
}

// Subscriber connects to a CometBFT WebSocket, receives NewBlockEvents,
// decodes them via decodeBlockEvents, and emits BlockEvents batches on a
// buffered channel. It handles reconnection with exponential backoff and
// jitter, and fires state change callbacks on connect/disconnect/reconnect.
type Subscriber struct {
	source           EventSource
	events           chan BlockEvents
	logger           zerolog.Logger
	stats            *DecodeStats
	baseDelay        time.Duration
	maxDelay         time.Duration
	heartbeatTimeout time.Duration
	onStateChange    StateChangeCallback
	onBlock          BlockCallback
	onGap            GapCallback
	lastSeenHeight   int64

	mu     sync.Mutex
	closed bool
}

// SetOnBlock sets the callback invoked on every block received from the WebSocket.
// Must be called before Run.
func (s *Subscriber) SetOnBlock(cb BlockCallback) {
	s.onBlock = cb
}

// SetOnGap sets the callback invoked when a height gap is detected after
// reconnection. Must be called before Run.
func (s *Subscriber) SetOnGap(cb GapCallback) {
	s.onGap = cb
}

// NewSubscriber creates a new Subscriber that reads events from the given EventSource.
// The onStateChange callback is optional and may be nil.
// heartbeatTimeout is the maximum duration to wait for a new block before treating
// the connection as dead; pass 0 to use the default (15 minutes).
func NewSubscriber(source EventSource, logger zerolog.Logger, baseDelay, maxDelay, heartbeatTimeout time.Duration, onStateChange StateChangeCallback) *Subscriber {
	if heartbeatTimeout <= 0 {
		heartbeatTimeout = defaultHeartbeatTimeout
	}
	return &Subscriber{
		source:           source,
		events:           make(chan BlockEvents, eventChannelBufferSize),
		logger:           logger.With().Str("component", "subscriber").Logger(),
		stats:            NewDecodeStats(),
		baseDelay:        baseDelay,
		maxDelay:         maxDelay,
		heartbeatTimeout: heartbeatTimeout,
		onStateChange:    onStateChange,
	}
}

// Events returns a read-only channel of BlockEvents for consumers.
func (s *Subscriber) Events() <-chan BlockEvents {
	return s.events
}

// Stats returns the decode statistics for metrics integration.
func (s *Subscriber) Stats() *DecodeStats {
	return s.stats
}

// Run is the main event loop. It subscribes to the event source, decodes
// block events, and emits them on the output channel. On disconnect, it
// reconnects with exponential backoff. Run blocks until the context is canceled.
func (s *Subscriber) Run(ctx context.Context) error {
	defer close(s.events)

	for {
		err := s.subscribeAndProcess(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Disconnected -- notify and attempt reconnection
		s.notifyStateChange(StateDisconnected, s.lastSeenHeight, 0)
		s.logger.Warn().
			Int64("last_seen_height", s.lastSeenHeight).
			Err(err).
			Msg("disconnected from CometBFT WebSocket")

		_ = s.source.Close()

		if err := s.reconnect(ctx); err != nil {
			return err
		}
	}
}

// subscribeAndProcess subscribes to the source and processes events until
// disconnect (channel close or heartbeat timeout) or context cancellation.
func (s *Subscriber) subscribeAndProcess(ctx context.Context) error {
	ch, err := s.source.Subscribe(ctx)
	if err != nil {
		return err
	}

	s.notifyStateChange(StateConnected, 0, 0)
	s.logger.Info().Msg("connected to CometBFT WebSocket")

	return s.receiveLoop(ctx, ch)
}

// receiveLoop reads events from the subscription channel and emits decoded
// BlockEvents. It returns when the channel closes, the heartbeat times out,
// or the context is canceled. It also logs periodic heartbeats to show the
// subscriber is alive between settlement blocks.
func (s *Subscriber) receiveLoop(ctx context.Context, ch <-chan coretypes.ResultEvent) error {
	heartbeat := time.NewTimer(s.heartbeatTimeout)
	defer heartbeat.Stop()

	// Periodic heartbeat log so the system doesn't appear dead between
	// settlement blocks (which can be 30+ minutes apart on beta/mainnet).
	heartbeatLog := time.NewTicker(defaultHeartbeatLogInterval)
	defer heartbeatLog.Stop()

	var blocksReceived int64
	var settlementBlocks int64

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case result, ok := <-ch:
			if !ok {
				return nil // channel closed
			}

			// Reset heartbeat on every received event
			if !heartbeat.Stop() {
				select {
				case <-heartbeat.C:
				default:
				}
			}
			heartbeat.Reset(s.heartbeatTimeout)

			blockData, ok := result.Data.(comettypes.EventDataNewBlockEvents)
			if !ok {
				s.logger.Error().
					Str("data_type", typeNameOf(result.Data)).
					Msg("unexpected event data type, expected EventDataNewBlockEvents")
				continue
			}

			blockTime, headerErr := s.source.Header(ctx, blockData.Height)
			if headerErr != nil {
				// During shutdown, context cancellation is expected — don't warn.
				if ctx.Err() != nil {
					return ctx.Err()
				}
				// Fallback to time.Now() if header query fails (log warning)
				s.logger.Warn().Err(headerErr).Int64("height", blockData.Height).
					Msg("failed to fetch block header timestamp, using wall-clock")
				blockTime = time.Now()
			}

			blockEvents := decodeBlockEvents(blockData, blockTime, s.logger, s.stats)

			// Detect height gaps (e.g., after reconnect skipped blocks).
			if s.lastSeenHeight > 0 && blockData.Height > s.lastSeenHeight+1 {
				gapFrom := s.lastSeenHeight + 1
				gapTo := blockData.Height - 1
				s.logger.Warn().
					Int64("last_seen", s.lastSeenHeight).
					Int64("current", blockData.Height).
					Int64("gap_from", gapFrom).
					Int64("gap_to", gapTo).
					Msg("height gap detected, triggering backfill")
				if s.onGap != nil {
					s.onGap(gapFrom, gapTo)
				}
			}

			s.lastSeenHeight = blockData.Height
			blocksReceived++

			// Notify on every block for height tracking and block rate metrics.
			if s.onBlock != nil {
				s.onBlock(blockData.Height)
			}

			if len(blockEvents.Events) > 0 {
				settlementBlocks++
				s.emitBlockEvents(ctx, blockEvents)
			}

		case <-heartbeatLog.C:
			if blocksReceived > 0 {
				s.logger.Info().
					Int64("blocks_received", blocksReceived).
					Int64("settlement_blocks", settlementBlocks).
					Int64("last_height", s.lastSeenHeight).
					Msg("subscriber heartbeat: receiving blocks")
				blocksReceived = 0
				settlementBlocks = 0
			}

		case <-heartbeat.C:
			s.logger.Warn().
				Int64("last_seen_height", s.lastSeenHeight).
				Dur("timeout", s.heartbeatTimeout).
				Msg("heartbeat timeout, no events received")
			return nil // trigger reconnection
		}
	}
}

// emitBlockEvents sends block events on the output channel. It first attempts
// a non-blocking send; if the channel is full it logs a warning about a slow
// consumer and then performs a blocking send to avoid dropping events.
func (s *Subscriber) emitBlockEvents(ctx context.Context, blockEvents BlockEvents) {
	select {
	case s.events <- blockEvents:
		return
	default:
	}

	// Channel full -- warn about slow consumer then block
	s.logger.Warn().
		Int64("height", blockEvents.Height).
		Int("event_count", len(blockEvents.Events)).
		Msg("slow consumer: event channel full, blocking")

	select {
	case s.events <- blockEvents:
	case <-ctx.Done():
	}
}

// reconnect attempts to re-subscribe to the source with exponential backoff
// and jitter. It retries forever until the context is canceled.
func (s *Subscriber) reconnect(ctx context.Context) error {
	attempt := 0

	for {
		delay := NextDelay(attempt, s.baseDelay, s.maxDelay)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}

		ch, err := s.source.Subscribe(ctx)
		if err != nil {
			attempt++
			continue
		}

		s.logger.Info().
			Int("attempts", attempt+1).
			Msg("reconnected to CometBFT WebSocket")

		s.notifyStateChange(StateReconnected, s.lastSeenHeight, 0)

		// Resume processing with the new channel
		err = s.receiveLoop(ctx, ch)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Disconnected again -- notify, close, and retry
		s.notifyStateChange(StateDisconnected, s.lastSeenHeight, 0)
		s.logger.Warn().
			Int64("last_seen_height", s.lastSeenHeight).
			Err(err).
			Msg("disconnected from CometBFT WebSocket")

		_ = s.source.Close()
		attempt = 0
	}
}

// Close closes the source and marks the subscriber as closed.
func (s *Subscriber) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true
	return s.source.Close()
}

// notifyStateChange calls the onStateChange callback if non-nil.
// Protects against nil callback panic.
func (s *Subscriber) notifyStateChange(state ConnectionState, lastSeen, resume int64) {
	if s.onStateChange == nil {
		return
	}
	s.onStateChange(StateChangeEvent{
		State:          state,
		LastSeenHeight: lastSeen,
		ResumeHeight:   resume,
	})
}

// NextDelay calculates the next reconnection delay with exponential backoff
// and +/-20% jitter. Exported for testing.
//
//	delay = min(baseDelay * 2^attempt, maxDelay)
//	jitter = delay / 5  (20%)
//	actualDelay = delay - jitter + rand(0, 2*jitter)
func NextDelay(attempt int, baseDelay, maxDelay time.Duration) time.Duration {
	delay := baseDelay
	for i := 0; i < attempt; i++ {
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
			break
		}
	}

	jitter := delay / 5
	if jitter > 0 {
		delay = delay - jitter + time.Duration(rand.Int64N(int64(2*jitter+1)))
	}

	return delay
}

// typeNameOf returns a string representation of the type for logging.
func typeNameOf(v interface{}) string {
	if v == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%T", v)
}
