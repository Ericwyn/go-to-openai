# 添加根证书移除功能计划

## 背景
当前代码已支持安装根证书功能（`root-crt-install`），包括针对不同系统的实现：
- **Linux**: 复制到 `/usr/local/share/ca-certificates` 或 `/etc/pki/ca-trust/source/anchors`，然后运行 `update-ca-certificates` 或 `update-ca-trust`
- **macOS**: 使用 `security add-trusted-cert` 命令安装到系统钥匙串
- **Windows**: 使用 `certutil -addstore Root` 命令安装到受信任的根证书颁发机构

## 目标
添加 `root-crt-remove` 命令，用于从系统信任存储中移除之前安装的根证书，同样需要适配不同操作系统。

## 实现步骤

### 1. 添加新的命令常量
- 在 `printUsage()` 函数中添加 `root-crt-remove` 命令的说明

### 2. 添加命令路由
- 在 `main()` 函数的 switch 语句中添加 `root-crt-remove` 命令的处理分支
- 调用新的 `handleRootCrtRemove()` 函数

### 3. 实现 `handleRootCrtRemove()` 函数
- 解析命令行参数（`-cert-path`，默认值为 `./cert/goto-openai-root.crt`）
- 调用 `removeRootCA()` 函数执行移除操作
- 输出成功或失败信息

### 4. 实现 `removeRootCA()` 函数
- 检查证书文件是否存在
- 根据 `runtime.GOOS` 分发到不同系统的移除函数：
  - `removeRootCALinux()`
  - `removeRootCAMacOS()`
  - `removeRootCAWindows()`

### 5. 实现 `removeRootCALinux()` 函数
- 检测 Linux 发行版（复用 `detectLinuxDistro()` 函数）
- 根据发行版确定证书安装路径：
  - Debian/Ubuntu: `/usr/local/share/ca-certificates/`
  - RHEL/CentOS/Fedora: `/etc/pki/ca-trust/source/anchors/`
- 删除对应的证书文件
- 运行相应的更新命令（`update-ca-certificates` 或 `update-ca-trust`）

### 6. 实现 `removeRootCAMacOS()` 函数
- 使用 `security delete-certificate` 命令移除证书
- 需要通过证书的 Common Name（`goto-openai-root`）来定位和删除
- 使用 `sudo` 执行系统级操作

### 7. 实现 `removeRootCAWindows()` 函数
- 使用 `certutil -delstore Root` 命令从受信任的根证书颁发机构存储中删除证书
- 需要通过证书序列号或名称来定位

### 8. 更新使用帮助
- 在 `printUsage()` 中添加 `root-crt-remove` 命令的示例

## 技术细节

### Linux 移除策略
- 直接删除已安装的证书文件
- 运行更新命令刷新系统证书存储

### macOS 移除策略
- macOS 的 `security delete-certificate` 需要证书的唯一标识
- 可以通过 Common Name 查找并删除
- 命令示例：`sudo security delete-certificate -c "Go-To-OpenAI Root CA" /Library/Keychains/System.keychain`

### Windows 移除策略
- 使用 `certutil -delstore Root` 命令
- 需要先获取证书的序列号，然后通过序列号删除
- 或者使用 PowerShell 的 `Remove-Item` 命令操作证书存储

## 文件修改清单
- `main.go`: 添加所有移除相关的函数和命令处理

## 测试建议
- 在各个操作系统上测试移除功能
- 验证移除后证书不再被系统信任
- 测试重复移除的情况（证书已不存在时）
