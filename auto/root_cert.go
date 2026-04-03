package auto

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ericwyn/go-to-openai/certmanager"
)

const (
	DefaultCertDir    = "./cert"
	DefaultAutoConfig = "./auto.config.json"
)

func GenerateRootCert() error {
	fmt.Println("正在生成根证书...")

	outputDir := filepath.Clean(DefaultCertDir)

	if err := certmanager.GenerateRootCA("Go-To-OpenAI Root CA", "Go-To-OpenAI", 3650, outputDir); err != nil {
		return fmt.Errorf("生成根证书失败: %w", err)
	}

	certPath := filepath.Join(outputDir, certmanager.DefaultRootCAName+".crt")
	keyPath := filepath.Join(outputDir, certmanager.DefaultRootCAName+".key")

	fmt.Printf("根证书生成成功:\n")
	fmt.Printf("  证书: %s\n", certPath)
	fmt.Printf("  私钥: %s\n", keyPath)

	return nil
}

func InstallRootCert() error {
	certPath := filepath.Join(DefaultCertDir, certmanager.DefaultRootCAName+".crt")

	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		return fmt.Errorf("根证书不存在，请先生成根证书")
	}

	fmt.Println("正在安装根证书到系统信任存储...")

	if err := certmanager.Install(certPath); err != nil {
		return fmt.Errorf("安装根证书失败: %w", err)
	}

	fmt.Printf("根证书安装成功: %s\n", certPath)

	return nil
}

func RemoveRootCert() error {
	certPath := filepath.Join(DefaultCertDir, certmanager.DefaultRootCAName+".crt")

	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		return fmt.Errorf("根证书不存在")
	}

	fmt.Println("正在从系统信任存储卸载根证书...")

	if err := certmanager.Remove(certPath); err != nil {
		return fmt.Errorf("卸载根证书失败: %w", err)
	}

	fmt.Printf("根证书卸载成功: %s\n", certPath)

	return nil
}

func RootCertExists() bool {
	certPath := filepath.Join(DefaultCertDir, certmanager.DefaultRootCAName+".crt")
	keyPath := filepath.Join(DefaultCertDir, certmanager.DefaultRootCAName+".key")

	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)

	return certErr == nil && keyErr == nil
}
