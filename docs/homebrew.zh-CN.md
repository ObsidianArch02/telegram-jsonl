# Homebrew 安装

[English](homebrew.md) | [简体中文](homebrew.zh-CN.md)

[返回 README](../README.zh-CN.md)

## 可用状态

项目与 Homebrew tap 共用
[ObsidianArch02/telegram-jsonl](https://github.com/ObsidianArch02/telegram-jsonl) 仓库。
下方命令要求仓库已发布的 `main` 分支包含经过审查的源码和 `Formula/telegram-jsonl.rb`。
仅创建仓库不会使工具可安装。初始安装采用 HEAD 源码构建，
稳定版安装还要求已采用稳定版 formula，并具备对应发布产物。

## 开发版安装

先安装 [Homebrew](https://brew.sh/)，在 macOS 或 Linux 运行：

```sh
brew tap obsidianarch02/telegram-jsonl https://github.com/ObsidianArch02/telegram-jsonl.git
brew install --HEAD obsidianarch02/telegram-jsonl/telegram-jsonl
telegram-jsonl --version
```

必须明确填写 tap URL，因为仓库名是 `telegram-jsonl`，而不是 `homebrew-telegram-jsonl`。
formula 位于 `Formula/telegram-jsonl.rb`。
采用稳定版 formula 前，初始 formula 只有 `HEAD` 源码定义，因此需要 `--HEAD`，构建的是仓库 `main` 分支，
不是固定发布版本。

formula 支持 macOS、Linux 的 Intel/AMD（`amd64`）与 ARM（`arm64`）。
Homebrew 会安装 Go 构建依赖，源码要求 [go.mod](../go.mod) 指定的 Go 版本，当前为 1.25 或更新版本。
安装结果是单一独立可执行文件，运行时无需 Go、额外 tdl、TDLib 或 SQLite 安装。

审查通过的源码更新发布后，更新开发版：

```sh
brew upgrade --fetch-HEAD obsidianarch02/telegram-jsonl/telegram-jsonl
```

`--fetch-HEAD` 让 Homebrew 检查上游开发源码，而非仅依赖本地 HEAD 修订。
更新不会导入旧 JSON 状态，请阅读[存储升级说明](usage.zh-CN.md#从-json-状态升级)。
账户数据应保存在自己的私密目录，和 Homebrew 的 Cellar 分开。

## 稳定版安装

稳定发布可提供生成的 formula，使用四个平台的 macOS/Linux 二进制包及已验证的 SHA-256。
维护者审查该 formula、提交，并批准上传到同一 tap 后，才可使用普通安装与更新：

```sh
brew update
brew install obsidianarch02/telegram-jsonl/telegram-jsonl
brew upgrade obsidianarch02/telegram-jsonl/telegram-jsonl
```

稳定版使用预编译可执行文件，不要求 Go。
已有 HEAD 安装切换为稳定版时，先运行
`brew uninstall obsidianarch02/telegram-jsonl/telegram-jsonl`，再执行上方稳定版安装命令。
账户数据应保留在软件包目录之外。
发布自动化将生成的 formula 作为 Release 附件提供，不自动替换 tap formula 或推送 `main`。
生成与审查流程见[发布文档](releasing.zh-CN.md#homebrew-formula)。

## 源码或 Windows

源码工作目录可在仓库根部使用 `go.mod` 指定的 Go 版本构建：

```sh
go build -trimpath -o telegram-jsonl .
./telegram-jsonl --version
```

源码发布后，可通过以下命令取得工作目录：

```sh
git clone https://github.com/ObsidianArch02/telegram-jsonl.git
cd telegram-jsonl
```

本地构建使用 `./telegram-jsonl` 运行，详细[使用例子](usage.zh-CN.md)采用 `PATH` 中的
`telegram-jsonl`，本地构建时请替换为 `./telegram-jsonl`。

此 Homebrew 安装方式不覆盖 Windows。标签版本发布后，从
[Releases](https://github.com/ObsidianArch02/telegram-jsonl/releases) 下载匹配的 Windows 包，
按 `SHA256SUMS` 校验后运行 `telegram-jsonl.exe`。
也可使用 `go build -trimpath -o telegram-jsonl.exe .` 从源码构建，按所用终端调整启动语法。
发布包覆盖 Windows `amd64` 和 `arm64`。
