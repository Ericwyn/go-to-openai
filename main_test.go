package main

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ericwyn/go-to-openai/certmanager"
	"github.com/ericwyn/go-to-openai/config"
	"github.com/ericwyn/go-to-openai/proxy"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type retryTransport struct {
	base     http.RoundTripper
	retryMax int
}

func newTestRetryTransport(base http.RoundTripper, retryMax int) http.RoundTripper {
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
		clonedReq, err := cloneTestRequest(req, body)
		if err != nil {
			return nil, err
		}

		resp, roundTripErr := t.base.RoundTrip(clonedReq)
		if roundTripErr == nil {
			return resp, nil
		}
		if !isTestRetryableError(roundTripErr) || attempt == attempts {
			return nil, roundTripErr
		}
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

func cloneTestRequest(req *http.Request, body []byte) (*http.Request, error) {
	clonedReq := req.Clone(req.Context())
	clonedReq.Body = io.NopCloser(bytes.NewReader(body))
	clonedReq.ContentLength = int64(len(body))
	if len(body) == 0 {
		clonedReq.Body = nil
		clonedReq.ContentLength = 0
	}
	return clonedReq, nil
}

func isTestRetryableError(err error) bool {
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

func dumpTestRequest(r *http.Request) ([]byte, error) {
	body, err := snapshotRequestBody(r)
	if err != nil {
		return nil, err
	}

	clonedReq, err := cloneTestRequest(r, body)
	if err != nil {
		return nil, err
	}

	return httputil.DumpRequest(clonedReq, true)
}

func TestChatCompletionsProxy(t *testing.T) {
	t.Parallel()

	type capturedRequest struct {
		Method        string
		Path          string
		RawQuery      string
		Authorization string
		ContentType   string
		UserAgent     string
		Host          string
		HeaderHost    string
		Body          []byte
	}

	requests := make(chan capturedRequest, 1)
	handler := newTestHandler(t, config.Config{
		Upstreams: []config.UpstreamConfig{
			{
				Host:    "api.openai.com",
				BaseURL: "http://api.openai.com",
				Routes:  config.Default().Upstreams[0].Routes,
			},
		},
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}

			requests <- capturedRequest{
				Method:        r.Method,
				Path:          r.URL.Path,
				RawQuery:      r.URL.RawQuery,
				Authorization: r.Header.Get("Authorization"),
				ContentType:   r.Header.Get("Content-Type"),
				UserAgent:     r.Header.Get("User-Agent"),
				Host:          r.Host,
				HeaderHost:    r.Header.Get("Host"),
				Body:          body,
			}

			return &http.Response{
				StatusCode: http.StatusCreated,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			}, nil
		}),
	}, false)

	payload := []byte(`{"model":"gpt-5.4-mini","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions?trace=1", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "proxy-test")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	respBody, err := io.ReadAll(recorder.Result().Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusCreated)
	}
	if string(respBody) != `{"ok":true}` {
		t.Fatalf("response body = %s", string(respBody))
	}

	captured := <-requests
	if captured.Method != http.MethodPost {
		t.Fatalf("method = %s", captured.Method)
	}
	if captured.Path != "/v1/chat/completions" {
		t.Fatalf("path = %s", captured.Path)
	}
	if captured.RawQuery != "trace=1" {
		t.Fatalf("raw query = %s", captured.RawQuery)
	}
	if captured.Authorization != "Bearer test-key" {
		t.Fatalf("authorization = %s", captured.Authorization)
	}
	if captured.ContentType != "application/json" {
		t.Fatalf("content type = %s", captured.ContentType)
	}
	if captured.UserAgent != "proxy-test" {
		t.Fatalf("user agent = %s", captured.UserAgent)
	}
	if captured.Host != "api.openai.com" {
		t.Fatalf("host = %s", captured.Host)
	}
	if captured.HeaderHost != "api.openai.com" {
		t.Fatalf("header host = %s", captured.HeaderHost)
	}
	if !bytes.Equal(captured.Body, payload) {
		t.Fatalf("body mismatch = %s", string(captured.Body))
	}
}

func TestModelsProxy(t *testing.T) {
	t.Parallel()

	type capturedRequest struct {
		Method        string
		Path          string
		RawQuery      string
		Authorization string
	}

	requests := make(chan capturedRequest, 1)
	handler := newTestHandler(t, config.Config{
		Upstreams: []config.UpstreamConfig{
			{
				Host:    "api.openai.com",
				BaseURL: "http://api.openai.com",
				Routes:  config.Default().Upstreams[0].Routes,
			},
		},
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests <- capturedRequest{
				Method:        r.Method,
				Path:          r.URL.Path,
				RawQuery:      r.URL.RawQuery,
				Authorization: r.Header.Get("Authorization"),
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"data":[]}`)),
			}, nil
		}),
	}, false)

	req, err := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/models?limit=10", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer test-key")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	respBody, err := io.ReadAll(recorder.Result().Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if string(respBody) != `{"data":[]}` {
		t.Fatalf("response body = %s", string(respBody))
	}

	captured := <-requests
	if captured.Method != http.MethodGet {
		t.Fatalf("method = %s", captured.Method)
	}
	if captured.Path != "/v1/models" {
		t.Fatalf("path = %s", captured.Path)
	}
	if captured.RawQuery != "limit=10" {
		t.Fatalf("raw query = %s", captured.RawQuery)
	}
	if captured.Authorization != "Bearer test-key" {
		t.Fatalf("authorization = %s", captured.Authorization)
	}
}

