# 第三方归属说明

[English](THIRD_PARTY_NOTICES.md) | [简体中文](THIRD_PARTY_NOTICES.zh-CN.md)

## tdl

集成应用身份、二维码/验证码登录流程和旧 Bolt 会话布局派生自
[iyear/tdl](https://github.com/iyear/tdl)，由 iyear 及其贡献者维护，
采用 GNU Affero General Public License v3.0。
引用版本为 `v0.20.4`，固定提交为
`9d7d49eef2bcb04da720c26e33598c49c68b9ddd`。

相关上游文件为 `app/login/qr.go`、`app/login/code.go`、`pkg/tclient/app.go`、
`pkg/kv/bolt.go` 及其会话存储辅助代码，见[固定提交源码](https://github.com/iyear/tdl/tree/9d7d49eef2bcb04da720c26e33598c49c68b9ddd)。

本项目修改集成方式，编译为一个 Go 可执行文件，二维码/验证码登录采用 tdl 自有内置应用身份，
本地保存稳定会话，隔离归档/下载授权，接入已有归档器，并在不修改原文件的情况下迁移旧原型 Bolt 状态。
不分发或运行旧 tdl 扩展宿主。这些修改由本项目维护，不属于上游 tdl 官方修改。
派生源码包含归属注释，[LICENSE](LICENSE) 提供 AGPL 全文。

## Go 依赖

依赖版本记录在 [go.mod](go.mod) 与 [go.sum](go.sum)，
各依赖保留自己的许可证和版权声明，包括负责 MTProto 与 Telegram API 的
[gotd/td](https://github.com/gotd/td)。
发布打包按要求附带依赖声明与许可证，再分发二进制或源码时应保留。

## 再分发

[许可证清单](licenses/inventory.json)记录六个平台实际链接模块的许可证文件与
SHA-256，用于核对随包提供的原文。

组合后的项目采用 [AGPL v3.0](LICENSE)，应保留归属、修改声明及适用依赖许可证。
分发二进制需按许可证提供对应源码，发布中的 Git 跟踪源码包用于标识本项目对应源码。
依赖模块继续通过固定版本和上游声明标识。
若修改程序并向远端用户提供网络交互，请核对 AGPL 第 13 条的对应源码要求。

项目许可证不授予 Telegram 服务访问、商标或用户内容的权利，见
[DISCLAIMER.zh-CN.md](DISCLAIMER.zh-CN.md)。
