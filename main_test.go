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
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

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
	return &retryTransport{base: base, retryMax: retryMax}
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

func TestChatCompletionsProxy(t *testing.T) {
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
				StatusCode: http.StatusCreated,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			}, nil
		}),
	}, false)

	req, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-4"}`)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusCreated)
	}
	if got := <-requests; got != "/v1/chat/completions" {
		t.Fatalf("path = %s", got)
	}
}

func TestModelsProxy(t *testing.T) {
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
				Body:       io.NopCloser(strings.NewReader(`{"data":[]}`)),
			}, nil
		}),
	}, false)

	req, err := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/models", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := <-requests; got != "/v1/models" {
		t.Fatalf("path = %s", got)
	}
}

func TestUnknownHost(t *testing.T) {
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
	req, err := http.NewRequest(http.MethodGet, "https://unknown.com/v1/models", nil)
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
				Routes:  config.Default().Upstreams[0].Routes,
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
		t.Fatalf("status = %d", recorder.Code)
	}

	body := recorder.Body.String()
	if !strings.Contains(body, "go-to-openai HTTPS Proxy Server") {
		t.Fatalf("body missing server title: %s", body)
	}
}

func TestRetryTransport(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	transport := newTestRetryTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempt := attempts.Add(1)
		if attempt < 3 {
			return nil, &net.OpError{Err: syscall.EPIPE}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
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
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
}

func TestRetryTransportNoRetry(t *testing.T) {
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
		t.Fatalf("attempts = %d, want 1", attempts.Load())
	}
}

func TestLoadConfig(t *testing.T) {
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
				"base_url": "http://test.local",
				"routes": [
					{"path": "/v1/chat", "target_path": "/v1/chat"}
				]
			}
		]
	}`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.ListenAddr != ":8443" {
		t.Fatalf("listen addr = %s", cfg.ListenAddr)
	}
	if cfg.RetryMax != 2 {
		t.Fatalf("retry max = %d", cfg.RetryMax)
	}
	if len(cfg.Upstreams) != 1 {
		t.Fatalf("upstreams len = %d", len(cfg.Upstreams))
	}
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
		t.Fatalf("common name = %s", cert.Subject.CommonName)
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
	if _, err := os.Stat(certPath); err != nil {
		t.Fatalf("certificate file not created: %v", err)
	}

	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read certificate: %v", err)
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
		t.Fatalf("common name = %s", cert.Subject.CommonName)
	}
}
