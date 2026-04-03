# Auto 模式根证书一致性改进计划

## 问题概述

当前 `autostart` 模式启动失败的根本原因：**域名证书的签发链与系统信任的根证书不一致**。

具体表现：

1. `OneKeyStart()` 只检查 `cert/` 目录是否有根证书文件，不检查系统是否已安装
2. 每次 `GenerateRootCert()` 都会重新生成新的 RSA 密钥对，可能导致与系统已安装的旧版本不匹配
3. 域名证书使用 `cert/` 目录的根证书签发，但系统可能信任的是另一个版本的根证书

## 改进方案

### 核心原则

**以** **`cert/`** **目录的根证书为事实来源**（因为必须有私钥才能签发域名证书），确保系统信任存储与该根证书一致。

### 实现步骤

#### 第一步：新增系统根证书检查功能

在 `certmanager/` 包中新增跨平台函数：

1. `IsRootCAInstalled(certPath string) (bool, error)` — 检查系统信任存储是否已安装指定根证书
   - Windows: 使用 `certutil -store Root` 查找匹配的证书指纹
   - macOS: 使用 `security find-certificate` 查找 CN 匹配的证书
   - Linux: 使用 `openssl` 或检查系统证书目录
2. `CompareRootCerts(certPath1, certPath2 string) (bool, error)` — 比较两个证书是否匹配（比较公钥指纹）

#### 第二步：新增本地与系统根证书同步功能

在 `auto/` 包中新增同步逻辑：

1. `SyncRootCertToSystem() error` — 同步根的证书到系统
   - 如果 `cert/` 目录有根证书，但系统未安装 → 安装到系统
   - 如果 `cert/` 目录有根证书，系统已安装但不匹配 → 先移除旧的，再安装新的
   - 如果 `cert/` 目录没有根证书 → 生成新并安装到系统
   * 如果都有且匹配 → 跳过
2. `GetSystemRootCert() ([]byte, error)` — 从系统导出已安装的根证书（用于诊断）

#### 第三步：重构 OneKeyStart 流程

修改 `auto/onestart.go` 的 `OneKeyStart()` 流程：

```
[1/5] 读取配置文件
[2/5] 检查/同步根证书到系统  ← 替换原来的"检查/生成根证书"
  - 检查 cert/ 目录是否有根证书
  - 检查系统是否已安装匹配的根证书
  - 如果不一致，同步到系统
[3/5] 生成域名证书
  - 使用 cert/ 目录的根证书签发（确保与系统信任的一致）
[4/5] 配置 hosts
[5/5] 启动代理服务器
```

#### 第四步：增强配置检查功能

修改 `auto/check.go` 的 `checkRootCert()` 函数，输出详细诊断信息：

```
[✓] 根证书 (cert目录): 已存在
[✓] 根证书 (系统信任): 已安装
[✓] 根证书匹配: cert目录与系统信任的根证书一致
[✓] 域名证书签发者: 与系统根证书匹配
```

如果不匹配，显示差异：

```
[✗] 根证书 (系统信任): 未安装
[✗] 根证书匹配: cert目录与系统信任的根证书不一致
  - cert目录证书指纹: SHA256:xxx
  - 系统证书指纹: SHA256:yyy (或"未找到")
```

#### 第五步：新增域名证书签发者检查

在 `auto/domain_cert.go` 中新增：

1. `VerifyDomainCertSignedByRoot(domain string) (bool, error)` — 验证域名证书是否由 `cert/` 目录的根证书签发
2. `VerifyDomainCertTrustedBySystem(domain string) (bool, error)` — 验证域名证书是否能被系统信任链验证

## 文件变更清单

| 文件                           | 变更类型 | 说明                                          |
| ---------------------------- | ---- | ------------------------------------------- |
| `certmanager/certmanager.go` | 新增函数 | `IsRootCAInstalled()`, `CompareRootCerts()` |
| `certmanager/install.go`     | 可能修改 | 支持证书更新场景                                    |
| `certmanager/remove.go`      | 无变更  | 现有功能已足够                                     |
| `auto/root_cert.go`          | 新增函数 | `SyncRootCertToSystem()`                    |
| `auto/domain_cert.go`        | 新增函数 | `VerifyDomainCertSignedByRoot()`            |
| `auto/onestart.go`           | 修改   | 重构证书检查/同步流程                                 |
| `auto/check.go`              | 修改   | 增强根证书检查输出                                   |

## 风险点

1. **Windows certutil 输出解析**：不同 Windows 版本输出格式可能不同，需要健壮的正则匹配
2. **macOS security 命令权限**：可能需要 sudo，但检查操作应该不需要
3. **Linux 发行版差异**：需要处理多种证书存储位置
