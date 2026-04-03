# 代码重构与添加证书移除功能计划

## 背景

当前 `main.go` 文件包含了所有功能：

* 证书管理（生成、安装）

* HTTP 代理服务

* 配置加载与解析

* 命令行解析与路由

代码耦合度高，不利于维护和扩展。

## 目标

1. 将代码重构为清晰的包结构
2. 添加根证书移除功能（`root-crt-remove` 命令）
3. 保持原有功能不变，确保向后兼容

## 新的目录结构

```
go-to-openai/
├── main.go                    # 仅包含命令解析和路由
├── config/                    # 配置模块
│   └── config.go              # 配置加载、解析、验证
├── certmanager/               # 证书管理模块
│   ├── certmanager.go         # 证书生成（根证书、域名证书）
│   ├── install.go             # 证书安装（多系统适配）
│   └── remove.go              # 证书移除（多系统适配）【新增】
├── proxy/                     # HTTP 代理模块
│   ├── handler.go             # HTTP 处理器
│   ├── middleware.go          # 中间件（日志、调试）
│   ├── retry.go               # 重试传输层
│   └── server.go              # 服务器启动逻辑
└── .trae/
    └── documents/
        └── refactor-code-and-add-certificate-removal-feature.md
```

## 详细实现步骤

### 第一阶段：创建 config 包

#### 1.1 创建 `config/config.go`

* 迁移以下类型和函数：

  * `Config` struct（原 `config`，首字母大写导出）

  * `UpstreamConfig` struct（原 `upstreamConfig`）

  * `RouteConfig` struct（原 `routeConfig`）

  * `Default()` → 返回默认配置（原 `defaultConfig()`）

  * `Load(configFile string) (Config, error)` → 加载配置（原 `loadConfig()`）

  * 内部函数：`mergeJSONConfig()`, `applyEnvOverrides()`, `validateConfig()`, `envOrDefault()`

* 导出必要的公共函数和类型

### 第二阶段：创建 certmanager 包

#### 2.1 创建 `certmanager/certmanager.go`

* 迁移证书生成相关函数：

  * `GenerateRootCA(caCN, caOrg string, days int, outputDir string) error`

  * `GenerateDomainCert(domain, rootCACertPath, rootCAKeyPath string, days int, outputDir string) error`

  * 内部函数：`newCATemplate()`, `newServerTemplate()`, `extractDNSNames()`, `extractIPAddresses()`, `randomSerialNumber()`, `writePrivateKey()`, `writeCertificate()`, `writePEMFile()`

* 导出常量：`DefaultRootCAName`, `DefaultDomainPrefix`

#### 2.2 创建 `certmanager/install.go`

* 迁移证书安装相关函数：

  * `Install(certPath string) error` → 主入口（原 `installRootCA()`）

  * 内部函数：`installRootCALinux()`, `installRootCAMacOS()`, `installRootCAWindows()`, `detectLinuxDistro()`

#### 2.3 创建 `certmanager/remove.go`【新增功能】

* 实现证书移除功能：

  * `Remove(certPath string) error` → 主入口函数

  * `removeRootCALinux(certPath string) error` → Linux 实现

    * 检测发行版，确定证书路径

    * 删除证书文件

    * 运行更新命令

  * `removeRootCAMacOS(certPath string) error` → macOS 实现

    * 使用 `security delete-certificate -c "Go-To-OpenAI Root CA"` 删除

  * `removeRootCAWindows(certPath string) error` → Windows 实现

    * 使用 PowerShell 通过 Subject CN 查找并删除证书

    * 命令：`powershell -Command "Get-ChildItem cert:\LocalMachine\Root | Where-Object {$_.Subject -like '*Go-To-OpenAI Root CA*'} | Remove-Item -Force"`

    * 备选方案：使用 `certutil -store Root` 列出证书，解析输出获取序列号，然后 `certutil -delstore Root <serial>` 删除

### 第三阶段：创建 proxy 包

#### 3.1 创建 `proxy/handler.go`

* 迁移 HTTP 处理器相关代码：

  * `hostRoute` struct

  * `NewHandler(cfg config.Config, debug bool) (http.Handler, error)` → 创建处理器（原 `newHandler()`）

  * 内部函数：`newRouteProxy()`, `writeStatusPage()`, `writeJSONError()`

#### 3.2 创建 `proxy/middleware.go`

* 迁移中间件相关代码：

  * `LoggingMiddleware(next http.Handler) http.Handler` → 日志中间件（原 `loggingMiddleware()`）

  * `DebugMiddleware(next http.Handler) http.Handler` → 调试中间件（原 `debugMiddleware()`）

  * `statusRecorder` struct

  * 内部函数：`dumpRequest()`, `writeDebugRequest()`

* 导出常量：`DefaultLogsDir`

#### 3.3 创建 `proxy/retry.go`

