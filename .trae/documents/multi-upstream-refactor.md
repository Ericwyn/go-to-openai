# 多上游反向代理改造计划

## 背景

当前项目硬编码了 `api.openai.com` 作为唯一上游，只能代理 OpenAI API 的请求。需要改造为支持多个上游（如 OpenAI、Anthropic、Vercel Gateway 等），根据请求的 `Host` 头选择对应的上游进行转发。

## 改动范围

### 1. 配置结构改造 (`main.go`)

**新增** **`upstreamConfig`** **类型**，表示一个上游配置：

```go
type upstreamConfig struct {
    Host       string        `json:"host"`
    BaseURL    string        `json:"base_url"`
    BaseHost   string        `json:"base_host"`
    Routes     []routeConfig `json:"routes"`
}
```

**改造** **`config`** **结构体**：

```go
type config struct {
    ListenAddr string            `json:"listen_addr"`
    CertFile   string            `json:"tls_cert_file"`
    KeyFile    string            `json:"tls_key_file"`
    Upstreams  []upstreamConfig  `json:"upstreams"`
    RetryMax   int               `json:"retry_max"`
    Transport  http.RoundTripper `json:"-"`
}
```

**删除旧字段**：`Upstream`、`UpstreamHost`、`Routes` 从 config 中移除。

### 2. 默认配置改造 (`main.go`)

`defaultConfig()` 返回新的多上游结构：

```go
func defaultConfig() config {
    return config{
        ListenAddr: defaultListenAddr,
        CertFile:   defaultCertFile,
        KeyFile:    defaultKeyFile,
        RetryMax:   defaultRetryMax,
        Upstreams: []upstreamConfig{
            {
                Host:    "api.openai.com",
                BaseURL: "http://api.openai.com",
                Routes: []routeConfig{
                    {Path: "/v1/chat/completions", TargetPath: "/v1/chat/completions"},
                    {Path: "/v1/models", TargetPath: "/v1/models"},
                    {Path: "/v1/responses", TargetPath: "/openai/responses"},
                },
            },
        },
    }
}
```

### 3. 配置验证改造 (`main.go`)

`validateConfig()` 改为验证 `Upstreams` 数组：

* `upstreams` 不能为空

* 每个 upstream 的 `host` 必须唯一

* 每个 upstream 的 `base_url` 必须是合法绝对 URL

* 每个 upstream 的 `routes` 不能为空，且 path 不能重复（在同一个 upstream 内）

* `base_host` 如果设置，不能包含 `://`

### 4. 环境变量覆盖改造 (`main.go`)

`applyEnvOverrides()` 简化，只保留全局配置的覆盖：

* `LISTEN_ADDR`、`TLS_CERT_FILE`、`TLS_KEY_FILE` 保留

* 删除 `UPSTREAM_BASE_URL`、`UPSTREAM_BASE_HOST`（多上游无法用单个环境变量覆盖）

### 5. 路由逻辑改造 (`main.go`)

**核心改动**：`newHandler()` 从按 path 路由改为按 Host + path 路由。

```go
func newHandler(cfg config, debug bool) (http.Handler, error) {
    // hostRoutes: map[host][]route
    hostRoutes := make(map[string][]route)

    for _, upstream := range cfg.Upstreams {
        target, err := url.Parse(upstream.BaseURL)
        if err != nil {
            return nil, fmt.Errorf("upstream %s: %w", upstream.Host, err)
        }

        upstreamHost := target.Host
        if upstream.BaseHost != "" {
            upstreamHost = upstream.BaseHost
        }

        routes := make([]route, 0, len(upstream.Routes))
        for _, item := range upstream.Routes {
            routes = append(routes, route{
                Path:  item.Path,
                Proxy: newRouteProxy(target, upstreamHost, item.TargetPath, cfg.Transport, cfg.RetryMax),
            })
        }
        hostRoutes[upstream.Host] = routes
    }

    mux := http.NewServeMux()
    mux.HandleFunc("/healthz", healthzHandler)

    // 注册一个通用 handler，根据 Host 头分发
    mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        host := r.Host
        // 去掉端口号
        if h, _, err := net.SplitHostPort(host); err == nil {
            host = h
        }

        routes, ok := hostRoutes[host]
        if !ok {
            writeJSONError(w, http.StatusNotFound, "unknown host")
            return
        }

        for _, rt := range routes {
            if r.URL.Path == rt.Path {
                rt.Proxy.ServeHTTP(w, r)
                return
            }
        }

        writeJSONError(w, http.StatusNotFound, "unsupported path")
    })

    if debug {
        return debugMiddleware(mux), nil
    }
    return mux, nil
}
```

### 6. 启动日志改造 (`main.go`)

`main()` 中的日志改为列出所有上游：

```go
log.Printf("[listening] : %s", cfg.ListenAddr)
for _, upstream := range cfg.Upstreams {
    log.Printf("[upstream] : %s -> %s", upstream.Host, upstream.BaseURL)
}
```

### 7. 配置模板改造 (`config.json.template`)

```json
{
  "listen_addr": "127.0.0.1:443",
  "tls_cert_file": "./cert/wildcard.crt",
  "tls_key_file": "./cert/wildcard.key",
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

### 8. 测试改造 (`main_test.go`)

所有测试需要适配新的配置结构：

* `newTestHandler` 改为接受新的 config 结构

* `TestChatCompletionsProxy`、`TestModelsProxy`、`TestResponsesProxy` 等需要在请求中设置正确的 Host 头

* `TestCustomRouteConfig` 改为测试自定义 upstream

* `TestProxyUsesConfiguredUpstreamHost` 改为测试 upstream 的 base\_host

* `TestUnsupportedPath` 改为测试未知 Host 和未知 Path 两种情况

* `TestLoadConfigFromJSONAndEnvOverride` 更新 JSON 结构和断言

* 新增测试：多 upstream 路由分发

### 9. gen-certs 无需改动

`cmd/gen-certs/main.go` 已经支持 `--domains` 参数生成多 SAN 证书，无需修改。

## 文件改动清单

| 文件                     | 改动类型                    |
| ---------------------- | ----------------------- |
| `main.go`              | 重构配置结构、路由逻辑、验证逻辑、日志     |
| `main_test.go`         | 适配新配置结构，新增多 upstream 测试 |
| `config.json.template` | 更新为多 upstream 格式        |

## 不改动的文件

| 文件                           | 原因       |
| ---------------------------- | -------- |
| `cmd/gen-certs/main.go`      | 已支持多域名证书 |
| `cmd/gen-certs/main_test.go` | 无需改动     |
| `go.mod`                     | 无新依赖     |

## 实施顺序

1. 改造 `main.go` 中的类型定义（config、upstreamConfig、routeConfig）
2. 改造 `defaultConfig()`、`validateConfig()`、`applyEnvOverrides()`
3. 改造 `newHandler()` 路由逻辑
4. 改造 `main()` 启动日志
5. 更新 `config.json.template`
6. 更新 `main_test.go` 中所有测试
7. 运行测试验证

