# hosts-setup 和 hosts-remove 功能实现计划

## 功能概述

为项目增加 `hosts-setup` 和 `hosts-remove` 子命令，用于自动管理 hosts 文件中的域名映射。添加的条目会带有注释标记，方便后续识别和移除。

## 实现步骤

### 1. 创建 `hostsmanager/hostsmanager.go`

新建 `hostsmanager` 包，包含以下功能：

* **常量定义**：

  * `HostsMarker = "# go-to-openai"` — 用于标识本工具添加的条目

  * 支持 Windows、Linux 和 macOS 平台（Windows: `C:\Windows\System32\drivers\etc\hosts`，Linux: `/etc/hosts`，macOS: `/etc/hosts`）

* **`Setup(domains []string, targetIP string) error`**：

  * 读取当前 hosts 文件

  * 检查是否已存在本工具添加的条目（通过 marker 识别），如有则先移除

  * 在文件末尾追加新条目，每行格式：`<IP> <domain> # go-to-openai`

  * 需要管理员/root 权限

* **`Remove() error`**：

  * 读取 hosts 文件

  * 移除所有包含 `# go-to-openai` 标记的行

  * 写回文件

* **辅助函数**：

  * `getHostsFilePath() string` — 根据 runtime.GOOS 返回 hosts 文件路径

  * `readHostsFile() ([]byte, error)` — 读取 hosts 文件

  * `writeHostsFile(content []byte) error` — 写入 hosts 文件

### 2. 修改 `main.go`

* 在 `printUsage()` 中添加新命令说明

* 在 `main()` 的 switch 中添加 `hosts-setup` 和 `hosts-remove` 分支

* 新增 `handleHostsSetup(args []string)` 函数：

  * 解析参数：`-ip`（目标 IP，默认 `127.0.0.1`）、`-domain`（可多次指定，或通过位置参数）

  * 调用 `hostsmanager.Setup()`

* 新增 `handleHostsRemove(args []string)` 函数：

  * 调用 `hostsmanager.Remove()`

### 3. 命令行接口设计

```bash
# 添加 hosts 条目
go-to-openai hosts-setup api.openai.com api.anthropic.com
go-to-openai hosts-setup -ip 127.0.0.1 api.openai.com

# 移除所有本工具添加的 hosts 条目
go-to-openai hosts-remove
```

### 4. hosts 条目格式

```
127.0.0.1 api.openai.com # go-to-openai
127.0.0.1 api.anthropic.com # go-to-openai
```

每行末尾添加 `# go-to-openai` 注释，移除时通过匹配该注释识别并删除对应行。

## 文件变更清单

| 文件                             | 操作 | 说明              |
| ------------------------------ | -- | --------------- |
| `hostsmanager/hostsmanager.go` | 新建 | hosts 文件管理逻辑    |
| `main.go`                      | 修改 | 添加命令解析和 handler |

## 注意事项

* 修改 hosts 文件需要管理员权限，错误提示中需说明

* 遵循项目现有错误处理规范（`%w` 包装错误）

* 遵循代码风格：无全局可变状态、类型安全

