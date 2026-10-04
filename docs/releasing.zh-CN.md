# 发布

[English](releasing.md) | [简体中文](releasing.zh-CN.md)

[返回贡献指南](../CONTRIBUTING.zh-CN.md)

## 维护者流程

向 GitHub 推送版本标签时触发发布，无需另外部署 webhook 服务。
工作流测试标签对应源码，交叉编译独立可执行文件，打包产物，
并用仓库范围的令牌创建 GitHub Release。

先审查并提交待发布源码，选取尚未发布的版本。
下方版本只是例子，不代表该版本已经发布：

```sh
go test -race ./...
go vet ./...
git status --short
git tag v0.1.0
git push origin v0.1.0
```

稳定标签采用 `vMAJOR.MINOR.PATCH`，`v0.1.0-rc.1` 等后缀版本发布为 prerelease。
工作流发布前会验证版本格式。
项目需先托管到 GitHub，启用 Actions，且仓库策略允许发布写入。
工作目录含未提交的发布修改时不要推送标签；标签标识的是已提交源码。

## 产物

发布矩阵覆盖 Linux、macOS、Windows 的 `amd64` 与 `arm64`。
Unix 包为 `.tar.gz`，Windows 包为 `.zip`，`SHA256SUMS` 记录包校验和。
包中包含可执行文件、项目文档、许可证与上游/依赖声明。
源码包从标签对应 Git 树生成，不打包工作目录，因此排除被忽略的本地账户状态。

校验和能检测意外改动，但不是独立的发布者签名。
交叉编译验证构建，不证明真实账户行为，也不代表在每个目标平台都执行过二进制。

## 本地打包

`scripts/release.sh` 可构建指定目标，不发布到 GitHub。fish 示例：

```fish
set -gx RELEASE_TAG v0.1.0
set -gx BUILD_COMMIT (git rev-parse HEAD)
set -gx GOOS linux
set -gx GOARCH amd64
bash scripts/release.sh build
```

脚本禁用 CGO，并使用指定版本和提交信息。
需在仓库根目录运行，准备好 Go 工具链和准确源码，打包会包含项目声明。

## 失败处理

检查失败任务，在再次发布前修复源码或仓库策略。
不要用已经发布的版本号指向不同源码。删除或替换公开标签会破坏来源可追踪性，应优先发布新版本。
构建和测试不需要 Telegram 凭证，不要把它们存入 Actions secrets。
