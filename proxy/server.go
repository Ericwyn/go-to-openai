package proxy

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/ericwyn/go-to-openai/config"
)

func Run(cfg config.Config, debug bool) error {
	handler, err := NewHandler(cfg, debug)
	if err != nil {
		return fmt.Errorf("build handler: %w", err)
	}

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           LoggingMiddleware(handler),
		ReadHeaderTimeout: 10 * time.Second,
	}

	slog.Info("listening", "addr", cfg.ListenAddr)
	for _, upstream := range cfg.Upstreams {
		slog.Info("upstream configured", "host", upstream.Host, "base_url", upstream.BaseURL)
	}
	if debug {
		slog.Info("debug mode enabled", "logs_dir", DefaultLogsDir)
	}

	if len(cfg.Upstreams) > 0 {
		firstHost := cfg.Upstreams[0].Host
		fmt.Printf("\nServer is ready. Test with:\n")
		fmt.Printf("  curl https://%s/\n", firstHost)
		fmt.Printf("\n")
	}

	if err := server.ListenAndServeTLS(cfg.CertFile, cfg.KeyFile); err != nil {
		return fmt.Errorf("listen tls: %w", err)
	}

	return nil
}