* 迁移重试相关代码：

  * `retryTransport` struct

  * 内部函数：`newRetryTransport()`, `snapshotRequestBody()`, `cloneRequest()`, `isRetryableProxyError()`, `transportOrDefault()`

#### 3.4 创建 `proxy/server.go`

* 迁移服务器启动逻辑：

  * `Run(cfg config.Config, debug bool) error` → 启动服务器

  * 包含 `http.Server` 创建、配置和启动

### 第四阶段：重构 main.go

#### 4.1 精简 main.go

* 仅保留：

  * `main()` 函数 - 命令路由

  * `printUsage()` 函数 - 使用帮助

  * `handleRootCrtGen()` - 调用 `certmanager.GenerateRootCA()`

  * `handleRootCrtInstall()` - 调用 `certmanager.Install()`

  * `handleRootCrtRemove()` 【新增】- 调用 `certmanager.Remove()`

  * `handleDomainCrtGen()` - 调用 `certmanager.GenerateDomainCert()`

  * `handleRun()` - 调用 `config.Load()` 和 `proxy.Run()`

#### 4.2 添加 `root-crt-remove` 命令

* 在 `main()` 的 switch 中添加：

  ```go
  case "root-crt-remove", "-root-crt-remove":
      handleRootCrtRemove(args)
  ```

* 实现 `handleRootCrtRemove()` 函数

* 在 `printUsage()` 中添加命令说明

### 第五阶段：更新导入和测试

#### 5.1 更新模块依赖

* 确保 `go.mod` 中的 module 名称正确

* 所有包使用正确的相对导入路径（如 `go-to-openai/config`, `go-to-openai/certmanager`, `go-to-openai/proxy`）

#### 5.2 编译验证

* 运行 `go build` 确保编译通过

* 验证所有命令正常工作

## 技术细节

### 证书移除实现细节

#### Linux

```bash
# 删除证书文件
rm /usr/local/share/ca-certificates/goto-openai-root.crt
# 或
rm /etc/pki/ca-trust/source/anchors/goto-openai-root.crt

# 更新证书存储
update-ca-certificates
# 或
update-ca-trust
```

#### macOS

```bash
# 通过 Common Name 删除证书
sudo security delete-certificate -c "Go-To-OpenAI Root CA" /Library/Keychains/System.keychain
```

#### Windows

Windows 提供两种删除证书的方法：

**方法一：PowerShell（推荐）**

```powershell
# 通过 Subject CN 查找并删除证书
Get-ChildItem cert:\LocalMachine\Root | Where-Object {$_.Subject -like '*Go-To-OpenAI Root CA*'} | Remove-Item -Force
```

* 优点：直接通过证书名称查找，无需解析序列号

* 缺点：需要 PowerShell 执行权限

**方法二：certutil（备选）**

```bash
# 步骤 1: 列出 Root 存储中的所有证书，找到目标证书的序列号
certutil -store Root

# 步骤 2: 通过序列号删除证书
certutil -delstore Root <serial_number>
```

* 优点：不依赖 PowerShell

* 缺点：需要解析 `certutil -store` 的输出，提取序列号（输出格式为 `Serial Number: xxxxxxxxxxxx`）

**实现策略**：

1. 优先使用 PowerShell 方法（更简洁可靠）
2. 如果 PowerShell 不可用，回退到 certutil 方法
3. 解析 certutil 输出时，通过证书的 Subject 字段匹配 `Go-To-OpenAI Root CA`

### 包导出规则

* 导出的函数和类型使用大写字母开头

* 内部辅助函数使用小写字母开头

* 保持最小化的公共 API 表面

### 错误处理

* 使用 `fmt.Errorf` 包装错误，提供上下文信息

* 保持与原有代码一致的错误消息格式

## 文件修改清单

### 新建文件

1. `config/config.go`
2. `certmanager/certmanager.go`
3. `certmanager/install.go`
4. `certmanager/remove.go`
5. `proxy/handler.go`
6. `proxy/middleware.go`
7. `proxy/retry.go`
8. `proxy/server.go`

### 修改文件

1. `main.go` - 精简为命令路由层

### 保持不变

* `go.mod` - 除非需要更新 module 名称

* `config.json` - 配置文件格式不变

* `cert/` 目录 - 证书文件不受影响

## 向后兼容性

* 所有命令行接口保持不变

* 配置文件格式不变

* 默认参数值不变

* 输出消息格式保持一致

## 测试建议

* 编译测试：`go build -o go-to-openai`

* 功能测试：

  * `./go-to-openai root-crt-gen`

  * `./go-to-openai root-crt-install`

  * `./go-to-openai root-crt-remove` 【新增】

  * `./go-to-openai domain-crt-gen api.openai.com`

  * `./go-to-openai run -config=./config.json`

* 代码检查：`go vet ./...`

