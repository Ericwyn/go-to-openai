package auto

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

func CheckRootPrivilege() error {
	switch runtime.GOOS {
	case "windows":
		return checkWindowsAdmin()
	case "linux", "darwin":
		return checkUnixRoot()
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}

func checkWindowsAdmin() error {
	cmd := exec.Command("net", "session")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("需要管理员权限运行，请以管理员身份重新启动程序")
	}
	return nil
}

func checkUnixRoot() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("需要 root 权限运行，请使用 sudo 重新启动程序")
	}
	return nil
}
