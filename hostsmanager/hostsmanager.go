package hostsmanager

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

const (
	HostsMarker = "# go-to-openai"
)

func getHostsFilePath() string {
	switch runtime.GOOS {
	case "windows":
		return `C:\Windows\System32\drivers\etc\hosts`
	case "linux", "darwin":
		return "/etc/hosts"
	default:
		return ""
	}
}

func readHostsFile() ([]byte, error) {
	path := getHostsFilePath()
	if path == "" {
		return nil, fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read hosts file: %w", err)
	}

	return data, nil
}

func writeHostsFile(content []byte) error {
	path := getHostsFilePath()
	if path == "" {
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	if err := os.WriteFile(path, content, 0o600); err != nil {
		return fmt.Errorf("write hosts file: %w (try running as Administrator/root)", err)
	}

	return nil
}

func Setup(domains []string, targetIP string) error {
	if len(domains) == 0 {
		return fmt.Errorf("no domains specified")
	}

	if targetIP == "" {
		return fmt.Errorf("target IP is empty")
	}

	content, err := readHostsFile()
	if err != nil {
		return err
	}

	lines := strings.Split(string(content), "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if !strings.Contains(line, HostsMarker) {
			filtered = append(filtered, line)
		}
	}

	for _, domain := range domains {
		entry := fmt.Sprintf("%s %s %s", targetIP, domain, HostsMarker)
		filtered = append(filtered, entry)
	}

	newContent := strings.Join(filtered, "\n")
	if !strings.HasSuffix(newContent, "\n") {
		newContent += "\n"
	}

	return writeHostsFile([]byte(newContent))
}

func Remove() error {
	content, err := readHostsFile()
	if err != nil {
		return err
	}

	lines := strings.Split(string(content), "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if !strings.Contains(line, HostsMarker) {
			filtered = append(filtered, line)
		}
	}

	newContent := strings.Join(filtered, "\n")
	newContent = strings.TrimRight(newContent, "\n") + "\n"

	return writeHostsFile([]byte(newContent))
}
