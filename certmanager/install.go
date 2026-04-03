package certmanager

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func Install(certPath string) error {
	if _, err := os.Stat(certPath); err != nil {
		return fmt.Errorf("certificate file not found: %s", certPath)
	}

	switch runtime.GOOS {
	case "linux":
		return installRootCALinux(certPath)
	case "darwin":
		return installRootCAMacOS(certPath)
	case "windows":
		return installRootCAWindows(certPath)
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}

func installRootCALinux(certPath string) error {
	linuxDistro := detectLinuxDistro()

	var destDir string
	var updateCmd string

	switch linuxDistro {
	case "debian", "ubuntu":
		destDir = "/usr/local/share/ca-certificates"
		updateCmd = "update-ca-certificates"
	case "rhel", "centos", "fedora", "amzn":
		destDir = "/etc/pki/ca-trust/source/anchors"
		updateCmd = "update-ca-trust"
	default:
		destDir = "/usr/local/share/ca-certificates"
		updateCmd = "update-ca-certificates"
		fmt.Printf("detected Linux distribution: %s (using Debian/Ubuntu method)\n", linuxDistro)
	}

	destFile := filepath.Join(destDir, filepath.Base(certPath))

	copyCmd := exec.Command("cp", certPath, destFile)
	copyCmd.Stdout = os.Stdout
	copyCmd.Stderr = os.Stderr
	if err := copyCmd.Run(); err != nil {
		return fmt.Errorf("copy certificate: %w (try running with sudo)", err)
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

func detectLinuxDistro() string {
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		content := string(data)
		if strings.Contains(content, "ubuntu") {
			return "ubuntu"
		}
		if strings.Contains(content, "debian") {
			return "debian"
		}
		if strings.Contains(content, "rhel") {
			return "rhel"
		}
		if strings.Contains(content, "centos") {
			return "centos"
		}
		if strings.Contains(content, "fedora") {
			return "fedora"
		}
		if strings.Contains(content, "amzn") {
			return "amzn"
		}
	}

	if _, err := exec.LookPath("lsb_release"); err == nil {
		cmd := exec.Command("lsb_release", "-si")
		if output, err := cmd.Output(); err == nil {
			return strings.ToLower(strings.TrimSpace(string(output)))
		}
	}

	return "unknown"
}

func installRootCAMacOS(certPath string) error {
	cmd := exec.Command("sudo", "security", "add-trusted-cert", "-d", "-r", "trustRoot",
		"-k", "/Library/Keychains/System.keychain", certPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("install certificate: %w", err)
	}
	return nil
}

func installRootCAWindows(certPath string) error {
	absPath, err := filepath.Abs(certPath)
	if err != nil {
		return fmt.Errorf("get absolute path: %w", err)
	}

	cmd := exec.Command("certutil", "-addstore", "Root", absPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("install certificate: %w (try running as Administrator)", err)
	}
	return nil
}
