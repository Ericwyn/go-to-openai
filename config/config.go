package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	ListenAddr string           `json:"listen_addr"`
	CertFile   string           `json:"tls_cert_file"`
	KeyFile    string           `json:"tls_key_file"`
	Upstreams  []UpstreamConfig `json:"upstreams"`
	RetryMax   int              `json:"retry_max"`
	Transport  http.RoundTripper `json:"-"`
}

type UpstreamConfig struct {
	Host     string        `json:"host"`
	BaseURL  string        `json:"base_url"`
	BaseHost string        `json:"base_host"`
	Routes   []RouteConfig `json:"routes"`
}

type RouteConfig struct {
	Path       string `json:"path"`
	TargetPath string `json:"target_path"`
}

func Default() Config {
	return Config{
		ListenAddr: ":443",
		CertFile:   NormalizePath("./cert/api.openai.com.crt"),
		KeyFile:    NormalizePath("./cert/api.openai.com.key"),
		RetryMax:   3,
		Upstreams: []UpstreamConfig{
			{
				Host:    "api.openai.com",
				BaseURL: "http://api.openai.com",
				Routes: []RouteConfig{
					{Path: "/v1/chat/completions", TargetPath: "/v1/chat/completions"},
					{Path: "/v1/models", TargetPath: "/v1/models"},
					{Path: "/v1/responses", TargetPath: "/openai/responses"},
				},
			},
		},
	}
}

func Load(configFile string) (Config, error) {
	cfg := Default()

	if err := mergeJSONConfig(&cfg, configFile); err != nil {
		return Config{}, err
	}

	cfg.CertFile = NormalizePath(cfg.CertFile)
	cfg.KeyFile = NormalizePath(cfg.KeyFile)

	applyEnvOverrides(&cfg)

	if err := validateConfig(cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func mergeJSONConfig(cfg *Config, filePath string) error {
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

func applyEnvOverrides(cfg *Config) {
	cfg.ListenAddr = envOrDefault("LISTEN_ADDR", cfg.ListenAddr)
	cfg.CertFile = NormalizePath(envOrDefault("TLS_CERT_FILE", cfg.CertFile))
	cfg.KeyFile = NormalizePath(envOrDefault("TLS_KEY_FILE", cfg.KeyFile))
}

func validateConfig(cfg Config) error {
	if strings.TrimSpace(cfg.ListenAddr) == "" {
		return errors.New("listen_addr is required")
	}
	if strings.TrimSpace(cfg.CertFile) == "" {
		return errors.New("tls_cert_file is required")
	}
	if strings.TrimSpace(cfg.KeyFile) == "" {
		return errors.New("tls_key_file is required")
	}
	if cfg.RetryMax < 0 {
		return errors.New("retry_max must be greater than or equal to 0")
	}
	if len(cfg.Upstreams) == 0 {
		return errors.New("at least one upstream is required")
	}

	seenHosts := make(map[string]struct{}, len(cfg.Upstreams))
	for i, upstream := range cfg.Upstreams {
		if strings.TrimSpace(upstream.Host) == "" {
			return fmt.Errorf("upstreams[%d].host is required", i)
		}
		if _, exists := seenHosts[upstream.Host]; exists {
			return fmt.Errorf("duplicate upstream host: %s", upstream.Host)
		}
		seenHosts[upstream.Host] = struct{}{}

		if strings.TrimSpace(upstream.BaseURL) == "" {
			return fmt.Errorf("upstreams[%d].base_url is required", i)
		}
		upstreamURL, err := url.Parse(upstream.BaseURL)
		if err != nil {
			return fmt.Errorf("upstreams[%d].base_url: %w", i, err)
		}
		if upstreamURL.Scheme == "" || upstreamURL.Host == "" {
			return fmt.Errorf("upstreams[%d].base_url must be a valid absolute URL", i)
		}
		if upstream.BaseHost != "" {
			if strings.Contains(upstream.BaseHost, "://") {
				return fmt.Errorf("upstreams[%d].base_host must be a host, not a URL", i)
			}
			if strings.TrimSpace(upstream.BaseHost) == "" {
				return fmt.Errorf("upstreams[%d].base_host must not be empty", i)
			}
		}
		if len(upstream.Routes) == 0 {
			return fmt.Errorf("upstreams[%d].routes: at least one route is required", i)
		}

		seenPaths := make(map[string]struct{}, len(upstream.Routes))
		for j, route := range upstream.Routes {
			if strings.TrimSpace(route.Path) == "" {
				return fmt.Errorf("upstreams[%d].routes[%d].path is required", i, j)
			}
			if strings.TrimSpace(route.TargetPath) == "" {
				return fmt.Errorf("upstreams[%d].routes[%d].target_path is required", i, j)
			}
			if !strings.HasPrefix(route.Path, "/") {
				return fmt.Errorf("upstreams[%d].routes[%d].path must start with /", i, j)
			}
			if !strings.HasPrefix(route.TargetPath, "/") {
				return fmt.Errorf("upstreams[%d].routes[%d].target_path must start with /", i, j)
			}
			if _, exists := seenPaths[route.Path]; exists {
				return fmt.Errorf("upstreams[%d].routes: duplicate path: %s", i, route.Path)
			}
			seenPaths[route.Path] = struct{}{}
		}
	}

	return nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