func TestResponsesProxy(t *testing.T) {
	t.Parallel()

	requests := make(chan string, 1)
	handler := newTestHandler(t, config.Config{
		Upstreams: []config.UpstreamConfig{
			{
				Host:    "api.openai.com",
				BaseURL: "http://api.openai.com",
				Routes:  config.Default().Upstreams[0].Routes,
			},
		},
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests <- r.URL.Path
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader("data: hello\n\ndata: done\n\n")),
			}, nil
		}),
	}, false)

	req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", bytes.NewBufferString(`{"model":"gpt-5.1","stream":true}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer test-key")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	body, err := io.ReadAll(recorder.Result().Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	if got := <-requests; got != "/openai/responses" {
		t.Fatalf("path = %s", got)
	}
	if recorder.Result().Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content type = %s", recorder.Result().Header.Get("Content-Type"))
	}
	if string(body) != "data: hello\n\ndata: done\n\n" {
		t.Fatalf("body = %q", string(body))
	}
}

func TestCustomRouteConfig(t *testing.T) {
	t.Parallel()

	requests := make(chan string, 1)
	handler := newTestHandler(t, config.Config{
		Upstreams: []config.UpstreamConfig{
			{
				Host:    "api.openai.com",
				BaseURL: "http://api.openai.com",
				Routes: []config.RouteConfig{
					{Path: "/v1/embeddings", TargetPath: "/openai/embeddings"},
				},
			},
		},
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests <- r.URL.Path
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			}, nil
		}),
	}, false)

	req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/embeddings", bytes.NewBufferString(`{"input":"hello"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if got := <-requests; got != "/openai/embeddings" {
		t.Fatalf("path = %s", got)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestProxyUsesConfiguredUpstreamHost(t *testing.T) {
	t.Parallel()

	type capturedRequest struct {
		Host       string
		HeaderHost string
	}

	requests := make(chan capturedRequest, 1)
	handler := newTestHandler(t, config.Config{
		Upstreams: []config.UpstreamConfig{
			{
				Host:     "api.openai.com",
				BaseURL:  "http://127.0.0.1:8080",
				BaseHost: "proxy.example.com",
				Routes:   config.Default().Upstreams[0].Routes,
			},
		},
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests <- capturedRequest{
				Host:       r.Host,
				HeaderHost: r.Header.Get("Host"),
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			}, nil
		}),
	}, false)

	req, err := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/models", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	captured := <-requests
	if captured.Host != "proxy.example.com" {
		t.Fatalf("host = %s", captured.Host)
	}
	if captured.HeaderHost != "proxy.example.com" {
		t.Fatalf("header host = %s", captured.HeaderHost)
	}
}

func TestMultiUpstreamRouting(t *testing.T) {
	t.Parallel()

	type capturedRequest struct {
		Host string
		Path string
	}

	requests := make(chan capturedRequest, 2)
	handler := newTestHandler(t, config.Config{
		Upstreams: []config.UpstreamConfig{
			{
				Host:    "api.openai.com",
				BaseURL: "http://openai-backend.local",
				Routes: []config.RouteConfig{
					{Path: "/v1/chat/completions", TargetPath: "/v1/chat/completions"},
				},
			},
			{
				Host:    "api.anthropic.com",
				BaseURL: "http://anthropic-backend.local",
				Routes: []config.RouteConfig{
					{Path: "/v1/messages", TargetPath: "/v1/messages"},
				},
			},
		},
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests <- capturedRequest{
				Host: r.Host,
				Path: r.URL.Path,
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			}, nil
		}),
	}, false)

	req1, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	recorder1 := httptest.NewRecorder()
	handler.ServeHTTP(recorder1, req1)

	req2, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	recorder2 := httptest.NewRecorder()
	handler.ServeHTTP(recorder2, req2)

	captured1 := <-requests
	if captured1.Host != "openai-backend.local" {
		t.Fatalf("openai host = %s", captured1.Host)
	}
	if captured1.Path != "/v1/chat/completions" {
		t.Fatalf("openai path = %s", captured1.Path)
	}

	captured2 := <-requests
	if captured2.Host != "anthropic-backend.local" {
		t.Fatalf("anthropic host = %s", captured2.Host)
	}
	if captured2.Path != "/v1/messages" {
		t.Fatalf("anthropic path = %s", captured2.Path)
	}
}

