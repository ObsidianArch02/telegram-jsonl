# 使用文档

[English](usage.md) | [简体中文](usage.zh-CN.md)

[返回 README](../README.zh-CN.md)

例子使用通过 Homebrew 等方式安装到 `PATH` 的 `telegram-jsonl`。
本地源码构建或解压 Release 后，改用 `./telegram-jsonl`；
Windows 使用 `telegram-jsonl.exe`，按所用终端调整启动语法。

## 登录与会话

`archive` 与 `fetch` 是同一个可执行文件内的两个独立组件，各自保存账户授权，
可以并行运行。两者必须登录同一个 Telegram 账户。数据目录绑定一个账户和组件，
账户或会话命名空间不匹配时命令停止。

```sh
telegram-jsonl archive --data ./data-tdl --check-client
telegram-jsonl archive --data ./data-tdl --login
```

`--check-client` 只在本地检查集成客户端，不连接 Telegram。
二维码登录在手机 **Telegram > 设置 > 设备 > 连接桌面设备**中扫码，请核对手机上的设备授权。
二维码包含短期登录令牌，不要分享终端录屏。`--login-method code` 使用手机号和验证码，
需要时还会请求两步验证密码。输入和粘贴的每个字符均显示为 `*`，退格删除最后一个字符，
Ctrl-U 清空输入，Enter 提交。

登录后复用同一数据目录，去掉 `--login`。即使带上 `--login`，已有有效会话也会复用。
只有确实要替换 tdl 授权时才用 `--login --tdl-relogin`。程序不注册新账户，不自动重试登录。
Ctrl-C 仅停止进程，不主动登出账户。

### 从 JSON 状态升级

此存储版本不导入旧版 JSON 元数据或运行状态。
已有导出、会话、下载文件和旧状态文件会保留，但不读取旧 JSON 状态。
旧归档缺少新 SQLite 元数据时，不能仅凭 JSONL 文件重新打开完整状态。
请使用新目录，并明确登录：

```sh
telegram-jsonl archive --data ./data-sqlite --login --history-days 7
telegram-jsonl fetch --archive ./data-sqlite/archive --data ./fetch-sqlite --login --pattern '(?i)\.pdf$' --limit 1
```

不要覆盖或删除旧数据目录来强行升级。
[数据格式](data-format.zh-CN.md)说明 SQLite 布局和查询例子。
如果需要把已有 schema 1 目录一次性转换为新格式，可使用独立脚本
`/tmp/telegram-jsonl-migration-20261004/migrate.py`。先停止归档器和下载器，运行 `--dry-run`，
再转换到全新的目标目录；源目录保持不变。锁检查、包含 WAL 的快照、会话保留和校验限制见同目录 `usage.txt`。

## 运行日志

运行日志使用标准库的结构化文本日志器并写入 stderr，时间戳使用电脑本地时区。可在进程环境中设置
`TZ=Asia/Shanghai` 指定时区。日志中的历史起点、下次同步时间和 FLOOD_WAIT
截止时间也使用该时区；JSONL 日期和 stdout 的 JSON 输出仍保持 UTC。

收到信号时会明确记录正常停止；客户端在没有信号时意外返回则记录为错误，不会伪装成同步完成。
交互式终端会为 `INFO`、`WARN` 和 `ERROR` 级别标签着色；重定向到文件或管道时保持纯文本，
也可以设置 `NO_COLOR=1` 禁用颜色。

日志包含授权状态、会话列表扫描、历史分页、编辑与删除核对、成功落盘的 JSONL
变更及附件处理结果。归档进程每分钟报告一次已保存会话和消息数量，下载过程中
最多每五秒报告一次字节进度。日志不包含消息正文、说明文字、密码、登录令牌或
应用凭证，但会话和消息标识仍属于个人元数据。下载跳过或失败的原因见 stdout JSON。

## 配置

未设置凭证环境变量时，归档器默认的 `--client auto` 选择 tdl。
只要存在 `TG_API_ID` 或 `TG_API_HASH` 任意一个，便选择 native，且要求两者都有效。
下载器默认 `--client tdl`，使用自有凭证下载时需明确指定 `--client native`。
tdl 模式在源码层面移植登录集成，使用本项目配置的应用身份，不启动辅助进程。
Telegram 仍可能拒绝该应用身份，无法保证账户或 API 访问可用。

