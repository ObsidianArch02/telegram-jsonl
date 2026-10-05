# 发布

[English](releasing.md) | [简体中文](releasing.zh-CN.md)

[返回贡献指南](../CONTRIBUTING.zh-CN.md)

## 维护者流程

向 GitHub 推送版本标签时触发发布，无需另外部署 webhook 服务。
工作流测试标签对应源码，交叉编译独立可执行文件，打包产物，
并用仓库范围的令牌创建 GitHub Release。

在 `dev/<topic>` 分支开发，每完成并验证一个原子修改就创建本地提交。
准备上传时，将未发布提交 rebase 到已核对远端的最新 `main`，解决冲突并运行受影响的检查。
下方准备命令假设已确认远端名为 `origin`，当前分支是尚未发布的开发分支：

```fish
git fetch origin
git rebase origin/main
go test -race ./...
go vet ./...
git status --short
git log origin/main..HEAD --oneline
```

向维护者展示 rebase 后的提交、准确变更、检查结果、上传目标，以及源码和历史中的应用凭证。
获明确上传批准后才能 push。允许 rebase 和本地提交不代表允许发布，
改写已发布历史或强制推送需单独授权。

批准后，向约定目标上传已审查的源码。只有发版及标签也获明确请求和批准时，才能创建并推送发布标签。
选择尚未发布的版本。下方示例假设当前 HEAD 是已审查的发布提交，不代表该版本已经发布：

```fish
git tag v0.1.0
git push origin v0.1.0
```

稳定标签采用 `vMAJOR.MINOR.PATCH`，`v0.1.0-rc.1` 等后缀版本发布为 prerelease。
工作流发布前会验证版本格式。
项目需先托管到 GitHub，启用 Actions，且仓库策略允许发布写入。
工作目录含未提交的发布修改时不要推送标签；标签标识的是已提交源码。

## 产物

发布矩阵覆盖 Linux、macOS、Windows 的 `amd64` 与 `arm64`。
Unix 包为 `.tar.gz`，Windows 包为 `.zip`，`SHA256SUMS` 记录六个二进制包的校验和。
包中包含可执行文件、项目文档、许可证与上游/依赖声明。
GitHub 会自动为标签提供源码归档，工作流不会再将重复源码包作为 Release 附件上传。

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
需在仓库根目录运行，准备好 Go 工具链，以及与 `BUILD_COMMIT` 和 `HEAD` 一致的干净已提交源码。
二进制和根目录文档来自工作目录，`docs/` 和 `licenses/` 来自 `HEAD`；
未提交的本地修改会导致同一个发布包中的内容不一致。

## 失败处理

检查失败任务，在再次发布前修复源码或仓库策略。
不要用已经发布的版本号指向不同源码。删除或替换公开标签会破坏来源可追踪性，应优先发布新版本。
构建和测试不需要 Telegram 凭证，不要把它们存入 Actions secrets。

## Homebrew Formula

Homebrew formula 应位于本仓库的 `Formula/` 目录，不作为 Release 附件上传。
稳定版使用四个 macOS/Linux 二进制产物；Windows 继续使用自己的发布包。
预发布标签不生成稳定版 formula。发布产物存在后，在本地生成候选 formula，
审查后将采用的版本提交到 tap。

本地生成时，将四个真实发布包与 `SHA256SUMS` 放入 `./dist`，
填写已有的实际发布标签。下方 `v0.1.0` 仅为例子：

```sh
go run ./scripts/homebrew \
  --repository ObsidianArch02/telegram-jsonl \
  --tag v0.1.0 \
  --checksums ./dist/SHA256SUMS \
  --assets ./dist \
  --output ./Formula/telegram-jsonl.rb
```

formula 的二进制 URL 指向该标签已有的发布产物，同时保留 `HEAD` 源码构建分支。
审查生成结果后，在 `dev/<topic>` 分支替换 `Formula/telegram-jsonl.rb`。
检查仓库 URL、目标平台、包名和校验和，然后提交完整更新。
在同一个原子采用提交中，移除两份 README 安装命令的 `--HEAD`，改用普通 `brew install`。
将未发布修改 rebase 到最新 `main`，按上文要求为准确的上传内容取得批准。
CI 不自动向 tap 提交、更新 `main`、推送分支或创建 Pull Request。

稳定标签成功发布并采用新 formula 后，用户通过以下命令更新稳定版：

```sh
brew update
brew upgrade obsidianarch02/telegram-jsonl/telegram-jsonl
```

采用稳定版 formula 之前，初始 formula 需要 `--HEAD`。
[Homebrew 安装](homebrew.zh-CN.md)包含同仓库 tap 的明确 URL，以及开发版与稳定版命令。
安装要求已发布的 `main` 包含审查通过的源码和 formula，发布这些内容仍需获批准。
