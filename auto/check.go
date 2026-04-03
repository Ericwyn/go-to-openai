package auto

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ericwyn/go-to-openai/certmanager"
)

func CheckConfiguration() error {
	fmt.Println("=== 配置检查报告 ===")
	fmt.Println()

	checkRootCert()
	checkConfigFile()
	checkHostsConfig()
	checkHostsMatch()

	fmt.Println()
	fmt.Println("=== 检查完成 ===")

	return nil
}

func checkRootCert() {
	certPath := filepath.Join(DefaultCertDir, certmanager.DefaultRootCAName+".crt")
	keyPath := filepath.Join(DefaultCertDir, certmanager.DefaultRootCAName+".key")

	localCertExists := fileExists(certPath)
	localKeyExists := fileExists(keyPath)

	if localCertExists && localKeyExists {
		fmt.Println("[✓] 根证书 (cert目录): 已存在")
	} else if localCertExists {
		fmt.Println("[!] 根证书 (cert目录): 证书存在但私钥缺失")
	} else {
		fmt.Println("[✗] 根证书 (cert目录): 不存在")
		return
	}

	installed, err := certmanager.IsRootCAInstalled(certPath)
	if err != nil {
		fmt.Printf("[!] 根证书 (系统信任): 检查失败 (%v)\n", err)
		return
	}

	if installed {
		fmt.Println("[✓] 根证书 (系统信任): 已安装")
	} else {
		fmt.Println("[✗] 根证书 (系统信任): 未安装")
	}

	fingerprint, err := certmanager.GetCertFingerprint(certPath)
	if err == nil {
		fmt.Printf("  证书指纹: SHA256:%s\n", fingerprint)
	}
}

func checkConfigFile() {
	if ConfigFileExists() {
		fmt.Println("[✓] 配置文件: 已存在 (" + DefaultAutoConfig + ")")

		cfg, err := LoadAutoConfig()
		if err != nil {
			fmt.Printf("[✗] 配置文件: 加载失败 (%v)\n", err)
			return
		}

		fmt.Printf("  监听地址: %s\n", cfg.ListenAddr)
		fmt.Printf("  证书文件: %s\n", cfg.CertFile)
		fmt.Printf("  私钥文件: %s\n", cfg.KeyFile)
		fmt.Printf("  上游数量: %d\n", len(cfg.Upstreams))

		for _, upstream := range cfg.Upstreams {
			fmt.Printf("    - 域名: %s -> %s\n", upstream.Host, upstream.BaseURL)
		}
	} else {
		fmt.Println("[✗] 配置文件: 不存在 (" + DefaultAutoConfig + ")")
	}
}

func checkHostsConfig() {
	if HostsConfigured() {
		fmt.Println("[✓] Hosts 配置: 已配置")

		domains := GetHostsDomains()
		if len(domains) > 0 {
			fmt.Printf("  已配置域名: %s\n", strings.Join(domains, ", "))
		}
	} else {
		fmt.Println("[✗] Hosts 配置: 未配置")
	}
}

func checkHostsMatch() {
	if !ConfigFileExists() {
		fmt.Println("[✗] Hosts 匹配: 配置文件不存在")
		return
	}

	cfg, err := LoadAutoConfig()
	if err != nil {
		fmt.Printf("[✗] Hosts 匹配: 加载配置文件失败: %v\n", err)
		return
	}

	hostsDomains := GetHostsDomains()
	if len(hostsDomains) == 0 {
		fmt.Println("[✗] Hosts 匹配: hosts 中未配置任何域名")
		return
	}

	configDomains := make(map[string]bool)
	for _, upstream := range cfg.Upstreams {
		configDomains[upstream.Host] = true
	}

	var missing []string
	for _, domain := range hostsDomains {
		if !configDomains[domain] {
			missing = append(missing, domain)
		}
	}

	if len(missing) == 0 {
		fmt.Println("[✓] Hosts 匹配: hosts 配置与配置文件匹配")
	} else {
		fmt.Printf("[✗] Hosts 匹配: 以下域名在 hosts 中但不在配置文件中: %s\n", strings.Join(missing, ", "))
	}

	var notInHosts []string
	for domain := range configDomains {
		found := false
		for _, h := range hostsDomains {
			if h == domain {
				found = true
				break
			}
		}
		if !found {
			notInHosts = append(notInHosts, domain)
		}
	}

	if len(notInHosts) > 0 {
		fmt.Printf("[!] Hosts 匹配: 以下域名在配置文件中但不在 hosts 中: %s\n", strings.Join(notInHosts, ", "))
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
