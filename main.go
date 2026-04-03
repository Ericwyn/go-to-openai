package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const (
	defaultListenAddr = ":443"
	defaultCertFile   = "./cert/api.openai.com.crt"
	defaultKeyFile    = "./cert/api.openai.com.key"
	defaultConfigFile = "./config.json"
	defaultRetryMax   = 3
	defaultLogsDir    = ".logs"

	defaultRootCAName   = "goto-openai-root"
	defaultDomainPrefix = "goto-openai-dm"
)

type config struct {
	ListenAddr string            `json:"listen_addr"`
	CertFile   string            `json:"tls_cert_file"`
	KeyFile    string            `json:"tls_key_file"`
	Upstreams  []upstreamConfig  `json:"upstreams"`
	RetryMax   int               `json:"retry_max"`
	Transport  http.RoundTripper `json:"-"`
}

type upstreamConfig struct {
	Host     string        `json:"host"`
	BaseURL  string        `json:"base_url"`
	BaseHost string        `json:"base_host"`
	Routes   []routeConfig `json:"routes"`
}

type routeConfig struct {
	Path       string `json:"path"`
	TargetPath string `json:"target_path"`
}

type hostRoute struct {
	Path  string
	Proxy *httputil.ReverseProxy
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
		RetryMax:   defaultRetryMax,
		Upstreams: []upstreamConfig{
			{
				Host:    "api.openai.com",
				BaseURL: "http://api.openai.com",
				Routes: []routeConfig{
					{Path: "/v1/chat/completions", TargetPath: "/v1/chat/completions"},
					{Path: "/v1/models", TargetPath: "/v1/models"},
					{Path: "/v1/responses", TargetPath: "/openai/responses"},
				},
			},
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

func parseRunFlags(args []string) startupOptions {
	flagSet := flag.NewFlagSet("run", flag.ExitOnError)
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

func printUsage() {
	fmt.Println(`Usage: go-to-openai <command> [options]

Commands:
  root-crt-gen          Generate root CA certificate
  root-crt-install      Install root CA certificate to system trust store
  domain-crt-gen        Generate domain certificate signed by root CA
  run                   Start HTTPS proxy server

Examples:
  go-to-openai root-crt-gen
  go-to-openai root-crt-install
  go-to-openai domain-crt-gen api.openai.com
  go-to-openai run -config=./config.json`)
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "root-crt-gen", "-root-crt-gen":
		handleRootCrtGen(args)
	case "root-crt-install", "-root-crt-install":
		handleRootCrtInstall(args)
	case "domain-crt-gen", "-domain-crt-gen":
		handleDomainCrtGen(args)
	case "run", "-run":
		handleRun(args)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func handleRootCrtGen(args []string) {
	flagSet := flag.NewFlagSet("root-crt-gen", flag.ExitOnError)
	caCN := flagSet.String("ca-cn", "Go-To-OpenAI Root CA", "CA common name")
	caOrg := flagSet.String("ca-org", "Go-To-OpenAI", "CA organization")
	caDays := flagSet.Int("ca-days", 3650, "CA certificate validity in days")
	outputDir := flagSet.String("output-dir", "./cert", "certificate output directory")
	_ = flagSet.Parse(args)

	if err := generateRootCA(*caCN, *caOrg, *caDays, *outputDir); err != nil {
		fmt.Fprintf(os.Stderr, "failed to generate root CA: %v\n", err)
		os.Exit(1)
	}

	certPath := filepath.Join(*outputDir, defaultRootCAName+".crt")
	keyPath := filepath.Join(*outputDir, defaultRootCAName+".key")
	fmt.Printf("root CA certificate generated successfully:\n")
	fmt.Printf("  certificate: %s\n", certPath)
	fmt.Printf("  private key: %s\n", keyPath)
}

func handleRootCrtInstall(args []string) {
	flagSet := flag.NewFlagSet("root-crt-install", flag.ExitOnError)
	certPath := flagSet.String("cert-path", filepath.Join("./cert", defaultRootCAName+".crt"), "root CA certificate path")
	_ = flagSet.Parse(args)

	if err := installRootCA(*certPath); err != nil {
		fmt.Fprintf(os.Stderr, "failed to install root CA: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("root CA certificate installed successfully: %s\n", *certPath)
}

func handleDomainCrtGen(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "domain is required\n")
		fmt.Fprintf(os.Stderr, "Usage: go-to-openai domain-crt-gen <domain> [options]\n")
		os.Exit(1)
	}

	domain := args[0]
	flagSet := flag.NewFlagSet("domain-crt-gen", flag.ExitOnError)
	rootCACert := flagSet.String("root-ca-cert", filepath.Join("./cert", defaultRootCAName+".crt"), "root CA certificate path")
	rootCAKey := flagSet.String("root-ca-key", filepath.Join("./cert", defaultRootCAName+".key"), "root CA private key path")
	serverDays := flagSet.Int("server-days", 825, "server certificate validity in days")
	outputDir := flagSet.String("output-dir", "./cert", "certificate output directory")
	_ = flagSet.Parse(args[1:])

	if err := generateDomainCert(domain, *rootCACert, *rootCAKey, *serverDays, *outputDir); err != nil {
		fmt.Fprintf(os.Stderr, "failed to generate domain certificate: %v\n", err)
		os.Exit(1)
	}

	certPath := filepath.Join(*outputDir, defaultDomainPrefix+"-"+domain+".crt")
	keyPath := filepath.Join(*outputDir, defaultDomainPrefix+"-"+domain+".key")
	fmt.Printf("domain certificate generated successfully for %s:\n", domain)
	fmt.Printf("  certificate: %s\n", certPath)
	fmt.Printf("  private key: %s\n", keyPath)
}

func handleRun(args []string) {
	opts := parseRunFlags(args)
	cfg, err := loadConfig(opts.ConfigFile)
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}

	handler, err := newHandler(cfg, opts.Debug)
	if err != nil {
		slog.Error("build handler failed", "error", err)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           loggingMiddleware(handler),
		ReadHeaderTimeout: 10 * time.Second,
	}

	slog.Info("listening", "addr", cfg.ListenAddr)
	for _, upstream := range cfg.Upstreams {
		slog.Info("upstream configured", "host", upstream.Host, "base_url", upstream.BaseURL)
	}
	if opts.Debug {
		slog.Info("debug mode enabled", "logs_dir", defaultLogsDir)
	}
	if err := server.ListenAndServeTLS(cfg.CertFile, cfg.KeyFile); err != nil {
		slog.Error("listen tls failed", "error", err)
		os.Exit(1)
	}
}

func generateRootCA(caCN, caOrg string, days int, outputDir string) error {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generate RSA key: %w", err)
	}

	caTemplate, err := newCATemplate(caCN, caOrg, days)
	if err != nil {
		return fmt.Errorf("create CA template: %w", err)
	}

	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("create CA certificate: %w", err)
	}

	caCertPath := filepath.Join(outputDir, defaultRootCAName+".crt")
	caKeyPath := filepath.Join(outputDir, defaultRootCAName+".key")

	if err := writeCertificate(caCertPath, caDER); err != nil {
		return fmt.Errorf("write CA certificate: %w", err)
	}

	if err := writePrivateKey(caKeyPath, caKey); err != nil {
		return fmt.Errorf("write CA private key: %w", err)
	}

	return nil
}

func installRootCA(certPath string) error {
	if _, err := os.Stat(certPath); err != nil {
		return fmt.Errorf("certificate file not found: %s", certPath)
	}

	switch runtime.GOOS {
	case "linux":
		return installRootCALinux(certPath)
	case "darwin":
		return installRootCAMacOS(certPath)
	case "windows":
		return installRootCAWindows(certPath)
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}

func installRootCALinux(certPath string) error {
	linuxDistro := detectLinuxDistro()

	var destDir string
	var updateCmd string

	switch linuxDistro {
	case "debian", "ubuntu":
		destDir = "/usr/local/share/ca-certificates"
		updateCmd = "update-ca-certificates"
	case "rhel", "centos", "fedora", "amzn":
		destDir = "/etc/pki/ca-trust/source/anchors"
		updateCmd = "update-ca-trust"
	default:
		destDir = "/usr/local/share/ca-certificates"
		updateCmd = "update-ca-certificates"
		fmt.Printf("detected Linux distribution: %s (using Debian/Ubuntu method)\n", linuxDistro)
	}

	destFile := filepath.Join(destDir, filepath.Base(certPath))

	copyCmd := exec.Command("cp", certPath, destFile)
	copyCmd.Stdout = os.Stdout
	copyCmd.Stderr = os.Stderr
	if err := copyCmd.Run(); err != nil {
		return fmt.Errorf("copy certificate: %w (try running with sudo)", err)
	}

	updateCmdParts := strings.Fields(updateCmd)
	updateCmdExec := exec.Command(updateCmdParts[0], updateCmdParts[1:]...)
	updateCmdExec.Stdout = os.Stdout
	updateCmdExec.Stderr = os.Stderr
	if err := updateCmdExec.Run(); err != nil {
		return fmt.Errorf("update certificate store: %w (try running with sudo)", err)
	}

	return nil
}

func detectLinuxDistro() string {
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		content := string(data)
		if strings.Contains(content, "ubuntu") {
			return "ubuntu"
		}
		if strings.Contains(content, "debian") {
			return "debian"
		}
		if strings.Contains(content, "rhel") {
			return "rhel"
		}
		if strings.Contains(content, "centos") {
			return "centos"
		}
		if strings.Contains(content, "fedora") {
			return "fedora"
		}
		if strings.Contains(content, "amzn") {
			return "amzn"
		}
	}

	if _, err := exec.LookPath("lsb_release"); err == nil {
		cmd := exec.Command("lsb_release", "-si")
		if output, err := cmd.Output(); err == nil {
			return strings.ToLower(strings.TrimSpace(string(output)))
		}
	}

	return "unknown"
}

func installRootCAMacOS(certPath string) error {
	cmd := exec.Command("sudo", "security", "add-trusted-cert", "-d", "-r", "trustRoot",
		"-k", "/Library/Keychains/System.keychain", certPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("install certificate: %w", err)
	}
	return nil
}

func installRootCAWindows(certPath string) error {
	absPath, err := filepath.Abs(certPath)
	if err != nil {
		return fmt.Errorf("get absolute path: %w", err)
	}

	cmd := exec.Command("certutil", "-addstore", "Root", absPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("install certificate: %w (try running as Administrator)", err)
	}
	return nil
}

func generateDomainCert(domain, rootCACertPath, rootCAKeyPath string, days int, outputDir string) error {
	if _, err := os.Stat(rootCACertPath); err != nil {
		return fmt.Errorf("root CA certificate not found: %s (run 'root-crt-gen' first)", rootCACertPath)
	}
	if _, err := os.Stat(rootCAKeyPath); err != nil {
		return fmt.Errorf("root CA private key not found: %s (run 'root-crt-gen' first)", rootCAKeyPath)
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	caCertPEM, err := os.ReadFile(rootCACertPath)
	if err != nil {
		return fmt.Errorf("read root CA certificate: %w", err)
	}

	caKeyPEM, err := os.ReadFile(rootCAKeyPath)
	if err != nil {
		return fmt.Errorf("read root CA private key: %w", err)
	}

	caCertBlock, _ := pem.Decode(caCertPEM)
	if caCertBlock == nil {
		return errors.New("failed to decode root CA certificate PEM")
	}

	caCert, err := x509.ParseCertificate(caCertBlock.Bytes)
	if err != nil {
		return fmt.Errorf("parse root CA certificate: %w", err)
	}

	caKeyBlock, _ := pem.Decode(caKeyPEM)
	if caKeyBlock == nil {
		return errors.New("failed to decode root CA private key PEM")
	}

	caKey, err := x509.ParsePKCS1PrivateKey(caKeyBlock.Bytes)
	if err != nil {
		return fmt.Errorf("parse root CA private key: %w", err)
	}

	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generate server RSA key: %w", err)
	}

	domains := []string{domain}
	serverTemplate, err := newServerTemplate(domains, "Go-To-OpenAI Server", days)
	if err != nil {
		return fmt.Errorf("create server template: %w", err)
	}

	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("create server certificate: %w", err)
	}

	certFileName := defaultDomainPrefix + "-" + domain + ".crt"
	keyFileName := defaultDomainPrefix + "-" + domain + ".key"

	certPath := filepath.Join(outputDir, certFileName)
	keyPath := filepath.Join(outputDir, keyFileName)

	if err := writeCertificate(certPath, serverDER); err != nil {
		return fmt.Errorf("write server certificate: %w", err)
	}

	if err := writePrivateKey(keyPath, serverKey); err != nil {
		return fmt.Errorf("write server private key: %w", err)
	}

	return nil
}

