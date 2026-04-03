package auto

import (
	"bufio"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ericwyn/go-to-openai/certmanager"
)

func GenerateDomainCertInteractive() error {
	if !RootCertExists() {
		return fmt.Errorf("根证书不存在，请先生成根证书")
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("请输入域名（多个域名用逗号或空格分隔）: ")
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	if input == "" {
		return fmt.Errorf("域名不能为空")
	}

	domains := parseDomains(input)
	if len(domains) == 0 {
		return fmt.Errorf("未解析到有效的域名")
	}

	outputDir := filepath.Clean(DefaultCertDir)
	rootCACert := filepath.Join(outputDir, certmanager.DefaultRootCAName+".crt")
	rootCAKey := filepath.Join(outputDir, certmanager.DefaultRootCAName+".key")

	for _, domain := range domains {
		fmt.Printf("正在为 %s 生成证书...\n", domain)

		if err := certmanager.GenerateDomainCert(domain, rootCACert, rootCAKey, 825, outputDir); err != nil {
			return fmt.Errorf("为 %s 生成证书失败: %w", domain, err)
		}

		certPath := filepath.Join(outputDir, certmanager.DefaultDomainPrefix+"-"+domain+".crt")
		keyPath := filepath.Join(outputDir, certmanager.DefaultDomainPrefix+"-"+domain+".key")

		fmt.Printf("  证书: %s\n", certPath)
		fmt.Printf("  私钥: %s\n", keyPath)
	}

	fmt.Printf("成功为 %d 个域名生成证书\n", len(domains))

	return nil
}

func GenerateDomainCertForDomain(domain string) error {
	if !RootCertExists() {
		return fmt.Errorf("根证书不存在，请先生成根证书")
	}

	outputDir := filepath.Clean(DefaultCertDir)
	rootCACert := filepath.Join(outputDir, certmanager.DefaultRootCAName+".crt")
	rootCAKey := filepath.Join(outputDir, certmanager.DefaultRootCAName+".key")

	if err := certmanager.GenerateDomainCert(domain, rootCACert, rootCAKey, 825, outputDir); err != nil {
		return fmt.Errorf("为 %s 生成证书失败: %w", domain, err)
	}

	return nil
}

func DomainCertExists(domain string) bool {
	certPath := filepath.Join(DefaultCertDir, certmanager.DefaultDomainPrefix+"-"+domain+".crt")
	keyPath := filepath.Join(DefaultCertDir, certmanager.DefaultDomainPrefix+"-"+domain+".key")

	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)

	return certErr == nil && keyErr == nil
}

func VerifyDomainCertSignedByRoot(domain string) (bool, error) {
	domainCertPath := filepath.Join(DefaultCertDir, certmanager.DefaultDomainPrefix+"-"+domain+".crt")
	rootCertPath := filepath.Join(DefaultCertDir, certmanager.DefaultRootCAName+".crt")

	domainData, err := os.ReadFile(domainCertPath)
	if err != nil {
		return false, fmt.Errorf("读取域名证书失败: %w", err)
	}

	rootData, err := os.ReadFile(rootCertPath)
	if err != nil {
		return false, fmt.Errorf("读取根证书失败: %w", err)
	}

	domainBlock, _ := pem.Decode(domainData)
	if domainBlock == nil {
		return false, fmt.Errorf("解析域名证书 PEM 失败")
	}

	rootBlock, _ := pem.Decode(rootData)
	if rootBlock == nil {
		return false, fmt.Errorf("解析根证书 PEM 失败")
	}

	domainCert, err := x509.ParseCertificate(domainBlock.Bytes)
	if err != nil {
		return false, fmt.Errorf("解析域名证书失败: %w", err)
	}

	rootCert, err := x509.ParseCertificate(rootBlock.Bytes)
	if err != nil {
		return false, fmt.Errorf("解析根证书失败: %w", err)
	}

	roots := x509.NewCertPool()
	roots.AddCert(rootCert)

	opts := x509.VerifyOptions{
		Roots: roots,
	}

	_, err = domainCert.Verify(opts)
	return err == nil, nil
}

func parseDomains(input string) []string {
	input = strings.ReplaceAll(input, ",", " ")
	parts := strings.Fields(input)

	var domains []string
	seen := make(map[string]bool)

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" && !seen[part] {
			domains = append(domains, part)
			seen[part] = true
		}
	}

	return domains
}
