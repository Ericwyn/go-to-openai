package auto

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func RunAutoMode() error {
	if err := CheckRootPrivilege(); err != nil {
		fmt.Fprintf(os.Stderr, "权限检查失败: %v\n", err)
		os.Exit(1)
	}

	reader := bufio.NewReader(os.Stdin)

	for {
		printMenu()

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		choice, err := strconv.Atoi(input)
		if err != nil {
			fmt.Println("无效输入，请输入数字 0-9")
			fmt.Println()
			continue
		}

		if err := handleChoice(choice); err != nil {
			fmt.Fprintf(os.Stderr, "操作失败: %v\n", err)
		}

		if choice == 0 {
			fmt.Println("退出程序")
			break
		}

		fmt.Println()
	}

	return nil
}

func printMenu() {
	fmt.Println("欢迎使用 goto-openai, 请输入数字进行操作")
	fmt.Println("1. 生成根证书")
	fmt.Println("2. 安装根证书")
	fmt.Println("3. 卸载根证书")
	fmt.Println("4. 生成域名证书")
	fmt.Println("5. 生成配置文件")
	fmt.Println("6. 添加hosts配置")
	fmt.Println("7. 删除hosts")
	fmt.Println("8. 配置检查")
	fmt.Println("9. 一键启动")
	fmt.Println("0. 退出")
	fmt.Print("请选择: ")
}

func handleChoice(choice int) error {
	switch choice {
	case 0:
		return nil
	case 1:
		return GenerateRootCert()
	case 2:
		return InstallRootCert()
	case 3:
		return RemoveRootCert()
	case 4:
		return GenerateDomainCertInteractive()
	case 5:
		return GenerateConfigFileInteractive()
	case 6:
		return SetupHostsInteractive()
	case 7:
		return RemoveHostsInteractive()
	case 8:
		return CheckConfiguration()
	case 9:
		return OneKeyStart()
	default:
		fmt.Println("无效选项，请输入 0-9")
		return nil
	}
}
