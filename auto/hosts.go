package auto

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/ericwyn/go-to-openai/hostsmanager"
)

func SetupHostsInteractive() error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("请输入需要配置 hosts 的域名（多个域名用逗号或空格分隔）: ")
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	if input == "" {
		return fmt.Errorf("域名不能为空")
	}

	domains := parseDomains(input)
	if len(domains) == 0 {
		return fmt.Errorf("未解析到有效的域名")
	}

	if err := hostsmanager.Setup(domains, "127.0.0.1"); err != nil {
		return fmt.Errorf("配置 hosts 失败: %w", err)
	}

	fmt.Println("hosts 配置成功:")
	for _, domain := range domains {
		fmt.Printf("  %s -> 127.0.0.1\n", domain)
	}

	return nil
}

func RemoveHostsInteractive() error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("确定要删除所有 go-to-openai 的 hosts 配置吗？(y/N): ")
	confirm, _ := reader.ReadString('\n')
	confirm = strings.TrimSpace(strings.ToLower(confirm))

	if confirm != "y" && confirm != "yes" {
		fmt.Println("已取消操作")
		return nil
	}

	if err := hostsmanager.Remove(); err != nil {
		return fmt.Errorf("删除 hosts 配置失败: %w", err)
	}

	fmt.Println("go-to-openai hosts 配置已删除")

	return nil
}

func HostsConfigured() bool {
	content, err := hostsmanager.ReadHostsContent()
	if err != nil {
		return false
	}

	return strings.Contains(content, hostsmanager.HostsMarker)
}

func GetHostsDomains() []string {
	content, err := hostsmanager.ReadHostsContent()
	if err != nil {
		return nil
	}

	var domains []string
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		if strings.Contains(line, hostsmanager.HostsMarker) {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				domains = append(domains, parts[1])
			}
		}
	}

	return domains
}
