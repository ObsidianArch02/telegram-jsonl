# 贡献指南

[English](CONTRIBUTING.md) | [简体中文](CONTRIBUTING.zh-CN.md)

## 开发

使用 [go.mod](go.mod) 指定的 Go 版本。项目构建为单一可执行文件，
集成的 tdl 派生登录代码不得依赖额外运行时二进制。

```sh
go build -trimpath -o telegram-jsonl .
go test -race ./...
go vet ./...
```

修改 Go 文件后运行 `gofmt`。自动化测试不得要求 Telegram 凭证或连接真实账户，
生命周期、归档、迁移和下载测试使用临时目录与模拟 RPC 响应。

## 修改要求

保持修改范围集中，说明用户可见行为和相应验证。
修改账户绑定、会话隔离、删除、保护、历史窗口或附件检查时，应增加回归覆盖。
`archive` 保持为唯一 JSONL 写入方，`search` 与 `fetch` 必须只读消费归档。

不得提交账户会话、登录令牌、应用秘密凭证、聊天归档、已下载附件或包含个人内容的日志。
检查实际暂存差异；`.gitignore` 无法保护已经被 Git 跟踪的秘密。
问题报告和文档应使用合成示例。

英文是默认文档语言。修改用户文档时同步对应 `.zh-CN.md` 页面，并保留顶部语言切换。
README 聚焦安装与首次使用，详细配置、边界情况和例子放入 `docs/`。

## 提交与 Pull Request

采用 conventional commit 标题，例如 `fix(fetch): reject replaced attachments`
或 `docs: explain session migration`。有助于审查时，在正文解释原因。
无关修改分别提交，说明真正执行过的检查。

提交信息只使用 ASCII 英文，标题不超过 72 个字符且不以英文句号结尾。
标题、正文和脚注之间留空行。破坏性变更需同时在标题使用 `!`，并添加
`BREAKING CHANGE:` 脚注说明变更内容与迁移方式。每个提交应能够独立编译。

不能将模拟测试或离线客户端检查描述成真实账户验证。
获授权集成测试需说明覆盖范围，但不要附带凭证、账户标识或会话内容。

## 许可证

贡献使用项目的 [AGPL v3.0 许可证](LICENSE)。
仅提交你有权贡献的代码和文档，保留上游声明，并标明第三方派生代码的修改。
详见 [THIRD_PARTY_NOTICES.zh-CN.md](THIRD_PARTY_NOTICES.zh-CN.md)。

更新依赖时，重新收集实际链接模块的许可证声明：

```sh
go run ./scripts/licenses --output licenses
```

检查并随依赖变更一起提交[版本清单](licenses/inventory.json)和许可证原文。
CI 会独立重新生成并比较结果。移除依赖时，也要清理已不再使用的生成声明目录。

## 发布

标签触发的工作流构建并发布多平台产物，维护者操作见
[docs/releasing.zh-CN.md](docs/releasing.zh-CN.md)。
安全问题报告方式见 [SECURITY.zh-CN.md](SECURITY.zh-CN.md)。
