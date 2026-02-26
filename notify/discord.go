package notify

import (
	"fmt"
	"strings"
	"time"

	"github.com/pokt-network/pocket-settlement-monitor/config"
	"github.com/pokt-network/pocket-settlement-monitor/store"
)

// Discord embed color constants as decimal RGB integers.
const (
	ColorSlash       = 0xFF0000 // Red (16711680) - Slashes
	ColorExpiration  = 0xFF8C00 // Dark Orange (16748544) - Expirations
	ColorSettlement  = 0x2ECC71 // Green (3066993) - Settlements
	ColorOverservice = 0xF1C40F // Yellow (15844367) - Overservice
	ColorSummary     = 0x3498DB // Blue (3447003) - Summaries
	ColorDiscard     = 0x95A5A6 // Gray (9807270) - Discards
)

const (
	upoktPerPokt = 1_000_000

	// embedCharLimit is the Discord limit for total characters across all embeds in a payload.
	// We use 5500 as a threshold, leaving 500 chars buffer below Discord's 6000 hard limit.
	embedCharLimit = 5500

	footerText = "pocket-settlement-monitor"
)

// webhookPayload is the JSON body sent to Discord's Execute Webhook endpoint.
type webhookPayload struct {
	Username string  `json:"username,omitempty"`
	Content  string  `json:"content,omitempty"`
	Embeds   []embed `json:"embeds"`
	isSlash  bool    `json:"-"` // internal flag for critical webhook routing, not serialized
}

// embed represents a Discord embed object.
type embed struct {
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description,omitempty"`
	Color       int          `json:"color,omitempty"`
	Fields      []embedField `json:"fields,omitempty"`
	Footer      *embedFooter `json:"footer,omitempty"`
	Timestamp   string       `json:"timestamp,omitempty"` // ISO8601
}

// embedField is a name/value pair within an embed.
type embedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

// embedFooter is the footer sub-object of an embed.
type embedFooter struct {
	Text    string `json:"text"`
	IconURL string `json:"icon_url,omitempty"`
}

// discordFormatter builds Discord embeds from store models. It holds configuration
// for per-type filtering. All methods are pure functions with no side effects.
type discordFormatter struct {
	cfg config.NotificationsConfig
}

// newDiscordFormatter creates a discordFormatter with the given config.
func newDiscordFormatter(cfg config.NotificationsConfig) *discordFormatter {
	return &discordFormatter{cfg: cfg}
}

// formatPOKT converts uPOKT (int64) to a human-readable POKT string with commas.
// Always shows 6 decimal places (1 POKT = 1,000,000 upokt).
// Example: 1234560000 -> "1,234.560000 POKT", 576 -> "0.000576 POKT"
func formatPOKT(upokt int64) string {
	negative := upokt < 0
	if negative {
		upokt = -upokt
	}
	whole := upokt / upoktPerPokt
	frac := upokt % upoktPerPokt

	wholeStr := formatWithCommas(whole)
	fracStr := fmt.Sprintf("%06d", frac)

	result := fmt.Sprintf("%s.%s POKT", wholeStr, fracStr)
	if negative {
		result = "-" + result
	}
	return result
}