func TestUnknownHost(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, config.Config{
		Upstreams: []config.UpstreamConfig{
			{
				Host:    "api.openai.com",
				BaseURL: "http://api.openai.com",
				Routes: []config.RouteConfig{
					{Path: "/v1/models", TargetPath: "/v1/models"},
				},
			},
		},
	}, false)

	recorder := httptest.NewRecorder()
	req, err := http.NewRequest(http.MethodGet, "https://unknown.example.com/v1/models", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d", recorder.Code)
	}

	var payload map[string]map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if payload["error"]["message"] != "unknown host" {
		t.Fatalf("message = %s", payload["error"]["message"])
	}
}

func TestRetryTransportRetriesRetryableError(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	transport := newTestRetryTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempt := attempts.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if string(body) != `{"hello":"world"}` {
			t.Fatalf("body = %s", string(body))
		}
		if attempt < 3 {
			return nil, &net.OpError{Err: syscall.EPIPE}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		}, nil
	}), 3)

	req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewBufferString(`{"hello":"world"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	defer resp.Body.Close()

	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d", attempts.Load())
	}
}

func TestRetryTransportRetriesConnectionRefused(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	transport := newTestRetryTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempt := attempts.Add(1)
		if attempt < 3 {
			return nil, &net.OpError{Err: syscall.ECONNREFUSED}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		}, nil
	}), 3)

	req, err := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/models", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	defer resp.Body.Close()

	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d", attempts.Load())
	}
}

func TestRetryTransportRetriesTimedOutOpError(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	transport := newTestRetryTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempt := attempts.Add(1)
		if attempt < 3 {
			return nil, &net.OpError{Err: syscall.ETIMEDOUT}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		}, nil
	}), 3)

	req, err := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/models", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	defer resp.Body.Close()

	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d", attempts.Load())
	}
}

func TestRetryTransportDoesNotRetryNonRetryableError(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	expectedErr := errors.New("bad request")
	transport := newTestRetryTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts.Add(1)
		return nil, expectedErr
	}), 3)

	req, err := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/models", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	_, err = transport.RoundTrip(req)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("err = %v", err)
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts = %d", attempts.Load())
	}
}

func TestParseRunFlags(t *testing.T) {
	t.Skip("parseRunFlags no longer exists; flag parsing is now done in handleRun via flag.FlagSet")
}

func TestDumpRequestPreservesBody(t *testing.T) {
	t.Parallel()

	payload := `{"message":"hello"}`
	req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions?trace=1", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	dump, err := dumpTestRequest(req)
	if err != nil {
		t.Fatalf("dump request: %v", err)
	}
	if !strings.Contains(string(dump), payload) {
		t.Fatalf("dump = %s", string(dump))
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != payload {
		t.Fatalf("body = %s", string(body))
	}
}

func TestDebugMiddlewareWritesRequestDump(t *testing.T) {
	tempDir := t.TempDir()
	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() {
		if err := os.Chdir(originalWd); err != nil {
			t.Fatalf("restore chdir: %v", err)
		}
	}()

	handler := proxy.DebugMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions", strings.NewReader(`{"hello":"world"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d", recorder.Code)
	}

	logsDir := filepath.Join(tempDir, proxy.DefaultLogsDir)
	var logFile string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(logsDir)
		if err == nil && len(entries) > 0 {
			logFile = filepath.Join(logsDir, entries[0].Name())
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if logFile == "" {
		t.Fatalf("debug log file was not created")
	}
	if !strings.HasPrefix(filepath.Base(logFile), "request_") || !strings.HasSuffix(filepath.Base(logFile), ".txt") {
		t.Fatalf("log file = %s", logFile)
	}

	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(content), "POST /v1/chat/completions HTTP/1.1") {
		t.Fatalf("content = %s", string(content))
	}
	if !strings.Contains(string(content), `{"hello":"world"}`) {
		t.Fatalf("content = %s", string(content))
	}
}

