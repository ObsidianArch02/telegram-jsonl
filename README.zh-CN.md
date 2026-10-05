# telegram-jsonl

[English](README.md) | [简体中文](README.zh-CN.md)

单账户 Telegram 命令行工具，在本地归档 JSONL，并按需下载附件。

本项目使用 AI 辅助生成，使用前请审查代码。

[使用文档](docs/usage.zh-CN.md) · [数据格式](docs/data-format.zh-CN.md) · [许可证](LICENSE)

## 安装

审查通过的源码和 formula 发布后，可在 macOS 或 Linux 上安装：

```sh
brew tap obsidianarch02/telegram-jsonl https://github.com/ObsidianArch02/telegram-jsonl.git
brew install --HEAD obsidianarch02/telegram-jsonl/telegram-jsonl
```

初始 formula 从已发布的 `main` 构建一个独立可执行文件，采用稳定版 formula 前需要 `--HEAD`，
无需额外 tdl 或 TDLib。
详见 [Homebrew、源码与 Windows 安装](docs/homebrew.zh-CN.md)。

## 快速开始

在一个终端运行归档命令，在手机 Telegram 的**设置 > 设备 > 连接桌面设备**中扫码，
授权已有账户。
支持**二维码登录**（默认，`--login-method qr`）和**手机号/验证码登录**
（`--login-method code`）。使用手机号和 Telegram 登录验证码时运行：

```sh
telegram-jsonl archive --data ./data-tdl --login --login-method code --history-days 7
```

两种方式在需要时都会请求两步验证密码，`fetch` 也接受相同登录参数。

```sh
# 归档近期消息，持续接收更新。
telegram-jsonl archive --data ./data-tdl --login --history-days 7

# 在另一终端搜索本地归档，不需要登录。
telegram-jsonl search --archive ./data-tdl/archive --pattern '(?i)\.pdf$' --limit 5

# 使用同一账户的独立会话下载匹配的 PDF。
telegram-jsonl fetch --archive ./data-tdl/archive --data ./fetch-data --login --pattern '(?i)\.pdf$' --limit 1
```

首次登录后保留各命令的数据目录，后续运行去掉 `--login`。
Windows 使用 `telegram-jsonl.exe`，并按所用终端调整启动语法。

## 子命令

| 命令 | 用途 |
| --- | --- |
| `archive` | 持续接收更新、补拉近期历史，维护 JSONL 快照。 |
| `search` | 用 Go 正则表达式搜索本地正文、文件名和结构化信息。 |
| `fetch` | 下载选中附件后退出，不改写 JSONL。 |

聊天消息继续保存在 JSONL，SQLite 将高频运行状态与低频会话属性、附件索引和下载记录分开保存，
详见[数据布局](docs/data-format.zh-CN.md)。

默认补拉最近 2 天，默认 JSONL 留存 7 天（`--retention-days auto`，比历史窗口多 5 天）。
`--history-days 0` 关闭历史补拉，`--history-days -1` 请求全量可访问历史。
默认活动 JSONL 容量上限为 1 GiB，使用 `--max-storage-bytes 0` 可关闭限制。
编辑和删除更新仍然处理。

未设置凭证环境变量时，客户端在源码层面集成经修改的 [tdl](https://github.com/iyear/tdl) 登录逻辑，
使用本项目配置的应用身份，用户无需自行申请 API 凭证。
Telegram 协议仍然需要应用身份，服务端也可能拒绝它。
归档器的 `--client auto` 在检测到凭证环境变量时选择 native 模式。
也可通过 `--client native` 使用自己的应用凭证，详见[配置](docs/usage.zh-CN.md#配置)。

## 使用边界

归档器保存小体积媒体信息，不保存附件文件。跳过自毁、受保护、受限制和付费媒体消息，
不归档秘密聊天与系统服务消息。

JSONL 表示当前归档状态，不是不可变事件日志。程序离线时不能同步远端删除。
`fetch` 保存的文件不会随之后的远端删除自动清理。登录会话是账户凭证，
归档目录和下载目录均需妥善保护。

本项目独立开发，与 Telegram 无隶属关系，也未获 tdl 背书。
官方接口和限速不保证账户安全或用途合规。使用前请阅读
[Telegram 相关免责声明](DISCLAIMER.zh-CN.md)。

## 文档

- [Homebrew、源码构建与 Windows 安装](docs/homebrew.zh-CN.md)
- [使用、登录与完整 PDF 下载例子](docs/usage.zh-CN.md)
- [JSONL 格式与非文本消息覆盖](docs/data-format.zh-CN.md)
- [消息生命周期与运行限制](docs/limitations.zh-CN.md)
- [安全问题报告](SECURITY.zh-CN.md)
- [第三方归属说明](THIRD_PARTY_NOTICES.zh-CN.md)

## 贡献

开发检查和贡献要求见 [CONTRIBUTING.zh-CN.md](CONTRIBUTING.zh-CN.md)。
真实账户集成尚未验证；本地测试使用模拟输入。

## 许可证

[GNU Affero General Public License v3.0](LICENSE)。上游归属与修改见
[THIRD_PARTY_NOTICES.zh-CN.md](THIRD_PARTY_NOTICES.zh-CN.md)。
