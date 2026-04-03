package certmanager

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func Remove(certPath string) error {
	if _, err := os.Stat(certPath); err != nil {
		return fmt.Errorf("certificate file not found: %s", certPath)
	}

	switch runtime.GOOS {
	case "linux":
		return removeRootCALinux(certPath)
	case "darwin":
		return removeRootCAMacOS(certPath)
	case "windows":
		return removeRootCAWindows(certPath)
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}

func removeRootCALinux(certPath string) error {
	linuxDistro := detectLinuxDistro()

	var updateCmd string
	var searchDirs []string

	switch linuxDistro {
	case "debian", "ubuntu":
		searchDirs = []string{
			"/usr/local/share/ca-certificates",
			"/usr/share/ca-certificates",
			"/etc/ssl/certs",
		}
		updateCmd = "update-ca-certificates"
	case "rhel", "centos", "fedora", "amzn":
		searchDirs = []string{
			"/etc/pki/ca-trust/source/anchors",
			"/etc/pki/ca-trust/extracted/pem",
			"/etc/pki/tls/certs",
		}
		updateCmd = "update-ca-trust"
	default:
		searchDirs = []string{
			"/usr/local/share/ca-certificates",
			"/usr/share/ca-certificates",
			"/etc/ssl/certs",
			"/etc/pki/ca-trust/source/anchors",
		}
		updateCmd = "update-ca-certificates"
		fmt.Printf("detected Linux distribution: %s (checking multiple locations)\n", linuxDistro)
	}

	certFileName := filepath.Base(certPath)
	var removedPaths []string

	for _, dir := range searchDirs {
		destFile := filepath.Join(dir, certFileName)
		if _, err := os.Stat(destFile); err == nil {
			if err := os.Remove(destFile); err != nil {
				fmt.Printf("warning: failed to remove %s: %v\n", destFile, err)
			} else {
				removedPaths = append(removedPaths, destFile)
				fmt.Printf("removed: %s\n", destFile)
			}
		}
	}

	if len(removedPaths) == 0 {
		return fmt.Errorf("installed certificate not found in any of: %v", searchDirs)
	}

	updateCmdParts := strings.Fields(updateCmd)
	updateCmdExec := exec.Command(updateCmdParts[0], updateCmdParts[1:]...)
	updateCmdExec.Stdout = os.Stdout
	updateCmdExec.Stderr = os.Stderr
	if err := updateCmdExec.Run(); err != nil {
		return fmt.Errorf("update certificate store: %w (try running with sudo)", err)
	}

	return nil
}

func removeRootCAMacOS(certPath string) error {
	certData, err := os.ReadFile(certPath)
	if err != nil {
		return fmt.Errorf("read certificate file: %w", err)
	}

	certPEM := string(certData)
	cn := ""
	for _, line := range strings.Split(certPEM, "\n") {
		if strings.Contains(line, "subject=") || strings.Contains(line, "CN") {
			if idx := strings.Index(line, "CN"); idx != -1 {
				start := idx + 3
				if start < len(line) {
					end := strings.Index(line[start:], ",")
					if end == -1 {
						end = len(line) - start
					}
					cn = strings.TrimSpace(line[start : start+end])
					break
				}
			}
		}
	}

	if cn == "" {
		cn = "Go-To-OpenAI Root CA"
	}

	cmd := exec.Command("sudo", "security", "delete-certificate", "-c", cn,
		"/Library/Keychains/System.keychain")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("delete certificate: %w", err)
	}

	return nil
}

func removeRootCAWindows(certPath string) error {
	err := removeRootCAWindowsPowerShell(certPath)
	if err == nil {
		return nil
	}

	fmt.Printf("PowerShell method failed: %v\n", err)
	fmt.Println("Trying certutil method...")

	return removeRootCAWindowsCertUtil(certPath)
}

func removeRootCAWindowsPowerShell(certPath string) error {
	certData, err := os.ReadFile(certPath)
	if err != nil {
		return fmt.Errorf("read certificate file: %w", err)
	}

	certPEM := string(certData)
	cn := ""
	for _, line := range strings.Split(certPEM, "\n") {
		if strings.Contains(line, "subject=") || strings.Contains(line, "CN") {
			if idx := strings.Index(line, "CN"); idx != -1 {
				start := idx + 3
				if start < len(line) {
					end := strings.Index(line[start:], ",")
					if end == -1 {
						end = len(line) - start
					}
					cn = strings.TrimSpace(line[start : start+end])
					break
				}
			}
		}
	}

	if cn == "" {
		cn = "Go-To-OpenAI Root CA"
	}

	psCommand := fmt.Sprintf(
		"Get-ChildItem cert:\\LocalMachine\\Root | Where-Object {$_.Subject -like '*%s*'} | Remove-Item -Force",
		cn,
	)

	cmd := exec.Command("powershell", "-Command", psCommand)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("delete certificate via PowerShell: %w (try running as Administrator)", err)
	}

	return nil
}

func removeRootCAWindowsCertUtil(certPath string) error {
	listCmd := exec.Command("certutil", "-store", "Root")
	var listOutput bytes.Buffer
	listCmd.Stdout = &listOutput
	listCmd.Stderr = os.Stderr
	if err := listCmd.Run(); err != nil {
		return fmt.Errorf("list certificates: %w", err)
	}

	output := listOutput.String()
	lines := strings.Split(output, "\n")

	var serialNumber string
	foundTarget := false

	for i, line := range lines {
		if strings.Contains(line, "Go-To-OpenAI Root CA") || strings.Contains(line, "goto-openai-root") {
			foundTarget = true
			for j := i; j >= 0; j-- {
				if strings.Contains(lines[j], "Serial Number:") {
					parts := strings.SplitN(lines[j], ":", 2)
					if len(parts) == 2 {
						serialNumber = strings.TrimSpace(parts[1])
					}
					break
				}
			}
			break
		}
	}

	if !foundTarget || serialNumber == "" {
		return fmt.Errorf("certificate not found in Root store")
	}

	delCmd := exec.Command("certutil", "-delstore", "Root", serialNumber)
	delCmd.Stdout = os.Stdout
	delCmd.Stderr = os.Stderr
	if err := delCmd.Run(); err != nil {
		return fmt.Errorf("delete certificate: %w (try running as Administrator)", err)
	}

	return nil
}