若使用自己的应用，在 [my.telegram.org](https://my.telegram.org) 申请凭证。
下面的环境变量命令适用于 **fish**；使用其他终端时请改用对应的环境变量导出语法：

```fish
set -gx TG_API_ID 'YOUR_APP_ID'
set -gx TG_API_HASH 'YOUR_APP_HASH'
telegram-jsonl archive --client native --data ./data-native --login
```

贡献者个人的凭证应使用私密环境变量配置，不要放进源码、Issue、终端录屏或提交。
修改项目默认应用身份需维护者明确授权，任何公开上传前都应审查源码和 Git 历史中的凭证。

| 归档参数 | 默认值 | 含义 |
| --- | --- | --- |
| `--data` | `./data-tdl` | 账户和会话状态，以及 `archive/` 目录。 |
| `--client` | `auto` | 按环境变量选择，或明确指定 `tdl`/`native`。 |
| `--login` | `false` | 尚未授权时允许交互登录。 |
| `--login-method` | `qr` | 登录方式，`qr` 或 `code`。 |
| `--tdl-relogin` | `false` | 与 `--login` 一起明确替换 tdl 授权。 |
| `--check-client` | `false` | 本地检查集成客户端，不发起连接。 |
| `--proxy` | 空 | 两种后端均支持 SOCKS5 代理地址。 |
| `--interval` | `2s` | 业务 RPC 最小启动间隔，最低 `1s`。 |
| `--batch` | `50` | 每次请求的消息或会话数，范围 `1` 至 `100`。 |
| `--sync-every` | `6h` | 定期历史和删除核对，最低 `10m`。 |
| `--reconcile-window` | `1h` | 编辑/删除核对窗口，默认回看一小时；可用时长或 `all`。 |
| `--history-days` | `30` | 历史窗口；`0` 关闭，`-1` 请求全量。 |
| `--history-since` | 空 | UTC 起始日期或 RFC3339 时间。 |

`telegram-jsonl archive --help`、`search --help`、`fetch --help`
列出所用版本实际接受的参数。不带子命令时默认运行 `archive`。

### 历史窗口

```sh
telegram-jsonl archive --data ./data-tdl --history-days 7
telegram-jsonl archive --data ./data-tdl --history-days 0
telegram-jsonl archive --data ./data-tdl --history-since 2026-09-01
```

窗口在进程启动时固定，包含恰好在起点的消息。`YYYY-MM-DD` 按 UTC 零点解释，
需要明确时区时用 RFC3339。不能同时显式指定 `--history-days` 和 `--history-since`。
分页到达更早消息后停止，边界请求可能返回早期记录，但不会将它们保存。
窗口内不设总条数上限。扩大窗口会重置此前的历史扫描进度，缩小窗口不会清除已有记录。

`archive` 是常驻服务。历史窗口只限制历史分页，不会关闭实时更新、差分恢复或定期编辑/删除核对。
程序没有一次性归档模式。

该设置限制历史分页，不是保留期限。实时更新和差分恢复仍会运行，也可能带回更早的消息。
已有记录继续核对编辑和删除。`FLOOD_WAIT` 等待截止时间会持久化，重启不会绕过等待。

`--reconcile-window` 限制已有消息的编辑/删除核对范围，默认从每个同步周期开始时间向前回看一小时。
周期起点和会话位置会保存到运行时 SQLite，因此重启会继续同一个窗口，不会静默改变边界。
使用 `--reconcile-window all` 核对所有保留消息。窗口缩短后，较早的远端编辑或删除可能不会被发现，
直到再次使用更大的窗口。

## 搜索并下载一份 PDF

假设归档器使用 `--data ./data-tdl` 持续运行，你要下载收到的 `invoice.pdf`。
在可执行文件所在目录另开一个终端即可。

### 1. 预览本地匹配

```sh
telegram-jsonl search --archive ./data-tdl/archive --pattern '(?i)\.pdf$' --limit 5
```

这一步完全离线，不会下载文件。`(?i)` 表示忽略大小写，`\.pdf$` 匹配 `.pdf` 结尾的文件名。
结果每行一个 JSON 对象。下面展开并省略部分字段以便阅读：

```json
{
  "peer": "user-7",
  "message_id": 456,
  "text": "本月账单",
  "media": {
    "kind": "document",
    "file": {"id": "9001", "name": "invoice.pdf", "mime_type": "application/pdf", "size_bytes": 18024}
  }
}
```

`peer` 是会话标识，`media.file.name` 是收到的原文件名。
这些字段描述附件，不代表文件已经下载。

### 2. 下载选中的匹配

将 `user-7` 替换为预览结果中的真实会话：

```sh
telegram-jsonl fetch \
  --archive ./data-tdl/archive \
  --data ./fetch-data \
  --peer user-7 \
  --pattern '^invoice\.pdf$' \
  --limit 1 \
  --output ./attachments \
  --login
```

| 参数 | 本例效果 |
| --- | --- |
| `--archive` | 读取归档器的 JSONL 匹配，不改写它。 |
| `--data ./fetch-data` | 保存下载器自己的独立会话。 |
| `--peer user-7` | 只搜索选定会话。 |
| `--pattern` | 匹配文件名；同一正则也会检查其他字段。 |
| `--limit 1` | 只处理最新一条匹配消息。 |
| `--output` | 将附件保存到 `./attachments`。 |
| `--login` | 允许下载器首次登录同一账户。 |

即使归档器已登录，下载器仍需要独立的首次授权。
之后复用 `./fetch-data`，去掉 `--login`。
下载器状态和输出目录必须与归档器状态及 JSONL 目录分开，程序会检查该关系，包括解析符号链接。

### 3. 查看结果

```json
{"peer":"user-7","message_id":456,"status":"downloaded","path":"/your/project/attachments/user-7-456-document-9001.pdf","size_bytes":18024,"sha256":"..."}
```

`path` 是本地文件的绝对路径，`/your/project` 仅为示例。
文件名使用会话、消息和附件 ID，加上媒体类型，不采用发送者提供的路径。
成功结果包含字节数和 SHA-256。`skipped` 附带原因，例如已删除、受保护、已替换或超过大小限制。
`error` 表示传输或写入失败。处理完匹配后命令退出，不推进归档游标，也不修改 JSONL。
`downloaded` 表示文件已保存，并已记录在 `archive/index.sqlite` 的 `files` 表。
下载器仅在共享索引中写下载记录，其他元数据由归档器维护。
下载器自己的客户端绑定与等待状态写入 `fetch-data/state.sqlite`。

若文件已保存但 SQLite 下载记录写入失败，结果为 `error`，仍包含已保存的 `path`、
大小、SHA-256 和指出索引写入失败的原因。文件会保留，但没有确认成功的数据库记录。
请先检查该路径并解决数据库错误，再决定是否重新下载。

再次下载同一个未变化附件时，会替换同一路径的本地文件。
批量下载时先预览相同条件，再增加 `--limit`。
保存后的文件是主动留存的本地副本，没有进程监测并同步之后的远端删除，详见[生命周期限制](limitations.zh-CN.md)。

## 搜索与下载选项

正则使用 Go/RE2 语法，检查正文、消息与会话 ID、媒体类型、文件名、MIME、
音频标题与演唱者、投票问题与选项，以及结构化信息 JSON。
`^456$` 等 ID 正则也可能匹配其他同值字段，请配合 `--peer` 并先预览。

`--limit` 范围 `1` 至 `1000`，按消息时间从新到旧排序。
本地搜索不加归档写锁，可与归档器同时运行。跨会话结果不是一个全局事务快照，下载时会再次在线验证。
搜索只读 SQLite 元数据与 JSONL，不修改记录或主数据库内容。
SQLite 只读连接仍可能创建 WAL/SHM 协调文件。
下载器同样读取消息数据，但需要更新共享 SQLite 下载记录的权限。

下载器默认 `--data ./fetch-data`，文件目录为 `fetch-data/attachments`，
`--interval 2s`（最低 `1s`），`--max-file-bytes 268435456`（每文件 256 MiB）。
大小限制接受 `1` 字节至 `10` GiB。下载顺序执行，单独持久化 `FLOOD_WAIT` 等待状态。
支持图片与文档类附件，包括语音、视频、音乐、贴纸和动画，不访问网页预览的第三方 URL。

下载前重新读取原消息和保护状态，通过 Telegram 授权接口取得新鲜文件引用，
完成后再次核对消息，再提交文件。引用过期最多刷新一次。
失败或正常中断时尽可能清理部分文件；强制杀死进程可能在输出目录留下 `.fetch-*` 文件。
