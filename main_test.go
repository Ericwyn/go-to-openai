package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
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
		Body          []byte
	}

	requests := make(chan capturedRequest, 1)
	handler := newTestHandler(t, config{
		Upstream: defaultUpstream,
		Routes:   defaultConfig().Routes,
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
				Body:          body,
			}

			return &http.Response{
				StatusCode: http.StatusCreated,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			}, nil
		}),
	})

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
	handler := newTestHandler(t, config{
		Upstream: defaultUpstream,
		Routes:   defaultConfig().Routes,
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
	})

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
	handler := newTestHandler(t, config{
		Upstream: defaultUpstream,
		Routes:   defaultConfig().Routes,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests <- r.URL.Path
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader("data: hello\n\ndata: done\n\n")),
			}, nil
		}),
	})

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
	handler := newTestHandler(t, config{
		Upstream: defaultUpstream,
		Routes: []routeConfig{
			{Path: "/v1/embeddings", TargetPath: "/openai/embeddings"},
		},
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests <- r.URL.Path
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			}, nil
		}),
	})

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

func TestRetryTransportRetriesRetryableError(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	transport := newRetryTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
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

func TestRetryTransportDoesNotRetryNonRetryableError(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	expectedErr := errors.New("bad request")
	transport := newRetryTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
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

func TestParseFlags(t *testing.T) {
	t.Parallel()

	if got := parseFlags(nil); got != defaultConfigFile {
		t.Fatalf("default config file = %s", got)
	}

	custom := "/tmp/custom-config.json"
	if got := parseFlags([]string{"-config", custom}); got != custom {
		t.Fatalf("config file = %s", got)
	}
}

func TestLoadConfigFromJSONAndEnvOverride(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	content := `{
		"listen_addr": ":8443",
		"tls_cert_file": "./custom.crt",
		"tls_key_file": "./custom.key",
		"upstream_base_url": "http://json-upstream.test",
		"retry_max": 2,
		"routes": [
			{
				"path": "/v1/embeddings",
				"target_path": "/openai/embeddings"
			}
		]
	}`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("UPSTREAM_BASE_URL", "http://env-upstream.test")

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.ListenAddr != ":8443" {
		t.Fatalf("listen addr = %s", cfg.ListenAddr)
	}
	if cfg.CertFile != "./custom.crt" {
		t.Fatalf("cert file = %s", cfg.CertFile)
	}
	if cfg.KeyFile != "./custom.key" {
		t.Fatalf("key file = %s", cfg.KeyFile)
	}
	if cfg.Upstream != "http://env-upstream.test" {
		t.Fatalf("upstream = %s", cfg.Upstream)
	}
	if cfg.RetryMax != 2 {
		t.Fatalf("retry max = %d", cfg.RetryMax)
	}
	if len(cfg.Routes) != 1 {
		t.Fatalf("routes len = %d", len(cfg.Routes))
	}
	if cfg.Routes[0].Path != "/v1/embeddings" {
		t.Fatalf("route path = %s", cfg.Routes[0].Path)
	}
	if cfg.Routes[0].TargetPath != "/openai/embeddings" {
		t.Fatalf("route target path = %s", cfg.Routes[0].TargetPath)
	}
}

func TestUnsupportedPath(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, config{
		Upstream: defaultUpstream,
		Routes:   defaultConfig().Routes,
	})
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

func newTestHandler(t *testing.T, cfg config) http.Handler {
	t.Helper()

	if cfg.Upstream == "" {
		cfg.Upstream = defaultUpstream
	}
	if cfg.RetryMax == 0 {
		cfg.RetryMax = defaultRetryMax
	}
	if len(cfg.Routes) == 0 {
		cfg.Routes = defaultConfig().Routes
	}

	handler, err := newHandler(cfg)
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	return handler
}
