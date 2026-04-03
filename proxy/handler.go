package proxy

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/ericwyn/go-to-openai/config"
)

type hostRoute struct {
	Path  string
	Proxy *httputil.ReverseProxy
}

func NewHandler(cfg config.Config, debug bool) (http.Handler, error) {
	hostRoutes := make(map[string][]hostRoute)

	for _, upstream := range cfg.Upstreams {
		target, err := url.Parse(upstream.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("upstream %s: %w", upstream.Host, err)
		}

		upstreamHost := target.Host
		if upstream.BaseHost != "" {
			upstreamHost = upstream.BaseHost
		}

		routes := make([]hostRoute, 0, len(upstream.Routes))
		for _, item := range upstream.Routes {
			routes = append(routes, hostRoute{
				Path:  item.Path,
				Proxy: newRouteProxy(target, upstreamHost, item.TargetPath, cfg.Transport, cfg.RetryMax),
			})
		}
		hostRoutes[upstream.Host] = routes
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}

		routes, ok := hostRoutes[host]
		if !ok {
			writeJSONError(w, http.StatusNotFound, "unknown host")
			return
		}

		for _, rt := range routes {
			if r.URL.Path == rt.Path {
				rt.Proxy.ServeHTTP(w, r)
				return
			}
		}

		if r.URL.Path == "/" {
			writeStatusPage(w, cfg, hostRoutes)
			return
		}

		writeJSONError(w, http.StatusNotFound, "unsupported path")
	})

	if debug {
		return DebugMiddleware(mux), nil
	}

	return mux, nil
}

func writeStatusPage(w http.ResponseWriter, cfg config.Config, hostRoutes map[string][]hostRoute) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	var buf strings.Builder
	buf.WriteString("go-to-openai HTTPS Proxy Server\n")
	buf.WriteString(strings.Repeat("=", 40) + "\n\n")

	buf.WriteString(fmt.Sprintf("Listen Address: %s\n", cfg.ListenAddr))
	buf.WriteString(fmt.Sprintf("TLS Certificate: %s\n", cfg.CertFile))
	buf.WriteString(fmt.Sprintf("TLS Key: %s\n\n", cfg.KeyFile))

	buf.WriteString("Upstream Configurations:\n")
	buf.WriteString(strings.Repeat("-", 40) + "\n")

	for _, upstream := range cfg.Upstreams {
		buf.WriteString(fmt.Sprintf("\nHost: %s\n", upstream.Host))
		buf.WriteString(fmt.Sprintf("  Base URL: %s\n", upstream.BaseURL))
		if upstream.BaseHost != "" {
			buf.WriteString(fmt.Sprintf("  Base Host: %s\n", upstream.BaseHost))
		}
		buf.WriteString("  Routes:\n")

		routes := hostRoutes[upstream.Host]
		for _, rt := range routes {
			buf.WriteString(fmt.Sprintf("    %s -> %s\n", rt.Path, rt.Path))
		}
	}

	buf.WriteString("\n")
	buf.WriteString("Access /healthz for health check.\n")

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(buf.String()))
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
		slog.Error("proxy request failed",
			"method", r.Method,
			"path", r.URL.Path,
			"host", r.Host,
			"error", err,
		)
		writeJSONError(w, http.StatusBadGateway, "upstream request failed")
	}

	return proxy
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
