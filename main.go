package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ericwyn/go-to-openai/certmanager"
	"github.com/ericwyn/go-to-openai/config"
	"github.com/ericwyn/go-to-openai/proxy"
)

var version = "v1.0.0 260403"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	if command == "-v" || command == "--version" {
		fmt.Println(version)
		os.Exit(0)
	}

	args := os.Args[2:]

	switch command {
	case "root-crt-gen", "-root-crt-gen":
		handleRootCrtGen(args)
	case "root-crt-install", "-root-crt-install":
		handleRootCrtInstall(args)
	case "root-crt-remove", "-root-crt-remove":
		handleRootCrtRemove(args)
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

func printUsage() {
	fmt.Println(`Usage: go-to-openai <command> [options]

Commands:
  root-crt-gen          Generate root CA certificate
  root-crt-install      Install root CA certificate to system trust store
  root-crt-remove       Remove root CA certificate from system trust store
  domain-crt-gen        Generate domain certificate signed by root CA
  run                   Start HTTPS proxy server

Options:
  -v, --version         Print version information

Examples:
  go-to-openai root-crt-gen
  go-to-openai root-crt-install
  go-to-openai root-crt-remove
  go-to-openai domain-crt-gen api.openai.com
  go-to-openai run -config=./config.json`)
}

func handleRootCrtGen(args []string) {
	flagSet := flag.NewFlagSet("root-crt-gen", flag.ExitOnError)
	caCN := flagSet.String("ca-cn", "Go-To-OpenAI Root CA", "CA common name")
	caOrg := flagSet.String("ca-org", "Go-To-OpenAI", "CA organization")
	caDays := flagSet.Int("ca-days", 3650, "CA certificate validity in days")
	outputDir := flagSet.String("output-dir", "./cert", "certificate output directory")
	_ = flagSet.Parse(args)

	if err := certmanager.GenerateRootCA(*caCN, *caOrg, *caDays, *outputDir); err != nil {
		fmt.Fprintf(os.Stderr, "failed to generate root CA: %v\n", err)
		os.Exit(1)
	}

	certPath := filepath.Join(*outputDir, certmanager.DefaultRootCAName+".crt")
	keyPath := filepath.Join(*outputDir, certmanager.DefaultRootCAName+".key")
	fmt.Printf("root CA certificate generated successfully:\n")
	fmt.Printf("  certificate: %s\n", certPath)
	fmt.Printf("  private key: %s\n", keyPath)
}

func handleRootCrtInstall(args []string) {
	flagSet := flag.NewFlagSet("root-crt-install", flag.ExitOnError)
	certPath := flagSet.String("cert-path", filepath.Join("./cert", certmanager.DefaultRootCAName+".crt"), "root CA certificate path")
	_ = flagSet.Parse(args)

	if err := certmanager.Install(*certPath); err != nil {
		fmt.Fprintf(os.Stderr, "failed to install root CA: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("root CA certificate installed successfully: %s\n", *certPath)
}

func handleRootCrtRemove(args []string) {
	flagSet := flag.NewFlagSet("root-crt-remove", flag.ExitOnError)
	certPath := flagSet.String("cert-path", filepath.Join("./cert", certmanager.DefaultRootCAName+".crt"), "root CA certificate path")
	_ = flagSet.Parse(args)

	if err := certmanager.Remove(*certPath); err != nil {
		fmt.Fprintf(os.Stderr, "failed to remove root CA: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("root CA certificate removed successfully: %s\n", *certPath)
}

func handleDomainCrtGen(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "domain is required\n")
		fmt.Fprintf(os.Stderr, "Usage: go-to-openai domain-crt-gen <domain> [options]\n")
		os.Exit(1)
	}

	domain := args[0]
	flagSet := flag.NewFlagSet("domain-crt-gen", flag.ExitOnError)
	rootCACert := flagSet.String("root-ca-cert", filepath.Join("./cert", certmanager.DefaultRootCAName+".crt"), "root CA certificate path")
	rootCAKey := flagSet.String("root-ca-key", filepath.Join("./cert", certmanager.DefaultRootCAName+".key"), "root CA private key path")
	serverDays := flagSet.Int("server-days", 825, "server certificate validity in days")
	outputDir := flagSet.String("output-dir", "./cert", "certificate output directory")
	_ = flagSet.Parse(args[1:])

	if err := certmanager.GenerateDomainCert(domain, *rootCACert, *rootCAKey, *serverDays, *outputDir); err != nil {
		fmt.Fprintf(os.Stderr, "failed to generate domain certificate: %v\n", err)
		os.Exit(1)
	}

	certPath := filepath.Join(*outputDir, certmanager.DefaultDomainPrefix+"-"+domain+".crt")
	keyPath := filepath.Join(*outputDir, certmanager.DefaultDomainPrefix+"-"+domain+".key")
	fmt.Printf("domain certificate generated successfully for %s:\n", domain)
	fmt.Printf("  certificate: %s\n", certPath)
	fmt.Printf("  private key: %s\n", keyPath)
}

func handleRun(args []string) {
	flagSet := flag.NewFlagSet("run", flag.ExitOnError)
	configFile := flagSet.String("config", "./config.json", "path to config file")
	debug := flagSet.Bool("debug", false, "enable debug request logging")
	_ = flagSet.Parse(args)

	cfg, err := config.Load(*configFile)
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}

	if err := proxy.Run(cfg, *debug); err != nil {
		slog.Error("run proxy failed", "error", err)
		os.Exit(1)
	}
}
