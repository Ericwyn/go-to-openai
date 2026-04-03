# 支持 -auto 交互式命令行模式

## 目标

添加 `-auto` 启动模式，提供交互式命令行界面，用户可以通过数字选择不同功能。所有 auto 相关代码放在 `auto` 包中。

## 实现步骤

### 1. 创建 `auto` 包结构

创建 `auto/` 目录，包含以下文件：

```
auto/
├── menu.go          # 交互式菜单主逻辑
├── root_cert.go     # 根证书操作（生成/安装/卸载）
├── domain_cert.go   # 域名证书生成
├── config_gen.go    # 配置文件生成
├── hosts.go         # hosts 配置操作
├── check.go         # 配置检查
├── onestart.go      # 一键启动
└── privilege.go     # 权限检查
```

### 2. 实现权限检查 (`auto/privilege.go`)

* 实现 `CheckRootPrivilege() error` 函数

* Windows: 使用 `net session` 或 `whoami /groups` 检查管理员权限

* Linux/macOS: 检查 UID 是否为 0

* 无权限时返回错误并退出

### 3. 实现交互式菜单 (`auto/menu.go`)

* 实现 `RunAutoMode()` 函数作为 auto 模式入口

* 显示菜单选项（0-9）

* 读取用户输入并分发到对应功能

* 循环直到用户选择退出

* 菜单文本：

  ```
  欢迎使用 goto-openai, 请输出数字进行操作 
  1. 生成根证书 
  2. 安装根证书 
  3. 卸载根证书 
  4. 生成域名证书 
  5. 生成配置文件 
  6. 添加hosts配置 
  7. 删除hosts 
  8. 配置检查 
  9. 一键启动 
  0.退出 
  ```

### 4. 实现根证书操作 (`auto/root_cert.go`)

* `GenerateRootCert()` - 调用 `certmanager.GenerateRootCA`

* `InstallRootCert()` - 调用 `certmanager.Install`

* `RemoveRootCert()` - 调用 `certmanager.Remove`

* 使用默认参数（CA CN、Org、天数、输出目录）

### 5. 实现域名证书生成 (`auto/domain_cert.go`)

* `GenerateDomainCert()` - 提示用户输入一个或多个域名

* 调用 `certmanager.GenerateDomainCert` 为每个域名生成证书

* 支持输入多个域名（逗号分隔或空格分隔）

### 6. 实现配置文件生成 (`auto/config_gen.go`)

* `GenerateConfigFile()` - 提示用户输入域名

* 生成 `auto.config.json` 文件

* 模板包含：

  * 基础的 3 个路径映射（/v1/chat/completions, /v1/models, /v1/responses）

  * 用户配置的域名作为 upstream host

  * 默认的 listen\_addr、证书路径等

### 7. 实现 hosts 操作 (`auto/hosts.go`)

* `SetupHosts()` - 提示用户输入域名，调用 `hostsmanager.Setup`

* `RemoveHosts()` - 调用 `hostsmanager.Remove`，需要用户确认

### 8. 实现配置检查 (`auto/check.go`)

* `CheckConfiguration()` - 检查以下项目：

  * 系统是否有根证书（检查 cert 目录下的根证书文件）

  * 是否有配置文件 `auto.config.json`

  * 是否有 hosts 配置（检查 hosts 文件中是否有 go-to-openai 标记）

  * hosts 配置与 `auto.config.json` 中的域名是否匹配

* 输出检查报告

### 9. 实现一键启动 (`auto/onestart.go`)

* `OneKeyStart()` - 按顺序执行：

  1. 读取 `auto.config.json` 配置（不存在则退出，提示先生成配置）
  2. 检查/生成根证书（已有则跳过）
  3. 基于最新根证书重新生成域名证书
  4. 自动配置 hosts 文件
  5. 启动 HTTPS 服务器（调用 `proxy.Run`）

### 10. 修改 `main.go`

* 添加 `-auto` 命令处理

* 在 switch 中添加 `case "auto", "-auto":` 分支

* 调用 `auto.RunAutoMode()`

### 11. 更新使用帮助

* 在 `printUsage()` 中添加 `auto` 命令说明

## 技术细节

### 默认路径约定

* 根证书：`./cert/goto-openai-root.crt` 和 `./cert/goto-openai-root.key`

* 域名证书：`./cert/goto-openai-dm-{domain}.crt` 和 `./cert/goto-openai-dm-{domain}.key`

* 配置文件：`./auto.config.json`

* 监听地址：`:443`

### 配置文件模板格式

```json
{
  "listen_addr": ":443",
  "tls_cert_file": "./cert/goto-openai-dm-{domain}.crt",
  "tls_key_file": "./cert/goto-openai-dm-{domain}.key",
  "retry_max": 3,
  "upstreams": [
    {
      "host": "{user-domain}",
      "base_url": "http://{user-domain}",
      "routes": [
        {"path": "/v1/chat/completions", "target_path": "/v1/chat/completions"},
        {"path": "/v1/models", "target_path": "/v1/models"},
        {"path": "/v1/responses", "target_path": "/openai/responses"}
      ]
    }
  ]
}
```

### 权限检查实现

* Windows: 执行 `net session` 命令，检查返回码

* Linux/macOS: 检查 `os.Geteuid() == 0`

### 交互式输入处理

* 使用 `bufio.Scanner` 读取标准输入

* 处理无效输入（非数字、超出范围）

* 每个操作完成后返回菜单

## 风险点

1. **权限问题**：hosts 文件修改和证书安装需要管理员权限，必须在操作前检查
2. **并发安全**：一键启动涉及多个步骤，需要确保步骤间的依赖关系正确
3. **错误恢复**：某一步骤失败时应给出明确提示，不继续执行后续步骤

