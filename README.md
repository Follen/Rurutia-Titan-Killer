# Rurutia Titan Killer

[![Windows Release](https://github.com/Follen/Rurutia-Titan-Killer/actions/workflows/release.yml/badge.svg)](https://github.com/Follen/Rurutia-Titan-Killer/actions/workflows/release.yml)

一个面向 Windows 的魔兽世界时光服退出残留进程守护工具，使用 Wails 2、Go 和原生 Windows API 构建。

## 功能

- 每 15 秒扫描 `WowClassic.exe`
- 只处理 `_classic_titan_` 客户端目录中的进程
- 仅清理退出码已结束且只剩一个线程的残留实例
- 保留仍处于 `STILL_ACTIVE` 状态的正常游戏实例
- 启动守护时清理已有残留，之后持续自动清理
- 持久化记录清理时间、PID、线程数和释放内存
- 无边框窗口、自绘窗口控制与系统托盘
- 关闭窗口时隐藏到托盘，托盘右键菜单可完全退出

## 环境

- Windows 10 或 Windows 11
- Go 1.23 或更高版本
- Node.js 18 或更高版本
- Wails CLI 2.12

## 开发

```powershell
wails dev
```

## 构建

```powershell
wails build
```

构建结果位于 `build/bin/露露时光服残留专杀.exe`。

推送形如 `0.0.1` 的版本 tag 后，GitHub Actions 会自动构建 Windows EXE 并创建对应 Release。

## 判定规则

清理前会再次核对进程路径、PID、创建时间、退出码和线程数量。退出码为 `259 (STILL_ACTIVE)` 的实例不会进入清理流程。
