package notify

import (
	"bytes"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestParseRetryAfter_JSONBody(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"retry_after": 0.5}`))),
	}

	n := &Notifier{}
	d := n.parseRetryAfter(resp)

	assert.Equal(t, 500*time.Millisecond, d)
}

func TestParseRetryAfter_RetryAfterHeader(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"2"}},
		Body:       io.NopCloser(bytes.NewReader([]byte{})),
	}

	n := &Notifier{}
	d := n.parseRetryAfter(resp)

	assert.Equal(t, 2*time.Second, d)
}

func TestParseRetryAfter_Fallback(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader([]byte{})),
	}

	n := &Notifier{}
	d := n.parseRetryAfter(resp)

	assert.Equal(t, defaultRetryWait, d)
}

func TestParseRetryAfter_JSONPreferredOverHeader(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"5"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"retry_after": 1.5}`))),
	}

	n := &Notifier{}
	d := n.parseRetryAfter(resp)

	assert.Equal(t, 1500*time.Millisecond, d)
}

func TestParseRetryAfter_MalformedJSON(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"3"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(`{not valid json}`))),
	}

	n := &Notifier{}
	d := n.parseRetryAfter(resp)

	assert.Equal(t, 3*time.Second, d)
}
