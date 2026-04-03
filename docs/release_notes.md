# Release Notes - V1.1.0

## 新功能

### Auto 交互式命令行模式
- 新增 `auto` 子命令，提供菜单驱动的交互式操作
- 支持数字选择快速完成证书管理、配置生成和代理启动
- 新增 `autostart` 非交互式一键启动命令

### Hosts 文件管理
- 新增 `hosts-setup` 和 `hosts-remove` 子命令
- 新增 `hostsmanager` 包，实现跨平台 hosts 文件读写
- 支持自动将域名指向 `127.0.0.1`，并可通过标记清理

### 证书管理增强
- 新增 `SyncRootCertToSystem` 函数，确保本地根证书与系统信任存储一致
- 新增跨平台证书验证功能：`IsRootCAInstalled` 和 `CompareRootCerts`
- 新增域名证书签发验证功能：`VerifyDomainCertSignedByRoot`
- 重构 `OneKeyStart` 流程，自动同步根证书到系统

### 启动脚本
- 新增 Windows 启动脚本：`run-auto-win.bat`、`run-autostart-win.bat`
- 新增 Linux/macOS 启动脚本：`run-auto.sh`、`run-autostart.sh`
- 所有脚本均包含权限检查

## 改进

### CI/CD 构建流程
- 构建时自动打包对应平台的启动脚本
- 统一使用 zip 格式打包，目录结构清晰
- 修复二进制文件与打包目录同名冲突问题

### 文档
- 新增 Auto 模式使用说明
- 新增 Windows 11 PowerShell 启动方案
- 新增 Windows 证书吊销状态检查
- 完善恢复原样（清理环境）说明
- 更新 README，添加常见 QA 部分

## 变更统计
- 27 个文件变更
- 新增 2293 行，删除 55 行
- 新增 `auto/` 包（交互式命令行）
- 新增 `hostsmanager/` 包（跨平台 hosts 管理）
