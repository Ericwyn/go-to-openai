package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
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
	defaultLogsDir    = ".logs"
)

type config struct {
	ListenAddr   string            `json:"listen_addr"`
	CertFile     string            `json:"tls_cert_file"`
	KeyFile      string            `json:"tls_key_file"`
	Upstream     string            `json:"upstream_base_url"`
	UpstreamHost string            `json:"upstream_base_host"`
	Routes       []routeConfig     `json:"routes"`
	RetryMax     int               `json:"retry_max"`
	Transport    http.RoundTripper `json:"-"`
}

type routeConfig struct {
	Path       string `json:"path"`
	TargetPath string `json:"target_path"`
}

type startupOptions struct {
	ConfigFile string
	Debug      bool
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

func parseFlags(args []string) startupOptions {
	flagSet := flag.NewFlagSet("go-to-openai", flag.ExitOnError)
	configFile := flagSet.String("config", defaultConfigFile, "path to config file")
	debug := flagSet.Bool("debug", false, "enable debug request logging")
	_ = flagSet.Parse(args)
	return startupOptions{
		ConfigFile: strings.TrimSpace(*configFile),
		Debug:      *debug,
	}
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
	cfg.UpstreamHost = envOrDefault("UPSTREAM_BASE_HOST", cfg.UpstreamHost)
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
	upstreamURL, err := url.Parse(cfg.Upstream)
	if err != nil {
		return err
	}
	if upstreamURL.Scheme == "" || upstreamURL.Host == "" {
		return errors.New("upstream_base_url must be a valid absolute URL")
	}
	if cfg.UpstreamHost != "" {
		if strings.Contains(cfg.UpstreamHost, "://") {
			return errors.New("upstream_base_host must be a host, not a URL")
		}
		if strings.TrimSpace(cfg.UpstreamHost) == "" {
			return errors.New("upstream_base_host must not be empty")
		}
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
	opts := parseFlags(os.Args[1:])
	cfg, err := loadConfig(opts.ConfigFile)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	handler, err := newHandler(cfg, opts.Debug)
	if err != nil {
		log.Fatalf("build handler: %v", err)
	}

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           loggingMiddleware(handler),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("[listening] : %s", cfg.ListenAddr)
	log.Printf("[proxying] : %s", cfg.Upstream)
	if opts.Debug {
		log.Printf("debug mode enabled, request dumps will be written to %s", defaultLogsDir)
	}
	if err := server.ListenAndServeTLS(cfg.CertFile, cfg.KeyFile); err != nil {
		log.Fatalf("listen tls: %v", err)
	}
}

func newHandler(cfg config, debug bool) (http.Handler, error) {
	target, err := url.Parse(cfg.Upstream)
	if err != nil {
		return nil, err
	}

	upstreamHost := target.Host
	if cfg.UpstreamHost != "" {
		upstreamHost = cfg.UpstreamHost
	}

	routes := make([]route, 0, len(cfg.Routes))
	for _, item := range cfg.Routes {
		routes = append(routes, route{
			Path:  item.Path,
			Proxy: newRouteProxy(target, upstreamHost, item.TargetPath, cfg.Transport, cfg.RetryMax),
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

	if debug {
		return debugMiddleware(mux), nil
	}

	return mux, nil
}

func newRouteProxy(target *url.URL, upstreamHost, targetPath string, transport http.RoundTripper, retryMax int) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	proxy.Transport = newRetryTransport(transportOrDefault(transport), retryMax)

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		incomingHost := req.Host
		originalDirector(req)
		req.URL.Path = targetPath
		req.URL.RawPath = ""
		req.Host = upstreamHost
		req.Header.Set("Host", upstreamHost)
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

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(wrapped, r)
		log.Printf("request completed: method=%s path=%s status=%d duration=%s", r.Method, r.URL.Path, wrapped.statusCode, time.Since(start))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func writeJSONError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]map[string]string{
		"error": {
			"message": message,
		},
	})
}

func debugMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, err := dumpRequest(r)
		if err != nil {
			log.Printf("dump request error: method=%s path=%s err=%v", r.Method, r.URL.Path, err)
		} else {
			go writeDebugRequest(payload)
		}
		next.ServeHTTP(w, r)
	})
}

func dumpRequest(r *http.Request) ([]byte, error) {
	body, err := snapshotRequestBody(r)
	if err != nil {
		return nil, err
	}

	clonedReq, err := cloneRequest(r, body)
	if err != nil {
		return nil, err
	}

	return httputil.DumpRequest(clonedReq, true)
}

func writeDebugRequest(payload []byte) {
	if err := os.MkdirAll(defaultLogsDir, 0o755); err != nil {
		log.Printf("create debug log dir error: err=%v", err)
		return
	}

	filePath := filepath.Join(defaultLogsDir, fmt.Sprintf("request_%d.txt", time.Now().UnixNano()))
	if err := os.WriteFile(filePath, payload, 0o600); err != nil {
		log.Printf("write debug request error: path=%s err=%v", filePath, err)
	}
}
