package auto

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ericwyn/go-to-openai/certmanager"
	"github.com/ericwyn/go-to-openai/config"
)

func GenerateConfigFileInteractive() error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("请输入代理域名 (例如 api.openai.com): ")
	domain, _ := reader.ReadString('\n')
	domain = strings.TrimSpace(domain)

	if domain == "" {
		return fmt.Errorf("域名不能为空")
	}

	if err := GenerateConfigFile(domain); err != nil {
		return err
	}

	fmt.Printf("配置文件已生成: %s\n", DefaultAutoConfig)

	return nil
}

func GenerateConfigFile(domain string) error {
	certFile := filepath.Join(DefaultCertDir, certmanager.DefaultDomainPrefix+"-"+domain+".crt")
	keyFile := filepath.Join(DefaultCertDir, certmanager.DefaultDomainPrefix+"-"+domain+".key")

	cfg := config.Config{
		ListenAddr: ":443",
		CertFile:   certFile,
		KeyFile:    keyFile,
		RetryMax:   3,
		Upstreams: []config.UpstreamConfig{
			{
				Host:    domain,
				BaseURL: "http://" + domain,
				Routes: []config.RouteConfig{
					{Path: "/v1/chat/completions", TargetPath: "/v1/chat/completions"},
					{Path: "/v1/models", TargetPath: "/v1/models"},
					{Path: "/v1/responses", TargetPath: "/openai/responses"},
				},
			},
		},
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}

	if err := os.WriteFile(DefaultAutoConfig, data, 0o644); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}

	return nil
}

func ConfigFileExists() bool {
	_, err := os.Stat(DefaultAutoConfig)
	return err == nil
}

func LoadAutoConfig() (config.Config, error) {
	if !ConfigFileExists() {
		return config.Config{}, fmt.Errorf("配置文件 %s 不存在，请先生成配置", DefaultAutoConfig)
	}

	cfg, err := config.Load(DefaultAutoConfig)
	if err != nil {
		return config.Config{}, fmt.Errorf("加载配置文件失败: %w", err)
	}

	return cfg, nil
}
