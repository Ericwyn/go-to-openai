@echo off
:: 检查管理员权限，如果没有则申请
%1 mshta vbscript:CreateObject("Shell.Application").ShellExecute("cmd.exe","/c %~s0 ::","","runas",1)(window.close)&&exit
cd /d "%~dp0"

:: 运行你的命令
.\go-to-openai auto

pause
