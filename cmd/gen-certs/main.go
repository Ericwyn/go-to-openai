package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultOutputDir = "./cert"
	defaultCAName    = "openaica"
)

type certFiles struct {
	CAKey      string
	CACert     string
	ServerKey  string
	ServerCSR  string
	ServerCert string
}

func main() {
	outputDir := flag.String("output", defaultOutputDir, "certificate output directory")
	domainsArg := flag.String("domains", "api.openai.com", "comma-separated domain names")
	caCommonName := flag.String("ca-cn", "Local Dev CA", "CA common name")
	caOrg := flag.String("ca-org", "Local Dev", "CA organization")
	serverOrg := flag.String("server-org", "Local Dev Server", "server certificate organization")
	caDays := flag.Int("ca-days", 3650, "CA certificate validity in days")
	serverDays := flag.Int("server-days", 825, "server certificate validity in days")
	flag.Parse()

	domains, err := parseDomains(*domainsArg)
	if err != nil {
		fatal(err)
	}
	if err := validatePositiveDays(*caDays, *serverDays); err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		fatal(err)
	}

	files := buildCertFiles(*outputDir, domains[0])
	if err := backupExistingFiles(*outputDir, files); err != nil {
		fatal(err)
	}

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		fatal(err)
	}
	caTemplate, err := newCATemplate(*caCommonName, *caOrg, *caDays)
	if err != nil {
		fatal(err)
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		fatal(err)
	}

	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		fatal(err)
	}
	serverTemplate, err := newServerTemplate(domains, *serverOrg, *serverDays)
	if err != nil {
		fatal(err)
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		fatal(err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   domains[0],
			Organization: []string{*serverOrg},
		},
		DNSNames:    extractDNSNames(domains),
		IPAddresses: extractIPAddresses(domains),
	}, serverKey)
	if err != nil {
		fatal(err)
	}

	if err := writePrivateKey(files.CAKey, caKey); err != nil {
		fatal(err)
	}
	if err := writeCertificate(files.CACert, caDER); err != nil {
		fatal(err)
	}
	if err := writePrivateKey(files.ServerKey, serverKey); err != nil {
		fatal(err)
	}
	if err := writeCSR(files.ServerCSR, csrDER); err != nil {
		fatal(err)
	}
	if err := writeCertificate(files.ServerCert, serverDER); err != nil {
		fatal(err)
	}
	if err := verifyCertificate(serverDER, caCert, domains); err != nil {
		fatal(err)
	}

	fmt.Printf("certificate files are ready in: %s\n", *outputDir)
	fmt.Printf("- CA cert:      %s\n", files.CACert)
	fmt.Printf("- Server cert:  %s\n", files.ServerCert)
	fmt.Printf("- Server key:   %s\n", files.ServerKey)
	fmt.Printf("- Domains:      %s\n", strings.Join(domains, ", "))
}

func parseDomains(input string) ([]string, error) {
	parts := strings.Split(input, ",")
	seen := make(map[string]struct{}, len(parts))
	domains := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		if ip := net.ParseIP(value); ip == nil {
			if strings.ContainsAny(value, " /\\") || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
				return nil, fmt.Errorf("invalid domain: %s", value)
			}
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		domains = append(domains, value)
	}
	if len(domains) == 0 {
		return nil, errors.New("at least one domain is required")
	}
	return domains, nil
}

func validatePositiveDays(caDays, serverDays int) error {
	if caDays <= 0 {
		return errors.New("ca-days must be greater than 0")
	}
	if serverDays <= 0 {
		return errors.New("server-days must be greater than 0")
	}
	return nil
}

func buildCertFiles(outputDir, primaryName string) certFiles {
	return certFiles{
		CAKey:      filepath.Join(outputDir, defaultCAName+".key"),
		CACert:     filepath.Join(outputDir, defaultCAName+".crt"),
		ServerKey:  filepath.Join(outputDir, primaryName+".key"),
		ServerCSR:  filepath.Join(outputDir, primaryName+".csr"),
		ServerCert: filepath.Join(outputDir, primaryName+".crt"),
	}
}

func backupExistingFiles(outputDir string, files certFiles) error {
	paths := []string{files.CAKey, files.CACert, files.ServerKey, files.ServerCSR, files.ServerCert}
	existing := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			existing = append(existing, path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if len(existing) == 0 {
		return nil
	}

	backupDir := filepath.Join(outputDir, "backup-"+time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return err
	}
	for _, path := range existing {
		if err := os.Rename(path, filepath.Join(backupDir, filepath.Base(path))); err != nil {
			return err
		}
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

func writeCSR(path string, der []byte) error {
	block := &pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}
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

func verifyCertificate(serverDER []byte, caCert *x509.Certificate, domains []string) error {
	serverCert, err := x509.ParseCertificate(serverDER)
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	for _, domain := range domains {
		if ip := net.ParseIP(domain); ip != nil {
			if err := serverCert.VerifyHostname(ip.String()); err != nil {
				return err
			}
			continue
		}
		if err := serverCert.VerifyHostname(domain); err != nil {
			return err
		}
	}
	_, err = serverCert.Verify(x509.VerifyOptions{
		Roots:       pool,
		CurrentTime: time.Now(),
	})
	return err
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