func newCATemplate(commonName, organization string, days int) (*x509.Certificate, error) {
	serialNumber, err := randomSerialNumber()
	if err != nil {
		return nil, err
	}
	notBefore := time.Now().Add(-time.Hour)
	notAfter := notBefore.Add(time.Duration(days) * 24 * time.Hour)
	return &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{organization},
			Country:      []string{"CN"},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}, nil
}

func newServerTemplate(domains []string, organization string, days int) (*x509.Certificate, error) {
	serialNumber, err := randomSerialNumber()
	if err != nil {
		return nil, err
	}
	notBefore := time.Now().Add(-time.Hour)
	notAfter := notBefore.Add(time.Duration(days) * 24 * time.Hour)
	return &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   domains[0],
			Organization: []string{organization},
		},
		NotBefore:   notBefore,
		NotAfter:    notAfter,
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    extractDNSNames(domains),
		IPAddresses: extractIPAddresses(domains),
	}, nil
}

func extractDNSNames(domains []string) []string {
	dnsNames := make([]string, 0, len(domains))
	for _, domain := range domains {
		if net.ParseIP(domain) == nil {
			dnsNames = append(dnsNames, domain)
		}
	}
	return dnsNames
}

func extractIPAddresses(domains []string) []net.IP {
	ips := make([]net.IP, 0, len(domains))
	for _, domain := range domains {
		if ip := net.ParseIP(domain); ip != nil {
			ips = append(ips, ip)
		}
	}
	return ips
}

