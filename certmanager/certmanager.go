package certmanager

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	DefaultRootCAName   = "goto-openai-root"
	DefaultDomainPrefix = "goto-openai-dm"
)

func GenerateRootCA(caCN, caOrg string, days int, outputDir string) error {
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

	caCertPath := filepath.Join(outputDir, DefaultRootCAName+".crt")
	caKeyPath := filepath.Join(outputDir, DefaultRootCAName+".key")

	if err := writeCertificate(caCertPath, caDER); err != nil {
		return fmt.Errorf("write CA certificate: %w", err)
	}

	if err := writePrivateKey(caKeyPath, caKey); err != nil {
		return fmt.Errorf("write CA private key: %w", err)
	}

	return nil
}

func GenerateDomainCert(domain, rootCACertPath, rootCAKeyPath string, days int, outputDir string) error {
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

	certFileName := DefaultDomainPrefix + "-" + domain + ".crt"
	keyFileName := DefaultDomainPrefix + "-" + domain + ".key"

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
