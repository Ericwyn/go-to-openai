# Windows 11 + PowerShell 启动方案

## 问题描述

在 Windows 11 上使用普通 PowerShell 执行 go-to-openai 时，可能会遇到以下权限问题：

1. **证书安装/删除失败**：无法将证书安装到系统信任存储
2. **端口监听失败**：无法绑定到 443 端口（需要管理员权限）

## 解决方案

### 1. 以管理员身份运行 PowerShell

Windows 11 上需要以管理员身份运行 PowerShell 才能执行需要系统权限的操作。

#### 方法 1：从开始菜单打开

1. 点击 **开始** 按钮
2. 搜索 **PowerShell**
3. 右键点击 **Windows PowerShell** 或 **PowerShell**
4. 选择 **以管理员身份运行**

#### 方法 2：使用快捷键

1. 按 `Win + X` 打开快速访问菜单
2. 选择 **Windows PowerShell (管理员)** 或 **终端 (管理员)**

### 2. 完整启动步骤

#### 步骤 1：编译项目

```powershell
# 进入项目目录
cd d:\Chaos\go\go-to-openai

# 编译项目
go build -o go-to-openai .
```

#### 步骤 2：生成根证书

```powershell
# 生成根 CA 证书
./go-to-openai root-crt-gen
```

生成的文件：
- `cert/goto-openai-root.crt` - 根 CA 证书
- `cert/goto-openai-root.key` - 根 CA 私钥

#### 步骤 3：安装根证书（需要管理员权限）

```powershell
# 安装根 CA 证书到系统信任存储
./go-to-openai root-crt-install
```

系统会弹出 UAC 提示，点击 **是** 确认。

#### 步骤 4：生成域名证书

为需要代理的域名生成证书：

```powershell
# 生成 api.openai.com 证书
./go-to-openai domain-crt-gen "api.openai.com"

# 生成 api.anthropic.com 证书（如果需要）
./go-to-openai domain-crt-gen "api.anthropic.com"
```

生成的文件：
- `cert/goto-openai-dm-api.openai.com.crt` - 域名证书
- `cert/goto-openai-dm-api.openai.com.key` - 域名私钥

#### 步骤 5：配置 hosts 文件（需要管理员权限）

1. 以管理员身份打开记事本：
   - 按 `Win + X`，选择 **记事本 (管理员)**
   - 或搜索 **记事本**，右键选择 **以管理员身份运行**

2. 打开 hosts 文件：
   - 文件 > 打开
   - 导航到 `C:\Windows\System32\drivers\etc`
   - 选择 **所有文件 (*.*)** 作为文件类型
   - 选择 `hosts` 文件并打开

3. 添加以下内容：
   ```
   127.0.0.1 api.openai.com
   127.0.0.1 api.anthropic.com
   ```

4. 保存文件

#### 步骤 6：启动代理服务（需要管理员权限）

```powershell
# 启动代理服务
./go-to-openai run -config="./config.json"
```

服务会在 `127.0.0.1:443` 上启动。

### 3. 常见权限问题及解决方案

#### 问题 1：无法安装证书

**错误信息**：
```
install certificate: exit status 1 (try running as Administrator)
```

**解决方案**：确保以管理员身份运行 PowerShell。

#### 问题 2：无法监听 443 端口

**错误信息**：
```
listen tcp 127.0.0.1:443: bind: permission denied
```

**解决方案**：
1. 以管理员身份运行 PowerShell
2. 确保 443 端口没有被其他服务占用

#### 问题 3：curl 测试时证书吊销检查错误

**错误信息**：
```
curl: (35) schannel: next InitializeSecurityContext failed: CRYPT_E_NO_REVOCATION_CHECK (0x80092012)
```

**解决方案**：
```powershell
# 临时解决
curl --ssl-no-revoke https://api.openai.com/

# 或设置环境变量
$env:CURL_SSL_NO_REVOKE = "1"
```

### 4. 端口占用检查

如果 443 端口被占用，可以使用以下命令查看：

```powershell
# 查看 443 端口占用情况
netstat -ano | findstr :443

# 查看进程详情
Get-Process -Id <PID>
```

### 5. 防火墙配置

如果遇到连接问题，可能需要配置防火墙：

1. 打开 **Windows 安全中心**
2. 选择 **防火墙和网络保护**
3. 选择 **高级设置**
4. 添加入站规则，允许 443 端口的 TCP 连接

### 6. 完整测试流程

以管理员身份运行 PowerShell：

```powershell
# 测试服务是否正常运行
curl --ssl-no-revoke https://api.openai.com/

# 测试健康检查
curl --ssl-no-revoke https://api.openai.com/healthz

# 测试 API 调用（示例）
curl --ssl-no-revoke --location 'https://api.openai.com/v1/chat/completions' `
  --header 'Authorization: Bearer <YOUR_API_KEY>' `
  --header 'Content-Type: application/json' `
  --data-raw '{
    "messages": [
      {
        "role": "user",
        "content": "Hello"
      }
    ],
    "model": "gpt-4"
  }'
```

### 7. 卸载证书（需要管理员权限）

如果需要卸载证书：

```powershell
# 卸载根 CA 证书
./go-to-openai root-crt-remove
```

## 注意事项

1. **管理员权限**：所有涉及证书安装和 443 端口监听的操作都需要管理员权限
2. **UAC 提示**：执行证书安装时会弹出 UAC 提示，需要点击确认
3. **防火墙**：确保防火墙允许 443 端口的入站连接
4. **端口占用**：确保 443 端口没有被其他服务占用
5. **证书吊销检查**：Windows 上 curl 测试时需要使用 `--ssl-no-revoke` 参数

## 故障排查

如果遇到问题：

1. 确认是否以管理员身份运行 PowerShell
2. 检查 hosts 文件是否正确配置
3. 检查 443 端口是否被占用
4. 检查证书是否正确安装
5. 查看服务启动日志，确认是否有其他错误

## 总结

Windows 11 上运行 go-to-openai 需要注意以下几点：

1. 始终以管理员身份运行 PowerShell
2. 正确配置 hosts 文件
3. 确保 443 端口可用
4. 测试时使用 `--ssl-no-revoke` 参数避免证书吊销检查错误

按照上述步骤操作，应该能够顺利在 Windows 11 上启动和使用 go-to-openai 代理服务。