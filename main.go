package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
)

const (
	defaultListenAddr = ":443"
	defaultCertFile   = "./cert/api.openai.com.crt"
	defaultKeyFile    = "./cert/api.openai.com.key"
	defaultUpstream   = "http://api.openai.com"
	defaultConfigFile = "./config.json"
	defaultRetryMax   = 3
)

type config struct {
	ListenAddr string            `json:"listen_addr"`
	CertFile   string            `json:"tls_cert_file"`
	KeyFile    string            `json:"tls_key_file"`
	Upstream   string            `json:"upstream_base_url"`
	Routes     []routeConfig     `json:"routes"`
	RetryMax   int               `json:"retry_max"`
	Transport  http.RoundTripper `json:"-"`
}

type routeConfig struct {
	Path       string `json:"path"`
	TargetPath string `json:"target_path"`
}

func defaultConfig() config {
	return config{
		ListenAddr: defaultListenAddr,
		CertFile:   defaultCertFile,
		KeyFile:    defaultKeyFile,
		Upstream:   defaultUpstream,
		RetryMax:   defaultRetryMax,
		Routes: []routeConfig{
			{Path: "/v1/chat/completions", TargetPath: "/v1/chat/completions"},
			{Path: "/v1/models", TargetPath: "/v1/models"},
			{Path: "/v1/responses", TargetPath: "/openai/responses"},
		},
	}
}

func loadConfig(configFile string) (config, error) {
	cfg := defaultConfig()

	if err := mergeJSONConfig(&cfg, configFile); err != nil {
		return config{}, err
	}

	applyEnvOverrides(&cfg)

	if err := validateConfig(cfg); err != nil {
		return config{}, err
	}

	return cfg, nil
}

func parseFlags(args []string) string {
	flagSet := flag.NewFlagSet("go-to-openai", flag.ExitOnError)
	configFile := flagSet.String("config", defaultConfigFile, "path to config file")
	_ = flagSet.Parse(args)
	return strings.TrimSpace(*configFile)
}

func mergeJSONConfig(cfg *config, filePath string) error {
	if strings.TrimSpace(filePath) == "" {
		return nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	if len(data) == 0 {
		return nil
	}

	return json.Unmarshal(data, cfg)
}

func applyEnvOverrides(cfg *config) {
	cfg.ListenAddr = envOrDefault("LISTEN_ADDR", cfg.ListenAddr)
	cfg.CertFile = envOrDefault("TLS_CERT_FILE", cfg.CertFile)
	cfg.KeyFile = envOrDefault("TLS_KEY_FILE", cfg.KeyFile)
	cfg.Upstream = envOrDefault("UPSTREAM_BASE_URL", cfg.Upstream)
}

func validateConfig(cfg config) error {
	if strings.TrimSpace(cfg.ListenAddr) == "" {
		return errors.New("listen_addr is required")
	}
	if strings.TrimSpace(cfg.CertFile) == "" {
		return errors.New("tls_cert_file is required")
	}
	if strings.TrimSpace(cfg.KeyFile) == "" {
		return errors.New("tls_key_file is required")
	}
	if strings.TrimSpace(cfg.Upstream) == "" {
		return errors.New("upstream_base_url is required")
	}
	if _, err := url.Parse(cfg.Upstream); err != nil {
		return err
	}
	if cfg.RetryMax < 0 {
		return errors.New("retry_max must be greater than or equal to 0")
	}
	if len(cfg.Routes) == 0 {
		return errors.New("at least one route is required")
	}

	seenPaths := make(map[string]struct{}, len(cfg.Routes))
	for _, route := range cfg.Routes {
		if strings.TrimSpace(route.Path) == "" {
			return errors.New("route.path is required")
		}
		if strings.TrimSpace(route.TargetPath) == "" {
			return errors.New("route.target_path is required")
		}
		if !strings.HasPrefix(route.Path, "/") {
			return errors.New("route.path must start with /")
		}
		if !strings.HasPrefix(route.TargetPath, "/") {
			return errors.New("route.target_path must start with /")
		}
		if _, exists := seenPaths[route.Path]; exists {
			return errors.New("duplicate route.path: " + route.Path)
		}
		seenPaths[route.Path] = struct{}{}
	}

	return nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}

	return fallback
}

