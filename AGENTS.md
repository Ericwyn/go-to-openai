# AGENTS.md

## 角色定义

作为本项目的资深 Go 工程师，行为准则：简洁、专业、直接。拒绝废话，解决问题，验证结果，继续下一步。

## 项目概述

本地 HTTPS 反向代理工具，用于拦截对 `api.openai.com`、`api.anthropic.com` 等域名的请求，并转发到自定义上游服务器。支持多域名路由、自动重试、跨平台证书管理（生成/安装/移除），运行时零第三方依赖。

## 核心工程规范

1. **错误处理**：使用 `%w` 包装错误（`fmt.Errorf`），绝不静默吞掉错误。使用 `errors.Is` / `errors.As` 进行错误判断。

2. **类型安全**：使用强类型，避免 `interface{}` 或 `any`。优先显式结构体定义。生产路径禁止使用 `panic`，一律返回错误。

3. **命名规范**：遵循 Go 约定——导出标识符用 `CamelCase`，非导出用 `camelCase`。包名小写、单词、无下划线。测试函数使用 `TestXxx` 模式并配合 `t.Parallel()`。

4. **无全局状态**：避免包级可变状态。通过函数参数或结构体字段显式传递依赖。`config.Config` 是运行时配置的唯一事实来源。

5. **最小化依赖**：优先使用 Go 标准库。仅在绝对必要时添加外部依赖。本项目运行时零第三方依赖。

## 常用命令

```bash
go build -o go-to-openai .          # 构建
go test ./...                       # 测试
go test -race ./...                 # 竞态检测测试
go vet ./...                        # 静态分析
go run . run -config=./config.json  # 运行代理服务
```

## 禁止事项

- **严禁**记录密钥、API Key、Token 或证书私钥。
- **严禁**在没有明确并发理由的情况下使用 `sync` 原语。本代理按请求-协程模型设计。
- **严禁**硬编码路径、主机名或端口。所有配置必须通过 `config.Config` 或 CLI 参数传入。

## 仓库结构

```
go-to-openai/
├── main.go              # CLI 入口，命令分发
├── config/
│   └── config.go        # 配置加载、校验、环境变量覆盖
├── proxy/
│   ├── server.go        # HTTPS 服务启动
│   ├── handler.go       # 请求路由与代理逻辑
│   ├── middleware.go    # 日志中间件
│   └── retry.go         # 网络错误重试传输
├── certmanager/
│   ├── certmanager.go   # CA 与域名证书生成
│   ├── install.go       # 跨平台证书安装
│   └── remove.go        # 跨平台证书移除
└── cmd/gen-certs/       # 独立证书生成工具
```

**架构说明**：CLI 工具，支持子命令。`run` 命令启动 HTTPS 反向代理，根据 Host 头拦截请求并转发到可配置的上游服务器。证书管理独立在 `certmanager/`，代理逻辑在 `proxy/`，配置在 `config/`。
