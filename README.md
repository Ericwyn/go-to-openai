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

## 生成证书

在 `go-to-openai/` 目录执行：

```bash
go run ./cmd/gen-certs --domains api.openai.com,api.anthropic.com
```

第一个域名会作为主域名，决定输出文件名；其余域名会写入同一张服务端证书的 SAN 中。

生成的文件：

- `cert/openaica.crt` - CA 证书
- `cert/openaica.key` - CA 私钥
- `cert/api.openai.com.crt` - 服务端证书（包含所有域名的 SAN）
- `cert/api.openai.com.key` - 服务端私钥
- `cert/api.openai.com.csr` - 证书签名请求

### 指定输出目录

```bash
go run ./cmd/gen-certs --domains api.openai.com,api.anthropic.com --output /tmp/my-certs
```

### 自定义 CA 和证书有效期

```bash
go run ./cmd/gen-certs \
  --domains api.openai.com,api.anthropic.com \
  --ca-cn "My Local CA" \
  --ca-org "My Team" \
  --ca-days 3650 \
  --server-days 825
```

## 导入 CA 证书

### Debian/Ubuntu

```bash
sudo cp cert/openaica.crt /usr/local/share/ca-certificates/openaica.crt
sudo update-ca-certificates
```

### macOS

```bash
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain cert/openaica.crt
```

### 删除 CA

```bash
# Debian/Ubuntu
sudo rm -f /usr/local/share/ca-certificates/openaica.crt
sudo update-ca-certificates --fresh

# macOS
sudo security delete-certificate -c "My Local CA" /Library/Keychains/System.keychain
```

## 配置

支持 JSON 配置文件和环境变量两种方式。环境变量优先级高于配置文件。

### JSON 配置文件

复制 `config.json.template` 为 `config.json` 并修改：

```json
{
  "listen_addr": "127.0.0.1:443",
  "tls_cert_file": "./cert/api.openai.com.crt",
  "tls_key_file": "./cert/api.openai.com.key",
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

## 启动

在 `go-to-openai` 目录执行：

```bash
go run .
```

或先编译：

```bash
go build -o go-to-openai .
./go-to-openai
```

指定配置文件：

```bash
go run . -config /path/to/config.json
```

开启调试模式（请求 dump 到 `.logs` 目录）：

```bash
go run . -debug
```

如果你只是本地验证，不想占用 `443`：

```bash
LISTEN_ADDR=:8443 go run .
```

## 健康检查

```bash
curl https://api.openai.com/healthz
```

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

## 特性

- 支持多域名代理，根据 Host 头自动路由
- 保留原始请求体，不改参数
- 透传 `Authorization` 和 query string
- 支持流式响应转发
- 网络错误自动重试
- 结构化日志输出
- 调试模式可 dump 请求内容
