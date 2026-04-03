package auto

import (
	"fmt"
	"path/filepath"

	"github.com/ericwyn/go-to-openai/certmanager"
	"github.com/ericwyn/go-to-openai/hostsmanager"
	"github.com/ericwyn/go-to-openai/proxy"
)

func OneKeyStart() error {
	if err := CheckRootPrivilege(); err != nil {
		return fmt.Errorf("权限检查失败: %w", err)
	}

	fmt.Println("=== 一键启动 ===")
	fmt.Println()

	fmt.Println("[1/5] 读取配置文件...")
	cfg, err := LoadAutoConfig()
	if err != nil {
		return fmt.Errorf("读取配置失败: %w\n请先使用选项 5 生成配置文件", err)
	}
	fmt.Println("  配置文件加载成功")

	fmt.Println("[2/5] 检查/同步根证书到系统...")
	if err := SyncRootCertToSystem(); err != nil {
		return fmt.Errorf("同步根证书失败: %w", err)
	}
	fmt.Println("  根证书同步完成")

	fmt.Println("[3/5] 生成域名证书...")
	for _, upstream := range cfg.Upstreams {
		domain := upstream.Host
		if DomainCertExists(domain) {
			fmt.Printf("  域名 %s 证书已存在，重新生成以确保与最新根证书匹配...\n", domain)
		} else {
			fmt.Printf("  正在为 %s 生成证书...\n", domain)
		}

		outputDir := filepath.Clean(DefaultCertDir)
		rootCACert := filepath.Join(outputDir, certmanager.DefaultRootCAName+".crt")
		rootCAKey := filepath.Join(outputDir, certmanager.DefaultRootCAName+".key")

		if err := certmanager.GenerateDomainCert(domain, rootCACert, rootCAKey, 825, outputDir); err != nil {
			return fmt.Errorf("为 %s 生成证书失败: %w", domain, err)
		}
		fmt.Printf("  %s 证书生成成功\n", domain)
	}

	fmt.Println("[4/5] 配置 hosts...")
	var domains []string
	for _, upstream := range cfg.Upstreams {
		domains = append(domains, upstream.Host)
	}

	if err := hostsmanager.Setup(domains, "127.0.0.1"); err != nil {
		return fmt.Errorf("配置 hosts 失败: %w", err)
	}
	fmt.Printf("  hosts 配置成功: %s\n", domains)

	fmt.Println("[5/5] 启动 HTTPS 代理服务器...")
	fmt.Println()

	if err := proxy.Run(cfg, false); err != nil {
		return fmt.Errorf("启动代理服务器失败: %w", err)
	}

	return nil
}