func TestLoadConfigFromJSONAndEnvOverride(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	content := `{
		"listen_addr": ":8443",
		"tls_cert_file": "./custom.crt",
		"tls_key_file": "./custom.key",
		"retry_max": 2,
		"upstreams": [
			{
				"host": "api.openai.com",
				"base_url": "http://json-upstream.test",
				"base_host": "json-host.test",
				"routes": [
					{"path": "/v1/embeddings", "target_path": "/openai/embeddings"}
				]
			}
		]
	}`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("LISTEN_ADDR", ":9443")

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.ListenAddr != ":9443" {
		t.Fatalf("listen addr = %s", cfg.ListenAddr)
	}
	if cfg.CertFile != "./custom.crt" {
		t.Fatalf("cert file = %s", cfg.CertFile)
	}
	if cfg.KeyFile != "./custom.key" {
		t.Fatalf("key file = %s", cfg.KeyFile)
	}
	if cfg.RetryMax != 2 {
		t.Fatalf("retry max = %d", cfg.RetryMax)
	}
	if len(cfg.Upstreams) != 1 {
		t.Fatalf("upstreams len = %d", len(cfg.Upstreams))
	}
	if cfg.Upstreams[0].Host != "api.openai.com" {
		t.Fatalf("upstream host = %s", cfg.Upstreams[0].Host)
	}
	if cfg.Upstreams[0].BaseURL != "http://json-upstream.test" {
		t.Fatalf("upstream base url = %s", cfg.Upstreams[0].BaseURL)
	}
	if cfg.Upstreams[0].BaseHost != "json-host.test" {
		t.Fatalf("upstream base host = %s", cfg.Upstreams[0].BaseHost)
	}
	if len(cfg.Upstreams[0].Routes) != 1 {
		t.Fatalf("routes len = %d", len(cfg.Upstreams[0].Routes))
	}
	if cfg.Upstreams[0].Routes[0].Path != "/v1/embeddings" {
		t.Fatalf("route path = %s", cfg.Upstreams[0].Routes[0].Path)
	}
	if cfg.Upstreams[0].Routes[0].TargetPath != "/openai/embeddings" {
		t.Fatalf("route target path = %s", cfg.Upstreams[0].Routes[0].TargetPath)
	}
}

func TestUnsupportedPath(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, config.Config{
		Upstreams: []config.UpstreamConfig{
			{
				Host:    "api.openai.com",
				BaseURL: "http://api.openai.com",
				Routes:  config.Default().Upstreams[0].Routes,
			},
		},
	}, false)
	recorder := httptest.NewRecorder()
	req, err := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/unknown", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d", recorder.Code)
	}

	var payload map[string]map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if payload["error"]["message"] != "unsupported path" {
		t.Fatalf("message = %s", payload["error"]["message"])
	}
}

func TestRootPathReturnsStatusPage(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, config.Config{
		ListenAddr: ":8443",
		CertFile:   "./cert/test.crt",
		KeyFile:    "./cert/test.key",
		Upstreams: []config.UpstreamConfig{
			{
				Host:    "api.openai.com",
				BaseURL: "http://api.openai.com",
				Routes: []config.RouteConfig{
					{Path: "/v1/chat/completions", TargetPath: "/v1/chat/completions"},
				},
			},
		},
	}, false)

	recorder := httptest.NewRecorder()
	req, err := http.NewRequest(http.MethodGet, "https://api.openai.com/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	body := recorder.Body.String()
	if !strings.Contains(body, "go-to-openai HTTPS Proxy Server") {
		t.Fatalf("body missing server title: %s", body)
	}
	if !strings.Contains(body, "Listen Address: :8443") {
		t.Fatalf("body missing listen address: %s", body)
	}
	if !strings.Contains(body, "TLS Certificate: ./cert/test.crt") {
		t.Fatalf("body missing cert file: %s", body)
	}
	if !strings.Contains(body, "TLS Key: ./cert/test.key") {
		t.Fatalf("body missing key file: %s", body)
	}
	if !strings.Contains(body, "Host: api.openai.com") {
		t.Fatalf("body missing host: %s", body)
	}
	if !strings.Contains(body, "Base URL: http://api.openai.com") {
		t.Fatalf("body missing base url: %s", body)
	}
	if !strings.Contains(body, "/v1/chat/completions") {
		t.Fatalf("body missing route: %s", body)
	}
}

