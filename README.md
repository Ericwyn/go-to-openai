# go-to-openai

一个最小可用的 Go HTTPS 代理，用来在本地接管 `https://api.openai.com`，并把请求转发到 `http://yourdomain.com`。

目前处理三个 OpenAI 兼容入口：

| 本地入口 | 转发目标 |
|---|---|
| `POST /v1/chat/completions` | `http://yourdomain.com/v1/chat/completions` |
| `GET /v1/models` | `http://yourdomain.com/v1/models` |
| `POST /v1/responses` | `http://yourdomain.com/openai/responses` |

特性：

- 保留原始请求体，不改参数
- 透传 `Authorization`
- 透传 query string
- 支持流式响应转发
- 默认读取 `go-to-openai/cert` 下的证书文件

## 目录

```text
go-to-openai/
├── cert/
├── cmd/
│   └── gen-certs/
│       ├── main.go
│       └── main_test.go
├── scripts/
│   └── gen-certs.sh
├── go.mod
├── main.go
├── main_test.go
└── README.md
```

## 运行前提

1. 本机 `hosts` 增加：

```text
127.0.0.1 api.openai.com
```

2. 系统信任 `go-to-openai/cert/openaica.crt`
3. 本机 `443` 端口可用

> Linux/macOS 监听 `443` 往往需要 `root` 或额外授权。

## 先生成一套新证书

在 `go-to-openai/` 目录执行：

```bash
go run ./cmd/gen-certs
```

默认会生成一套适用于 `api.openai.com` 的证书文件：

- `cert/openaica.crt`
- `cert/openaica.key`
- `cert/api.openai.com.crt`
- `cert/api.openai.com.key`
- `cert/api.openai.com.csr`

如果目录里已有同名文件，会先备份到 `cert/backup-时间戳/`。

### 指定多个域名

多个域名使用逗号分隔，第一个域名会作为主域名，同时决定输出文件名；其余域名会写入同一张服务端证书的 SAN 中。

```bash
go run ./cmd/gen-certs --domains api.openai.com,foo.local,127.0.0.1
```

上面的命令会生成：

- `cert/openaica.crt`
- `cert/openaica.key`
- `cert/api.openai.com.crt`
- `cert/api.openai.com.key`
- `cert/api.openai.com.csr`

其中 `api.openai.com.crt` 同时可用于：

- `api.openai.com`
- `foo.local`
- `127.0.0.1`

### 指定输出目录

```bash
go run ./cmd/gen-certs --output /tmp/my-openai-certs
```

### 自定义 CA 和证书有效期

```bash
go run ./cmd/gen-certs \
  --domains api.openai.com,foo.local \
  --ca-cn "My Local CA" \
  --ca-org "My Team" \
  --server-org "My Dev Server" \
  --ca-days 3650 \
  --server-days 825
```

## Linux 下导入、更新、删除 CA 证书

以下示例适用于 Debian/Ubuntu 系系统。

### 首次导入 CA

生成完成后执行：

```bash
sudo cp cert/openaica.crt /usr/local/share/ca-certificates/openaica.crt
sudo update-ca-certificates
```

然后确认 `hosts`：

```bash
echo '127.0.0.1 api.openai.com' | sudo tee -a /etc/hosts
```

### 更新 CA

如果你重新执行了 `go run ./cmd/gen-certs`，生成了新的 `cert/openaica.crt`，需要重新覆盖系统里的 CA 并刷新证书库：

```bash
sudo cp cert/openaica.crt /usr/local/share/ca-certificates/openaica.crt
sudo update-ca-certificates
```

如果你之前已经把旧证书导入过，直接覆盖同名文件再执行 `update-ca-certificates` 即可。

### 删除 CA

如果你不再需要这套本地 CA，可以删除系统中的证书并刷新证书库：

```bash
sudo rm -f /usr/local/share/ca-certificates/openaica.crt
sudo update-ca-certificates --fresh
```

如果你还加过 `hosts`，也记得把对应记录删掉：

```text
127.0.0.1 api.openai.com
```

## 配置

支持以下环境变量：

| 变量名 | 默认值 | 说明 |
|---|---|---|
| `LISTEN_ADDR` | `:443` | HTTPS 监听地址 |
| `TLS_CERT_FILE` | `./cert/api.openai.com.crt` | TLS 证书路径 |
| `TLS_KEY_FILE` | `./cert/api.openai.com.key` | TLS 私钥路径 |
| `UPSTREAM_BASE_URL` | `http://yourdomain.com` | 下游基础地址 |

如果你生成的是其他主域名证书，启动时记得同步调整：

```bash
TLS_CERT_FILE=./cert/foo.local.crt \
TLS_KEY_FILE=./cert/foo.local.key \
go run .
```

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

如果你只是本地验证，不想占用 `443`：

```bash
LISTEN_ADDR=:8443 go run .
```

## 推荐启动方式

```bash
LISTEN_ADDR=:443 \
TLS_CERT_FILE=./cert/api.openai.com.crt \
TLS_KEY_FILE=./cert/api.openai.com.key \
UPSTREAM_BASE_URL=http://yourdomain.com \
go run .
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
    "enable_thinking": false,
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

## 已验证内容

本项目已通过本地自动化测试验证：

- `/v1/chat/completions` 路径转发正确
- `/v1/models` 路径转发正确
- `/v1/responses` 路径改写正确
- `Authorization` 和请求体透传正确
- SSE 响应内容可回传
- Go 版证书生成命令可正常工作

由于当前环境禁止外网访问，无法在此环境直接请求真实的 `yourdomain.com` 做联调；上线前请在你的机器上按上面的步骤再做一次实测。
