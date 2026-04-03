package certmanager

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func CompareRootCerts(certPath1, certPath2 string) (bool, error) {
	fingerprint1, err := GetCertFingerprint(certPath1)
	if err != nil {
		return false, fmt.Errorf("get fingerprint of %s: %w", certPath1, err)
	}

	fingerprint2, err := GetCertFingerprint(certPath2)
	if err != nil {
		return false, fmt.Errorf("get fingerprint of %s: %w", certPath2, err)
	}

	return fingerprint1 == fingerprint2, nil
}

func GetCertFingerprint(certPath string) (string, error) {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return "", fmt.Errorf("read certificate: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return "", fmt.Errorf("failed to decode PEM block")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse certificate: %w", err)
	}

	hash := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(hash[:]), nil
}

func IsRootCAInstalled(certPath string) (bool, error) {
	if _, err := os.Stat(certPath); err != nil {
		return false, fmt.Errorf("certificate file not found: %s", certPath)
	}

	switch runtime.GOOS {
	case "windows":
		return isRootCAInstalledWindows(certPath)
	case "darwin":
		return isRootCAInstalledMacOS(certPath)
	case "linux":
		return isRootCAInstalledLinux(certPath)
	default:
		return false, fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}

func isRootCAInstalledWindows(certPath string) (bool, error) {
	certData, err := os.ReadFile(certPath)
	if err != nil {
		return false, fmt.Errorf("read certificate: %w", err)
	}

	cert, err := parseCertFromPEM(certData)
	if err != nil {
		return false, err
	}

	certHash := sha256.Sum256(cert.Raw)
	certHashHex := hex.EncodeToString(certHash[:])

	cmd := exec.Command("powershell", "-Command",
		"Get-ChildItem Cert:\\LocalMachine\\Root | ForEach-Object { $_.Thumbprint }")
	output, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("list system root CAs: %w", err)
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(strings.ToLower(line))
		if line == "" {
			continue
		}
		if line == certHashHex {
			return true, nil
		}
	}

	return false, nil
}

func isRootCAInstalledMacOS(certPath string) (bool, error) {
	certData, err := os.ReadFile(certPath)
	if err != nil {
		return false, fmt.Errorf("read certificate: %w", err)
	}

	cert, err := parseCertFromPEM(certData)
	if err != nil {
		return false, err
	}

	certHash := sha256.Sum256(cert.Raw)
	certHashHex := hex.EncodeToString(certHash[:])

	cmd := exec.Command("security", "find-certificate", "-a", "-p", "/Library/Keychains/System.keychain")
	output, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("list system root CAs: %w", err)
	}

	return containsCertWithHash(output, certHashHex), nil
}

func isRootCAInstalledLinux(certPath string) (bool, error) {
	certData, err := os.ReadFile(certPath)
	if err != nil {
		return false, fmt.Errorf("read certificate: %w", err)
	}

	cert, err := parseCertFromPEM(certData)
	if err != nil {
		return false, err
	}

	certHash := sha256.Sum256(cert.Raw)
	certHashHex := hex.EncodeToString(certHash[:])

	searchPaths := []string{
		"/etc/ssl/certs",
		"/usr/local/share/ca-certificates",
		"/usr/share/ca-certificates",
		"/etc/pki/ca-trust/source/anchors",
		"/etc/pki/tls/certs",
	}

	var parseErrors []error

	for _, dir := range searchPaths {
		if _, err := os.Stat(dir); err != nil {
			continue
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if !strings.HasSuffix(strings.ToLower(entry.Name()), ".crt") &&
				!strings.HasSuffix(strings.ToLower(entry.Name()), ".pem") {
				continue
			}

			fullPath := dir + string(os.PathSeparator) + entry.Name()
			entryData, err := os.ReadFile(fullPath)
			if err != nil {
				continue
			}

			if bytes.Contains(entryData, cert.Raw) {
				return true, nil
			}

			entryCert, err := parseCertFromPEM(entryData)
			if err != nil {
				parseErrors = append(parseErrors, fmt.Errorf("parse %s: %w", fullPath, err))
				continue
			}

			entryHash := sha256.Sum256(entryCert.Raw)
			if hex.EncodeToString(entryHash[:]) == certHashHex {
				return true, nil
			}
		}
	}

	if len(parseErrors) > 0 {
		return false, fmt.Errorf("failed to parse %d certificate file(s): %w", len(parseErrors), errors.Join(parseErrors...))
	}

	return false, nil
}

func parseCertFromPEM(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}

	return cert, nil
}

func containsCertWithHash(pemData []byte, targetHash string) bool {
	for len(pemData) > 0 {
		block, rest := pem.Decode(pemData)
		if block == nil {
			break
		}

		cert, err := x509.ParseCertificate(block.Bytes)
		if err == nil {
			hash := sha256.Sum256(cert.Raw)
			if hex.EncodeToString(hash[:]) == targetHash {
				return true
			}
		}

		pemData = rest
	}

	return false
}
