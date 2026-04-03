# CLI 重构与跨平台优化计划

## 背景

当前项目存在以下问题：

1. `gen-certs` 作为独立子命令，使用不够便捷
2. 证书安装需要手动操作，不支持跨平台自动化
3. 主程序和证书生成工具分离，用户体验不够统一
4. 缺少清晰的 CLI 命令结构

## 目标

将项目重构为统一的 CLI 工具，提供清晰的子命令：

* `-root-crt-gen`：生成根证书

* `-root-crt-install`：安装根证书（跨平台）

* `-domain-crt-gen`：生成域名证书

* `-run`：启动 HTTPS 代理服务

## 设计原则

1. **单一二进制文件**：所有功能集成到一个可执行文件
2. **跨平台兼容**：支持 Linux、macOS、Windows
3. **逻辑清晰**：每个命令职责单一，参数明确
4. **向后兼容**：保留原有配置文件格式

***

## 实施步骤

### 步骤 1：重构 CLI 入口结构

**文件**：`main.go`

**改动内容**：

1. 定义新的 CLI 命令结构体，使用子命令模式
2. 解析命令行参数，根据子命令分发到不同处理函数
3. 保留原有的 `parseFlags` 作为 `-run` 命令的参数解析

**新的命令结构**：

```
go-to-openai <command> [options]

Commands:
  root-crt-gen          生成根证书
  root-crt-install      安装根证书到系统信任存储
  domain-crt-gen        生成指定域名的证书
  run                   启动 HTTPS 代理服务
```

**实现方式**：

* 不使用第三方库，手动实现子命令解析

* 第一个参数作为命令标识（如 `root-crt-gen`）

* 后续参数作为该命令的选项

***

### 步骤 2：实现根证书生成命令

**命令**：`-root-crt-gen`

**文件**：`main.go`（新增 `generateRootCA` 函数）

**功能**：

* 生成 RSA 2048 位私钥

* 创建自签名 CA 证书（有效期 10 年）

* 输出到 `cert/goto-openai-root.crt` 和 `cert/goto-openai-root.key`

**参数**：

* `--ca-cn`：CA 通用名称（默认 "Go-To-OpenAI Root CA"）

* `--ca-org`：CA 组织名称（默认 "Go-To-OpenAI"）

* `--ca-days`：有效期天数（默认 3650）

* `--output-dir`：输出目录（默认 `./cert`）

**实现细节**：

* 从 `cmd/gen-certs/main.go` 迁移 CA 生成逻辑

* 复用 `newCATemplate`、`writePrivateKey`、`writeCertificate` 等函数

* 自动创建输出目录

***

### 步骤 3：实现根证书安装命令（跨平台）

**命令**：`-root-crt-install`

**文件**：`main.go`（新增 `installRootCA` 函数）

**功能**：

* 检测当前操作系统

* 将根证书安装到系统信任存储

* 需要管理员权限时提示用户

**跨平台实现**：

**Linux (Debian/Ubuntu)**：

```bash
sudo cp cert/goto-openai-root.crt /usr/local/share/ca-certificates/
sudo update-ca-certificates
```

**Linux (RHEL/CentOS/Fedora)**：

```bash
sudo cp cert/goto-openai-root.crt /etc/pki/ca-trust/source/anchors/
sudo update-ca-trust
```

**macOS**：

```bash
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain cert/goto-openai-root.crt
```

**Windows**：

```powershell
Import-Certificate -FilePath "cert\goto-openai-root.crt" -CertStoreLocation Cert:\LocalMachine\Root
```

**实现细节**：

* 使用 `runtime.GOOS` 检测操作系统

* Linux 检测发行版（检查 `/etc/os-release` 或使用 `lsb_release`）

* Windows 使用 `certutil` 或 PowerShell 命令

* 提供清晰的错误提示和操作指引

**参数**：

* `--cert-path`：证书路径（默认 `./cert/goto-openai-root.crt`）

***

### 步骤 4：实现域名证书生成命令

**命令**：`-domain-crt-gen <domain>`

**文件**：`main.go`（新增 `generateDomainCert` 函数）

**功能**：

* 读取已有的根证书和私钥

* 为指定域名生成服务端证书

* 输出到 `cert/goto-openai-dm-{domain}.crt` 和 `.key`

**参数**：

* 域名作为位置参数（如 `-domain-crt-gen api.openai.com`）

* `--root-ca-cert`：根证书路径（默认 `./cert/goto-openai-root.crt`）

* `--root-ca-key`：根私钥路径（默认 `./cert/goto-openai-root.key`）

* `--server-days`：有效期天数（默认 825）

* `--output-dir`：输出目录（默认 `./cert`）