type route struct {
	Path  string
	Proxy *httputil.ReverseProxy
}

func main() {
	configFile := parseFlags(os.Args[1:])
	cfg, err := loadConfig(configFile)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	handler, err := newHandler(cfg)
	if err != nil {
		log.Fatalf("build handler: %v", err)
	}

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           loggingMiddleware(handler),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("listening on %s and proxying to %s", cfg.ListenAddr, cfg.Upstream)
	if err := server.ListenAndServeTLS(cfg.CertFile, cfg.KeyFile); err != nil {
		log.Fatalf("listen tls: %v", err)
	}
}

func newHandler(cfg config) (http.Handler, error) {
	target, err := url.Parse(cfg.Upstream)
	if err != nil {
		return nil, err
	}

	routes := make([]route, 0, len(cfg.Routes))
	for _, item := range cfg.Routes {
		routes = append(routes, route{
			Path:  item.Path,
			Proxy: newRouteProxy(target, item.TargetPath, cfg.Transport, cfg.RetryMax),
		})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	for _, item := range routes {
		routeItem := item
		mux.Handle(routeItem.Path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			routeItem.Proxy.ServeHTTP(w, r)
		}))
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSONError(w, http.StatusNotFound, "unsupported path")
	})

	return mux, nil
}

func newRouteProxy(target *url.URL, targetPath string, transport http.RoundTripper, retryMax int) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	proxy.Transport = newRetryTransport(transportOrDefault(transport), retryMax)

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		incomingHost := req.Host
		originalDirector(req)
		req.URL.Path = targetPath
		req.URL.RawPath = ""
		req.Host = target.Host
		if incomingHost != "" {
			req.Header.Set("X-Forwarded-Host", incomingHost)
		}
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("proxy error: method=%s path=%s err=%v", r.Method, r.URL.Path, err)
		writeJSONError(w, http.StatusBadGateway, "upstream request failed")
	}

	return proxy
}

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

		log.Printf("retry upstream request: method=%s path=%s attempt=%d/%d err=%v", req.Method, req.URL.Path, attempt, attempts, roundTripErr)
	}

	return nil, errors.New("unreachable retry state")
}

func snapshotRequestBody(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	if req.GetBody != nil {
		bodyReader, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		defer bodyReader.Close()
		return io.ReadAll(bodyReader)
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	if len(body) == 0 {
		req.GetBody = func() (io.ReadCloser, error) {
			return http.NoBody, nil
		}
		return body, nil
	}
	bodyCopy := append([]byte(nil), body...)
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(bodyCopy)), nil
	}
	return body, nil
}

func cloneRequest(req *http.Request, body []byte) (*http.Request, error) {
	clonedReq := req.Clone(req.Context())
	if len(body) == 0 {
		clonedReq.Body = http.NoBody
		clonedReq.GetBody = func() (io.ReadCloser, error) {
			return http.NoBody, nil
		}
		clonedReq.ContentLength = 0
		return clonedReq, nil
	}

	bodyCopy := append([]byte(nil), body...)
	clonedReq.Body = io.NopCloser(bytes.NewReader(bodyCopy))
	clonedReq.ContentLength = int64(len(bodyCopy))
	clonedReq.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(bodyCopy)), nil
	}
	return clonedReq, nil
}

func isRetryableProxyError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ETIMEDOUT) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}

	message := strings.ToLower(err.Error())
	return strings.Contains(message, "use of closed network connection") ||
		strings.Contains(message, "connection reset by peer") ||
		strings.Contains(message, "broken pipe") ||
		strings.Contains(message, "connection refused") ||
		strings.Contains(message, "timeout")
}

func transportOrDefault(transport http.RoundTripper) http.RoundTripper {
	if transport != nil {
		return transport
	}

	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"message": message,
			"type":    "proxy_error",
		},
	})
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("request method=%s path=%s remote=%s duration=%s", r.Method, r.URL.Path, r.RemoteAddr, time.Since(startedAt).Truncate(time.Millisecond))
	})
}