// formatWithCommas adds thousands separators to a non-negative integer.
func formatWithCommas(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

// truncateAddress shortens a bech32 address for embed readability.
// If len > 14, returns first 10 + "..." + last 4 chars. Otherwise returns full address.
func truncateAddress(addr string) string {
	if len(addr) <= 14 {
		return addr
	}
	return addr[:10] + "..." + addr[len(addr)-4:]
}

// buildBlockEmbeds groups events by severity into separate color-coded embeds.
// Each non-empty severity group becomes a separate webhookPayload.
// Per-type filtering is applied from config. Returns empty slice if no events are enabled.
func (f *discordFormatter) buildBlockEmbeds(
	height int64,
	ts time.Time,
	settlements []store.Settlement,
	overservices []store.OverserviceEvent,
	reimbursements []store.ReimbursementEvent,
) []webhookPayload {
	var payloads []webhookPayload

	// Group settlements by event type, respecting per-type config toggles.
	var settled, expired, slashed, discarded []store.Settlement
	for _, s := range settlements {
		switch s.EventType {
		case "settled":
			if f.cfg.NotifySettlements {
				settled = append(settled, s)
			}
		case "expired":
			if f.cfg.NotifyExpirations {
				expired = append(expired, s)
			}
		case "slashed":
			if f.cfg.NotifySlashes {
				slashed = append(slashed, s)
			}
		case "discarded":
			if f.cfg.NotifyDiscards {
				discarded = append(discarded, s)
			}
		}
	}

	timestamp := ts.Format(time.RFC3339)

	// Build one embed per non-empty severity group.
	if len(settled) > 0 {
		payloads = append(payloads, f.buildSettlementGroupEmbed(height, timestamp, settled, ColorSettlement, "Settlements", false))
	}
	if len(expired) > 0 {
		payloads = append(payloads, f.buildSettlementGroupEmbed(height, timestamp, expired, ColorExpiration, "Expirations", false))
	}
	if len(slashed) > 0 {
		payloads = append(payloads, f.buildSettlementGroupEmbed(height, timestamp, slashed, ColorSlash, "Slashes", true))
	}
	if len(discarded) > 0 {
		payloads = append(payloads, f.buildSettlementGroupEmbed(height, timestamp, discarded, ColorDiscard, "Discards", false))
	}

	// Overservice embed.
	if f.cfg.NotifyOverservice && len(overservices) > 0 {
		payloads = append(payloads, f.buildOverserviceEmbed(height, timestamp, overservices))
	}

	return payloads
}

// buildSettlementGroupEmbed creates a single embed for a group of settlements of the same severity.
func (f *discordFormatter) buildSettlementGroupEmbed(
	height int64,
	timestamp string,
	settlements []store.Settlement,
	color int,
	label string,
	isSlashGroup bool,
) webhookPayload {
	title := fmt.Sprintf("Block %d - %d %s", height, len(settlements), label)

	e := embed{
		Title:     title,
		Color:     color,
		Timestamp: timestamp,
		Footer:    &embedFooter{Text: footerText},
	}

	// Calculate totals for the group.
	var totalUpokt int64
	var totalRelays int64
	var totalEstimatedRelays int64
	var totalPenaltyUpokt int64
	suppliers := make(map[string]struct{})
	services := make(map[string]struct{})

	for _, s := range settlements {
		totalUpokt += s.ClaimedUpokt
		totalRelays += s.NumRelays
		totalEstimatedRelays += s.EstimatedRelays
		totalPenaltyUpokt += s.SlashPenaltyUpokt
		suppliers[s.SupplierOperatorAddress] = struct{}{}
		if s.ServiceID != "" {
			services[s.ServiceID] = struct{}{}
		}
	}

	// Build fields based on event type.
	fields := f.buildSettlementFields(settlements, totalUpokt, totalRelays, totalEstimatedRelays, totalPenaltyUpokt, suppliers, services, isSlashGroup)

	// Check character limit -- if fields would exceed limit, fall back to compact summary.
	if estimateEmbedChars(title, fields) > embedCharLimit {
		e.Description = f.compactSettlementSummary(settlements, totalUpokt, totalRelays, totalPenaltyUpokt, isSlashGroup)
	} else {
		e.Fields = fields
	}

	return webhookPayload{
		Username: footerText,
		Embeds:   []embed{e},
		isSlash:  isSlashGroup,
	}
}

// buildSettlementFields creates inline fields for a settlement group embed.
func (f *discordFormatter) buildSettlementFields(
	settlements []store.Settlement,
	totalUpokt, totalRelays, totalEstimatedRelays, totalPenaltyUpokt int64,
	suppliers map[string]struct{},
	services map[string]struct{},
	isSlash bool,
) []embedField {
	var fields []embedField

	if isSlash {
		fields = append(fields,
			embedField{Name: "Penalty", Value: formatPOKT(totalPenaltyUpokt), Inline: true},
			embedField{Name: "Claimed", Value: formatPOKT(totalUpokt), Inline: true},
		)
	} else {
		fields = append(fields,
			embedField{Name: "POKT", Value: formatPOKT(totalUpokt), Inline: true},
		)
	}

	fields = append(fields,
		embedField{Name: "Relays", Value: formatWithCommas(totalRelays), Inline: true},
		embedField{Name: "Est. Relays", Value: formatWithCommas(totalEstimatedRelays), Inline: true},
	)

	// Suppliers field.
	supplierList := addressCount(suppliers)
	fields = append(fields, embedField{Name: "Suppliers", Value: supplierList, Inline: true})

	// Services field.
	serviceList := sortedKeyList(services)
	if serviceList == "" {
		serviceList = "N/A"
	}
	fields = append(fields, embedField{Name: "Services", Value: serviceList, Inline: true})

	return fields
}

// buildOverserviceEmbed creates an embed for overservice events.
func (f *discordFormatter) buildOverserviceEmbed(
	height int64,
	timestamp string,
	overservices []store.OverserviceEvent,
) webhookPayload {
	title := fmt.Sprintf("Block %d - %d Overservice", height, len(overservices))

	e := embed{
		Title:     title,
		Color:     ColorOverservice,
		Timestamp: timestamp,
		Footer:    &embedFooter{Text: footerText},
	}

	var totalExpectedBurn, totalEffectiveBurn int64
	suppliers := make(map[string]struct{})
	apps := make(map[string]struct{})

	for _, o := range overservices {
		totalExpectedBurn += o.ExpectedBurnUpokt
		totalEffectiveBurn += o.EffectiveBurnUpokt
		suppliers[o.SupplierOperatorAddress] = struct{}{}
		apps[o.ApplicationAddress] = struct{}{}
	}

	fields := []embedField{
		{Name: "Expected Burn", Value: formatPOKT(totalExpectedBurn), Inline: true},
		{Name: "Effective Burn", Value: formatPOKT(totalEffectiveBurn), Inline: true},
		{Name: "Diff", Value: formatPOKT(totalExpectedBurn - totalEffectiveBurn), Inline: true},
		{Name: "Suppliers", Value: addressCount(suppliers), Inline: true},
		{Name: "Applications", Value: addressCount(apps), Inline: true},
	}

	if estimateEmbedChars(title, fields) > embedCharLimit {
		e.Description = fmt.Sprintf("%d overservice events: expected %s, effective %s",
			len(overservices), formatPOKT(totalExpectedBurn), formatPOKT(totalEffectiveBurn))
	} else {
		e.Fields = fields
	}

	return webhookPayload{
		Username: footerText,
		Embeds:   []embed{e},
	}
}

// buildHourlySummaryEmbed creates a blue summary embed for an hourly period.
// Always produces output even for zero-value summaries so operators know the monitor is alive.
func (f *discordFormatter) buildHourlySummaryEmbed(summary store.HourlySummaryNetwork) webhookPayload {
	title := fmt.Sprintf("Hourly Summary - %s", summary.HourStart.UTC().Format("2006-01-02 15:04 UTC"))

	fields := []embedField{
		{Name: "Settled", Value: fmt.Sprintf("%d", summary.ClaimsSettled), Inline: true},
		{Name: "Expired", Value: fmt.Sprintf("%d", summary.ClaimsExpired), Inline: true},
		{Name: "Slashed", Value: fmt.Sprintf("%d", summary.ClaimsSlashed), Inline: true},
		{Name: "Discarded", Value: fmt.Sprintf("%d", summary.ClaimsDiscarded), Inline: true},
		{Name: "POKT Earned", Value: formatPOKT(summary.ClaimedTotalUpokt), Inline: true},
		{Name: "POKT Lost", Value: formatPOKT(summary.ClaimedTotalUpokt - summary.EffectiveTotalUpokt), Inline: true},
		{Name: "Total Relays", Value: formatWithCommas(summary.NumRelays), Inline: true},
		{Name: "Est. Relays", Value: formatWithCommas(summary.EstimatedRelays), Inline: true},
		{Name: "Overserviced", Value: fmt.Sprintf("%d", summary.OverserviceCount), Inline: true},
	}

	e := embed{
		Title:     title,
		Color:     ColorSummary,
		Fields:    fields,
		Timestamp: summary.HourStart.UTC().Format(time.RFC3339),
		Footer:    &embedFooter{Text: footerText},
	}

	return webhookPayload{
		Username: footerText,
		Embeds:   []embed{e},
	}
}

// buildDailySummaryEmbed creates a blue summary embed for a daily period.
// Includes comparison to previous day and per-supplier breakdown.
func (f *discordFormatter) buildDailySummaryEmbed(
	summary store.DailySummaryNetwork,
	prevDay store.DailySummaryNetwork,
	supplierBreakdown string,
) webhookPayload {
	title := fmt.Sprintf("Daily Summary - %s", summary.DayDate.UTC().Format("2006-01-02"))

	// Calculate POKT lost as difference between claimed and effective for expired+slashed.
	poktLost := summary.ClaimedTotalUpokt - summary.EffectiveTotalUpokt

	fields := []embedField{
		{Name: "Settled", Value: fmt.Sprintf("%d", summary.ClaimsSettled), Inline: true},
		{Name: "Expired", Value: fmt.Sprintf("%d", summary.ClaimsExpired), Inline: true},
		{Name: "Slashed", Value: fmt.Sprintf("%d", summary.ClaimsSlashed), Inline: true},
		{Name: "Discarded", Value: fmt.Sprintf("%d", summary.ClaimsDiscarded), Inline: true},
		{Name: "POKT Earned", Value: formatPOKT(summary.ClaimedTotalUpokt), Inline: true},
		{Name: "POKT Lost", Value: formatPOKT(poktLost), Inline: true},
		{Name: "Total Relays", Value: formatWithCommas(summary.NumRelays), Inline: true},
		{Name: "Est. Relays", Value: formatWithCommas(summary.EstimatedRelays), Inline: true},
		{Name: "Overserviced", Value: fmt.Sprintf("%d", summary.OverserviceCount), Inline: true},
	}

	// Add comparison to previous day.
	comparison := buildDayComparison(summary, prevDay)
	if comparison != "" {
		fields = append(fields, embedField{Name: "vs Previous Day", Value: comparison, Inline: false})
	}

	// Add supplier breakdown if provided.
	if supplierBreakdown != "" {
		fields = append(fields, embedField{Name: "Supplier Breakdown", Value: supplierBreakdown, Inline: false})
	}

	e := embed{
		Title:     title,
		Color:     ColorSummary,
		Fields:    fields,
		Timestamp: summary.DayDate.UTC().Format(time.RFC3339),
		Footer:    &embedFooter{Text: footerText},
	}

	return webhookPayload{
		Username: footerText,
		Embeds:   []embed{e},
	}
}

// buildDayComparison produces a comparison string between current and previous day summaries.
func buildDayComparison(current, previous store.DailySummaryNetwork) string {
	var parts []string

	earnedComp := formatComparison(current.ClaimedTotalUpokt, previous.ClaimedTotalUpokt, "POKT earned")
	if earnedComp != "" {
		parts = append(parts, earnedComp)
	}

	lostCurrent := current.ClaimedTotalUpokt - current.EffectiveTotalUpokt
	lostPrevious := previous.ClaimedTotalUpokt - previous.EffectiveTotalUpokt
	lostComp := formatComparison(lostCurrent, lostPrevious, "POKT lost")
	if lostComp != "" {
		parts = append(parts, lostComp)
	}

	settledDiff := current.ClaimsSettled - previous.ClaimsSettled
	if settledDiff != 0 {
		sign := "+"
		if settledDiff < 0 {
			sign = ""
		}
		parts = append(parts, fmt.Sprintf("%s%d settled claims", sign, settledDiff))
	}

	expiredDiff := current.ClaimsExpired - previous.ClaimsExpired
	if expiredDiff != 0 {
		sign := "+"
		if expiredDiff < 0 {
			sign = ""
		}
		parts = append(parts, fmt.Sprintf("%s%d expired claims", sign, expiredDiff))
	}

	if len(parts) == 0 {
		return "No change"
	}
	return strings.Join(parts, "\n")
}

// formatComparison computes percentage change between current and previous values.
// Returns a human-readable string like "+15.2% POKT earned" or "no change" style.
// Handles division-by-zero gracefully when previous is 0.
func formatComparison(current, previous int64, label string) string {
	if current == previous {
		return ""
	}
	if previous == 0 {
		if current > 0 {
			return fmt.Sprintf("%s %s (new)", formatPOKT(current), label)
		}
		return ""
	}
	pctChange := float64(current-previous) / float64(previous) * 100
	sign := "+"
	if pctChange < 0 {
		sign = ""
	}
	return fmt.Sprintf("%s%.1f%% %s", sign, pctChange, label)
}

// compactSettlementSummary provides a fallback compact description when embeds exceed character limits.
func (f *discordFormatter) compactSettlementSummary(
	settlements []store.Settlement,
	totalUpokt, totalRelays, totalPenaltyUpokt int64,
	isSlash bool,
) string {
	if isSlash {
		return fmt.Sprintf("%d events: penalty %s, claimed %s, %s relays",
			len(settlements), formatPOKT(totalPenaltyUpokt), formatPOKT(totalUpokt), formatWithCommas(totalRelays))
	}
	return fmt.Sprintf("%d events: %s, %s relays",
		len(settlements), formatPOKT(totalUpokt), formatWithCommas(totalRelays))
}

// estimateEmbedChars estimates total character count for an embed with the given title and fields.
func estimateEmbedChars(title string, fields []embedField) int {
	total := len(title)
	for _, f := range fields {
		total += len(f.Name) + len(f.Value)
	}
	return total
}

// addressCount returns a formatted count string like "32" from an address set.
func addressCount(addrs map[string]struct{}) string {
	if len(addrs) == 0 {
		return "N/A"
	}
	return formatWithCommas(int64(len(addrs)))
}

// sortedKeyList returns a comma-separated list of keys from a set.
func sortedKeyList(m map[string]struct{}) string {
	if len(m) == 0 {
		return ""
	}
	var parts []string
	for k := range m {
		parts = append(parts, k)
	}
	return strings.Join(parts, ", ")
}
