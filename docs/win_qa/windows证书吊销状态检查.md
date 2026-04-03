# Windows 证书吊销状态检查问题

## 问题描述

在 Windows 系统上使用 curl 测试 go-to-openai 代理时，可能会遇到以下错误：

```powershell
PS C:\Users\Ericwyn> curl `https://api.openai.com/` 
 curl: (35) schannel: next InitializeSecurityContext failed: CRYPT_E_NO_REVOCATION_CHECK (0x80092012) - 吊销功能无法检查 证书是否吊销。
```

## 问题原因

这个错误是由于 Windows 上的 curl 使用 Schannel（Windows 的 SSL/TLS 提供者）进行证书验证时，会默认尝试检查证书的吊销状态（通过 CRL 或 OCSP）。由于 go-to-openai 生成的是自签名证书，没有配置吊销检查机制，因此 Schannel 会报错。

## 解决方案

### 方案 1：使用 `--ssl-no-revoke` 参数（临时解决）

```powershell
curl --ssl-no-revoke https://api.openai.com/
```

这个参数告诉 curl 跳过证书吊销检查，只验证证书的有效性。

### 方案 2：设置环境变量（永久解决）

在 PowerShell 中执行：

```powershell
$env:CURL_SSL_NO_REVOKE = "1"
```

或者添加到系统环境变量中，这样每次使用 curl 都会自动禁用吊销检查。

### 方案 3：在 Windows 证书存储中配置

1. 按 `Win + R`，输入 `certlm.msc` 打开本地计算机证书管理器
2. 找到 **受信任的根证书颁发机构** > **证书**
3. 找到你安装的 `goto-openai-root` 证书
4. 右键 > **属性**
5. 在 **证书策略** 选项卡中，禁用吊销检查相关选项

### 方案 4：使用 `-k` 参数（不推荐）

```powershell
curl -k https://api.openai.com/
```

这个参数会跳过所有证书验证，包括证书链和主机名验证，安全性较低。

## 推荐方案

**推荐使用方案 1 或方案 2**，因为它们只禁用吊销检查，仍然会验证证书的有效性，安全性较高。

## 其他注意事项

- 这个问题只在 Windows 系统上出现，因为 Linux 和 macOS 使用不同的 SSL/TLS 实现
- 如果你使用的是其他 HTTP 客户端（如 Postman、浏览器等），可能也会遇到类似问题，需要相应地配置
- 确保你的证书已经正确安装到系统的受信任根证书存储中