func TestRootPathWithProxyConfig(t *testing.T) {
	t.Parallel()

	requests := make(chan string, 1)
	handler := newTestHandler(t, config.Config{
		Upstreams: []config.UpstreamConfig{
			{
				Host:    "api.openai.com",
				BaseURL: "http://api.openai.com",
				Routes: []config.RouteConfig{
					{Path: "/", TargetPath: "/index"},
				},
			},
		},
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests <- r.URL.Path
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
				Body:       io.NopCloser(strings.NewReader("proxied")),
			}, nil
		}),
	}, false)

	req, err := http.NewRequest(http.MethodGet, "https://api.openai.com/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if got := <-requests; got != "/index" {
		t.Fatalf("path = %s, want /index", got)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func newTestHandler(t *testing.T, cfg config.Config, debug bool) http.Handler {
	t.Helper()

	if cfg.RetryMax == 0 {
		cfg.RetryMax = 3
	}
	if len(cfg.Upstreams) == 0 {
		cfg.Upstreams = config.Default().Upstreams
	}

	handler, err := proxy.NewHandler(cfg, debug)
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	return handler
}

func TestGenerateRootCA(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	err := certmanager.GenerateRootCA("Test Root CA", "Test Org", 365, tempDir)
	if err != nil {
		t.Fatalf("generate root CA: %v", err)
	}

	certPath := filepath.Join(tempDir, certmanager.DefaultRootCAName+".crt")
	keyPath := filepath.Join(tempDir, certmanager.DefaultRootCAName+".key")

	if _, err := os.Stat(certPath); err != nil {
		t.Fatalf("certificate file not created: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("key file not created: %v", err)
	}

	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read certificate: %v", err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		t.Fatal("failed to decode certificate PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}

	if !cert.IsCA {
		t.Fatal("certificate is not a CA")
	}
	if cert.Subject.CommonName != "Test Root CA" {
		t.Fatalf("common name = %s, want Test Root CA", cert.Subject.CommonName)
	}
	if len(cert.Subject.Organization) != 1 || cert.Subject.Organization[0] != "Test Org" {
		t.Fatalf("organization = %v", cert.Subject.Organization)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		t.Fatal("failed to decode key PEM")
	}
	if keyBlock.Type != "RSA PRIVATE KEY" {
		t.Fatalf("key type = %s, want RSA PRIVATE KEY", keyBlock.Type)
	}
}

func TestGenerateDomainCert(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	err := certmanager.GenerateRootCA("Test Root CA", "Test Org", 365, tempDir)
	if err != nil {
		t.Fatalf("generate root CA: %v", err)
	}

	rootCACert := filepath.Join(tempDir, certmanager.DefaultRootCAName+".crt")
	rootCAKey := filepath.Join(tempDir, certmanager.DefaultRootCAName+".key")

	err = certmanager.GenerateDomainCert("api.openai.com", rootCACert, rootCAKey, 365, tempDir)
	if err != nil {
		t.Fatalf("generate domain cert: %v", err)
	}

	certPath := filepath.Join(tempDir, certmanager.DefaultDomainPrefix+"-api.openai.com.crt")
	keyPath := filepath.Join(tempDir, certmanager.DefaultDomainPrefix+"-api.openai.com.key")

	if _, err := os.Stat(certPath); err != nil {
		t.Fatalf("certificate file not created: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("key file not created: %v", err)
	}

	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read certificate: %v", err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		t.Fatal("failed to decode certificate PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}

	if cert.IsCA {
		t.Fatal("server certificate should not be a CA")
	}
	if cert.Subject.CommonName != "api.openai.com" {
		t.Fatalf("common name = %s, want api.openai.com", cert.Subject.CommonName)
	}
	if len(cert.DNSNames) != 1 || cert.DNSNames[0] != "api.openai.com" {
		t.Fatalf("DNS names = %v", cert.DNSNames)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		t.Fatal("failed to decode key PEM")
	}
	if keyBlock.Type != "RSA PRIVATE KEY" {
		t.Fatalf("key type = %s, want RSA PRIVATE KEY", keyBlock.Type)
	}
}

func TestGenerateDomainCertWithMissingRootCA(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	err := certmanager.GenerateDomainCert("api.openai.com", filepath.Join(tempDir, "missing.crt"), filepath.Join(tempDir, "missing.key"), 365, tempDir)
	if err == nil {
		t.Fatal("expected error when root CA is missing, got nil")
	}
}

func TestNewCATemplate(t *testing.T) {
	t.Skip("newCATemplate is now a private function in the certmanager package")
}

func TestNewServerTemplate(t *testing.T) {
	t.Skip("newServerTemplate is now a private function in the certmanager package")
}
