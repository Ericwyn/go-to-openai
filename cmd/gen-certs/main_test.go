package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

func TestParseDomains(t *testing.T) {
	t.Parallel()

	domains, err := parseDomains(" api.openai.com, foo.local,127.0.0.1,api.openai.com ")
	if err != nil {
		t.Fatalf("parse domains: %v", err)
	}
	if len(domains) != 3 {
		t.Fatalf("domains len = %d", len(domains))
	}
	if domains[0] != "api.openai.com" || domains[1] != "foo.local" || domains[2] != "127.0.0.1" {
		t.Fatalf("domains = %#v", domains)
	}
}

func TestParseDomainsRequiresValue(t *testing.T) {
	t.Parallel()

	if _, err := parseDomains(" , "); err == nil {
		t.Fatal("expected error")
	}
}

func TestBackupExistingFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	files := buildCertFiles(dir, "api.openai.com")
	for _, path := range []string{files.CAKey, files.CACert, files.ServerKey, files.ServerCSR, files.ServerCert} {
		if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}

	if err := backupExistingFiles(dir, files); err != nil {
		t.Fatalf("backup files: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries len = %d", len(entries))
	}
	backupDir := filepath.Join(dir, entries[0].Name())
	for _, name := range []string{"openaica.key", "openaica.crt", "api.openai.com.key", "api.openai.com.csr", "api.openai.com.crt"} {
		if _, err := os.Stat(filepath.Join(backupDir, name)); err != nil {
			t.Fatalf("stat backup file %s: %v", name, err)
		}
	}
}

func TestGeneratedCertificateSupportsMultipleNames(t *testing.T) {
	t.Parallel()

	caTemplate, err := newCATemplate("Local Dev CA", "Local Dev", 3650)
	if err != nil {
		t.Fatalf("new ca template: %v", err)
	}
	serverTemplate, err := newServerTemplate([]string{"api.openai.com", "foo.local", "127.0.0.1"}, "Local Dev Server", 825)
	if err != nil {
		t.Fatalf("new server template: %v", err)
	}
	if serverTemplate.Subject.CommonName != "api.openai.com" {
		t.Fatalf("common name = %s", serverTemplate.Subject.CommonName)
	}
	if len(serverTemplate.DNSNames) != 2 {
		t.Fatalf("dns names len = %d", len(serverTemplate.DNSNames))
	}
	if len(serverTemplate.IPAddresses) != 1 {
		t.Fatalf("ip addresses len = %d", len(serverTemplate.IPAddresses))
	}
	if !caTemplate.IsCA {
		t.Fatal("expected ca template to be CA")
	}
}

func TestVerifyCertificate(t *testing.T) {
	t.Parallel()

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate ca key: %v", err)
	}
	caTemplate, err := newCATemplate("Local Dev CA", "Local Dev", 3650)
	if err != nil {
		t.Fatalf("new ca template: %v", err)
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create ca cert: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse ca cert: %v", err)
	}

	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	serverTemplate, err := newServerTemplate([]string{"api.openai.com", "foo.local", "127.0.0.1"}, "Local Dev Server", 825)
	if err != nil {
		t.Fatalf("new server template: %v", err)
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create server cert: %v", err)
	}

	if err := verifyCertificate(serverDER, caCert, []string{"api.openai.com", "foo.local", "127.0.0.1"}); err != nil {
		t.Fatalf("verify certificate: %v", err)
	}
}