**实现细节**：

* 从 `cmd/gen-certs/main.go` 迁移服务端证书生成逻辑

* 复用 `newServerTemplate`、`writePrivateKey`、`writeCertificate` 等函数

* 支持 IP 地址和域名

* 验证根证书是否存在，不存在则提示先生成根证书

**文件命名**：

* 证书：`cert/goto-openai-dm-api.openai.com.crt`

* 私钥：`cert/goto-openai-dm-api.openai.com.key`

***

### 步骤 5：重构启动服务命令

**命令**：`-run`

**文件**：`main.go`

**功能**：

* 保持原有反向代理逻辑不变

* 调整参数解析方式，适配新的 CLI 结构

**参数**：

* `-config`：配置文件路径（默认 `./config.json`）

* `-debug`：开启调试模式

**实现细节**：

* 将原有 `main()` 函数逻辑迁移到 `runServer` 函数

* 保留所有现有功能（多上游、重试、日志等）

* 配置文件格式保持不变

***

### 步骤 6：更新配置文件模板

**文件**：`config.json.template`

**改动内容**：

* 更新注释，说明证书路径建议使用新的命名规范

* 示例配置使用 `goto-openai-dm-{domain}.crt` 格式

***

### 步骤 7：更新运行脚本

**文件**：`run.sh`

**改动内容**：

* 提供完整的示例工作流

* 包含证书生成、安装、服务启动的完整流程

**示例脚本**：

```bash
#!/bin/bash

# 1. 生成根证书
./go-to-openai -root-crt-gen

# 2. 安装根证书（需要 sudo）
sudo ./go-to-openai -root-crt-install

# 3. 生成域名证书
./go-to-openai -domain-crt-gen "api.openai.com"
./go-to-openai -domain-crt-gen "api.anthropic.com"

# 4. 启动服务
sudo ./go-to-openai -run -config="./config.json"
```

***

### 步骤 8：更新测试

**文件**：`main_test.go`

**改动内容**：

* 新增 CLI 命令解析测试

* 新增证书生成函数测试

* 保留所有现有反向代理测试

* 新增跨平台证书安装逻辑测试（模拟不同 OS）

***

### 步骤 9：更新文档

**文件**：`README.md`

**改动内容**：

* 更新快速开始指南，使用新的 CLI 命令

* 添加跨平台说明

* 更新证书生成章节

* 添加 Windows 使用说明

***

## 文件改动清单

| 文件                     | 改动类型 | 说明                 |
| ---------------------- | ---- | ------------------ |
| `main.go`              | 重构   | 整合所有命令，新增证书生成和安装逻辑 |
| `main_test.go`         | 更新   | 新增 CLI 和证书生成测试     |
| `config.json.template` | 更新   | 更新证书路径示例           |
| `run.sh`               | 更新   | 提供完整工作流示例          |
| `README.md`            | 更新   | 更新文档说明             |
| `cmd/gen-certs/`       | 保留   | 可选保留作为独立工具，或标记为废弃  |

***

## 实施顺序

1. 重构 CLI 入口，实现子命令分发
2. 实现 `-root-crt-gen` 命令
3. 实现 `-root-crt-install` 命令（跨平台）
4. 实现 `-domain-crt-gen` 命令
5. 重构 `-run` 命令
6. 更新配置文件模板
7. 更新运行脚本
8. 更新测试
9. 更新文档
10. 运行测试验证

***

## 跨平台兼容性说明

### 支持的平台

| 平台                    | root-crt-gen | root-crt-install | domain-crt-gen | run |
| --------------------- | ------------ | ---------------- | -------------- | --- |
| Linux (Debian/Ubuntu) | ✅            | ✅                | ✅              | ✅   |
| Linux (RHEL/CentOS)   | ✅            | ✅                | ✅              | ✅   |
| macOS                 | ✅            | ✅                | ✅              | ✅   |
| Windows               | ✅            | ✅                | ✅              | ✅   |

### 注意事项

1. **权限要求**：

   * 证书安装需要管理员权限（sudo / 管理员）

   * 监听 443 端口需要管理员权限（Linux/macOS）

2. **Windows 特殊处理**：

   * 使用 `certutil` 或 PowerShell 安装证书

   * 路径分隔符使用 `\`

3. **Linux 发行版检测**：

   * 优先检查 `/etc/os-release`

   * 回退到 `lsb_release` 命令

   * 默认使用 Debian/Ubuntu 方式

***

## 向后兼容性

* 保留原有 `config.json` 格式

* 保留原有 `-config` 和 `-debug` 参数

* `cmd/gen-certs/` 暂时保留，后续版本可移除

