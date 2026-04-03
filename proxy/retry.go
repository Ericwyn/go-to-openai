package proxy

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"syscall"
)

type retryTransport struct {
	base     http.RoundTripper
	retryMax int
}

func newRetryTransport(base http.RoundTripper, retryMax int) http.RoundTripper {
	if retryMax <= 0 {
		return base
	}

	return &retryTransport{
		base:     base,
		retryMax: retryMax,
	}
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return t.base.RoundTrip(req)
	}

	body, err := snapshotRequestBody(req)
	if err != nil {
		return nil, err
	}

	attempts := t.retryMax + 1
	for attempt := 1; attempt <= attempts; attempt++ {
		clonedReq, err := cloneRequest(req, body)
		if err != nil {
			return nil, err
		}

		resp, roundTripErr := t.base.RoundTrip(clonedReq)
		if roundTripErr == nil {
			return resp, nil
		}
		if !isRetryableProxyError(roundTripErr) || attempt == attempts {
			return nil, roundTripErr
		}

		slog.Warn("retrying upstream request",
			"method", req.Method,
			"path", req.URL.Path,
			"attempt", attempt,
			"max_attempts", attempts,
			"error", roundTripErr,
		)
	}

	return nil, errors.New("unreachable retry state")
}

func snapshotRequestBody(req *http.Request) ([]byte, error) {
	if req == nil || req.Body == nil {
		return nil, nil
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	if err := req.Body.Close(); err != nil {
		return nil, err
	}

	req.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func cloneRequest(req *http.Request, body []byte) (*http.Request, error) {
	clonedReq := req.Clone(req.Context())
	clonedReq.Body = io.NopCloser(bytes.NewReader(body))
	clonedReq.ContentLength = int64(len(body))
	if len(body) == 0 {
		clonedReq.Body = nil
		clonedReq.ContentLength = 0
	}
	return clonedReq, nil
}

func isRetryableProxyError(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if errors.Is(opErr.Err, syscall.EPIPE) || errors.Is(opErr.Err, syscall.ECONNRESET) || errors.Is(opErr.Err, syscall.ECONNREFUSED) || errors.Is(opErr.Err, syscall.ETIMEDOUT) {
			return true
		}
	}

	return false
}

func transportOrDefault(transport http.RoundTripper) http.RoundTripper {
	if transport != nil {
		return transport
	}

	return http.DefaultTransport
}