func randomSerialNumber() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, limit)
}

func writePrivateKey(path string, key *rsa.PrivateKey) error {
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	return writePEMFile(path, block, 0o600)
}

func writeCertificate(path string, der []byte) error {
	block := &pem.Block{Type: "CERTIFICATE", Bytes: der}
	return writePEMFile(path, block, 0o644)
}

func writePEMFile(path string, block *pem.Block, perm os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if err := pem.Encode(file, block); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func newHandler(cfg config, debug bool) (http.Handler, error) {
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
		return debugMiddleware(mux), nil
	}

	return mux, nil
}

func writeStatusPage(w http.ResponseWriter, cfg config, hostRoutes map[string][]hostRoute) {
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

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(wrapped, r)
		slog.Info("request completed",
			"method", r.Method,
			"host", r.Host,
			"path", r.URL.Path,
			"status", wrapped.statusCode,
			"duration", time.Since(start).String(),
		)
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
			slog.Error("dump request failed",
				"method", r.Method,
				"path", r.URL.Path,
				"error", err,
			)
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
		slog.Error("create debug log dir failed", "error", err)
		return
	}

	filePath := filepath.Join(defaultLogsDir, fmt.Sprintf("request_%d.txt", time.Now().UnixNano()))
	if err := os.WriteFile(filePath, payload, 0o600); err != nil {
		slog.Error("write debug request failed", "path", filePath, "error", err)
	}
}
