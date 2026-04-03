# go-to-openai

一个本地 HTTPS 反向代理，用于在本地拦截对 `api.openai.com`、`api.anthropic.com` 等域名的请求，并转发到你配置的上游服务器。

支持同时代理多个域名，每个域名可以配置不同的上游地址和路由规则。

## 目录

```text
go-to-openai/
├── cert/
├── cmd/
│   └── gen-certs/
│       ├── main.go
│       └── main_test.go
├── go.mod
├── main.go
├── main_test.go
├── config.json.template
└── README.md
```

## 运行前提

1. 本机 `hosts` 增加需要代理的域名：

```text
127.0.0.1 api.openai.com
127.0.0.1 api.anthropic.com
```

2. 系统信任生成的 CA 证书
3. 本机 `443` 端口可用

> Linux/macOS 监听 `443` 往往需要 `root` 或额外授权。

## 快速开始

首先编译项目：

```bash
go build -o go-to-openai .
```

### 1. 生成根证书

```bash
./go-to-openai root-crt-gen
```

生成的文件：

- `cert/goto-openai-root.crt` - 根 CA 证书
- `cert/goto-openai-root.key` - 根 CA 私钥

### 2. 安装根证书到系统信任存储

```bash
sudo ./go-to-openai root-crt-install
```

支持的平台：

- **Linux (Debian/Ubuntu)**: 自动复制到 `/usr/local/share/ca-certificates/` 并更新
- **Linux (RHEL/CentOS/Fedora)**: 自动复制到 `/etc/pki/ca-trust/source/anchors/` 并更新
- **macOS**: 使用 `security` 命令安装到系统钥匙串
- **Windows**: 使用 `certutil` 安装到本地计算机信任存储（需要管理员权限）

### 3. 生成域名证书

为每个需要代理的域名生成独立的证书：

```bash
./go-to-openai domain-crt-gen "api.openai.com"
./go-to-openai domain-crt-gen "api.anthropic.com"
```

生成的文件：

- `cert/goto-openai-dm-api.openai.com.crt` - 域名证书
- `cert/goto-openai-dm-api.openai.com.key` - 域名私钥

### 4. 启动代理服务

```bash
sudo ./go-to-openai run -config="./config.json"
```

## 命令参考

### root-crt-gen

生成根 CA 证书。

```bash
./go-to-openai root-crt-gen [options]
```

选项：

| 选项 | 说明 | 默认值 |
|---|---|---|
| `--ca-cn` | CA 通用名称 | `Go-To-OpenAI Root CA` |
| `--ca-org` | CA 组织名称 | `Go-To-OpenAI` |
| `--ca-days` | 证书有效期（天） | `3650` |
| `--output-dir` | 输出目录 | `./cert` |

### root-crt-install

安装根 CA 证书到系统信任存储。

```bash
sudo ./go-to-openai root-crt-install [options]
```

选项：

| 选项 | 说明 | 默认值 |
|---|---|---|
| `--cert-path` | 根证书路径 | `./cert/goto-openai-root.crt` |

### domain-crt-gen

为指定域名生成服务端证书。

```bash
./go-to-openai domain-crt-gen <domain> [options]
```

选项：

| 选项 | 说明 | 默认值 |
|---|---|---|
| `--root-ca-cert` | 根证书路径 | `./cert/goto-openai-root.crt` |
| `--root-ca-key` | 根私钥路径 | `./cert/goto-openai-root.key` |
| `--server-days` | 证书有效期（天） | `825` |
| `--output-dir` | 输出目录 | `./cert` |

### run

启动 HTTPS 代理服务。

```bash
sudo ./go-to-openai run [options]
```

选项：

| 选项 | 说明 | 默认值 |
|---|---|---|
| `-config` | 配置文件路径 | `./config.json` |
| `-debug` | 开启调试模式 | `false` |

## 配置

支持 JSON 配置文件和环境变量两种方式。环境变量优先级高于配置文件。

### JSON 配置文件

复制 `config.json.template` 为 `config.json` 并修改：

```json
{
  "listen_addr": "127.0.0.1:443",
  "tls_cert_file": "./cert/goto-openai-dm-api.openai.com.crt",
  "tls_key_file": "./cert/goto-openai-dm-api.openai.com.key",
  "retry_max": 3,
  "upstreams": [
    {
      "host": "api.openai.com",
      "base_url": "http://openai-backend.local",
      "base_host": "openai-backend.local",
      "routes": [
        {"path": "/v1/chat/completions", "target_path": "/v1/chat/completions"},
        {"path": "/v1/models", "target_path": "/v1/models"},
        {"path": "/v1/responses", "target_path": "/v1/responses"}
      ]
    },
    {
      "host": "api.anthropic.com",
      "base_url": "http://anthropic-backend.local",
      "base_host": "anthropic-backend.local",
      "routes": [
        {"path": "/v1/messages", "target_path": "/v1/messages"}
      ]
    }
  ]
}
```

### 配置字段说明

| 字段 | 说明 |
|---|---|
| `listen_addr` | HTTPS 监听地址，默认 `:443` |
| `tls_cert_file` | TLS 证书路径 |
| `tls_key_file` | TLS 私钥路径 |
| `retry_max` | 网络错误时的最大重试次数，默认 `3` |
| `upstreams` | 上游配置数组，至少需要一个 |

### upstream 配置

| 字段 | 说明 |
|---|---|
| `host` | 请求的 Host 头，用于匹配路由 |
| `base_url` | 上游服务器地址 |
| `base_host` | 可选，转发时设置的 Host 头，默认使用 `base_url` 的 Host |
| `routes` | 路由规则数组 |

### route 配置

| 字段 | 说明 |
|---|---|
| `path` | 请求路径，必须以 `/` 开头 |
| `target_path` | 转发到上游的路径，必须以 `/` 开头 |

### 环境变量

| 变量名 | 说明 |
|---|---|
| `LISTEN_ADDR` | HTTPS 监听地址 |
| `TLS_CERT_FILE` | TLS 证书路径 |
| `TLS_KEY_FILE` | TLS 私钥路径 |

## 验证示例

### chat completions

```bash
curl --location 'https://api.openai.com/v1/chat/completions' \
  --header 'Authorization: Bearer <YOUR_API_KEY>' \
  --header 'Content-Type: application/json' \
  --data-raw '{
    "messages": [
      {
        "role": "user",
        "content": "简单介绍一下你自己"
      }
    ],
    "max_tokens": 2000,
    "temperature": 0,
    "model": "gpt-5.4-mini",
    "stream": true
  }'
```

### responses

```bash
curl --location 'https://api.openai.com/v1/responses' \
  --header 'Authorization: Bearer <YOUR_API_KEY>' \
  --header 'Content-Type: application/json' \
  --data-raw '{
    "model": "gpt-5.1",
    "input": "请用三句话介绍 NestJS",
    "stream": true
  }'
```

## 健康检查

```bash
curl https://api.openai.com/healthz
```

## 特性

- 支持多域名代理，根据 Host 头自动路由
- 保留原始请求体，不改参数
- 透传 `Authorization` 和 query string
- 支持流式响应转发
- 网络错误自动重试
- 结构化日志输出
- 调试模式可 dump 请求内容
- 跨平台证书安装支持（Linux/macOS/Windows）
- 统一的 CLI 命令结构，易于使用
