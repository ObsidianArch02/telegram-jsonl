# 数据格式

[English](data-format.md) | [简体中文](data-format.zh-CN.md)

[返回 README](../README.zh-CN.md)

## 文件

各组件的状态保存在自己的数据目录中：

```text
data-tdl/
  .lock                 单进程会话锁
  session.json          账户授权，需按凭证保护
  state.sqlite          高频运行状态：客户端绑定、更新游标、同步任务、
                        回执、删除标记和等待状态
  archive/
    index.sqlite        低频属性：名称、用户名、access hash、媒体索引与已下载文件记录
    user-123.jsonl      私聊
    chat-456.jsonl      普通群
    channel-789.jsonl   超级群或频道
```

下载器在 `fetch-data/` 保存自己的 `session.json` 与 `state.sqlite`，
默认在 `fetch-data/attachments/` 保存附件，可用 `--output` 指定其他目录。
下载完成后只向共享归档数据库的 `files` 表写入文件记录，不改写消息、归档元数据、会话或媒体索引。

除消息文件外，只有登录授权继续使用 JSON，其他运行状态与索引均保存在 SQLite 中。
纯 Go SQLite 驱动不要求 CGO 或额外 SQLite 库，仍构建为单一独立可执行文件。
可选的 `sqlite3` 命令行工具便于检查数据库，程序本身不依赖它。

旧 `metadata.json`、`updates.json`、`cooldown.json` 和 `client.json`
不会被导入或读取，也不会被删除。
存在旧 JSON 元数据而没有 SQLite 索引的归档需要使用新的数据目录，详见[升级说明](usage.zh-CN.md#从-json-状态升级)。

不要把状态目录作为诊断附件分享。内部元数据含访问所需信息，不是下游数据格式。
限制文件权限不等于加密，还需使用系统访问控制和磁盘保护。
`state.sqlite` 是高频运行数据库，`index.sqlite` 是低频属性数据库，保存名称、用户名、
会话 access hash、媒体和文件路径。两者有意分开，频繁更新游标/任务不会重复写入稳定的会话属性。
两者都不是公开的会话名称导出文件，不应直接对外发布整个数据库。
数据库及其 journal 附属文件都需要保护（Unix 为 `-wal`、`-shm`，Windows 为 `-journal`）。
SQLite 备份 API 可生成一致的数据库快照。
若要把 JSONL 和数据库作为完整归档备份，请先停止归档器和下载器，再复制其状态。
只复制运行中的 `.sqlite` 文件可能丢失尚在 journal 内的已提交数据。
Windows 用户应检查目录的 NTFS 权限。
SQLite 只读连接也可能创建 WAL/SHM 协调文件。
搜索不进行逻辑数据库写入，也不修改主数据库或 JSONL，但不能保证目录内完全没有文件变化。

## SQLite 索引

`archive/index.sqlite` 包含以下表：

| 表 | 用途 |
| --- | --- |
| `state` | 高频序列化状态；`archive_metadata` 保存账户绑定、peer ID 和历史进度，`sync_cycle`、`sync_job:*` 与 `jsonl_intent:*` 保存可恢复任务和崩溃回执。名称与 access hash 从 `index.sqlite` 关联。 |
| `peers` | `peer`、`kind`、数字 `id`、显示名称 `name`、`username` 与 `is_dialog`。 |
| `media` | 当前附件记录的 `peer`、`message_id`、`media_id`、`media_kind`、`file_name`、`mime_type` 与 `size_bytes`。 |
| `files` | 已完成下载的 `peer`、`message_id`、`media_id`、`media_kind`、本地 `path`、`size_bytes`、`sha256` 与 `downloaded_at`。 |

`files` 主键为 `(peer, message_id, media_id, media_kind, path)`。
`media_kind` 来自实际下载附件的元数据，区分数值 ID 可能相同的不同附件类别。
`downloaded_at` 为 UTC RFC3339 时间，保留可用的小数秒精度。

显示名称与用户名来自发现的 Telegram 实体，发现前可能未知，也可能服务端本来没有。
UTF-8 名称与稳定 ID 一同保存，名称或用户名不构成唯一账户标识。
数据库不复制消息正文，也不提供 SQLite 全文搜索。
搜索仍然使用正则表达式检查本地 JSONL 消息。
各组件自己的 `state.sqlite` 在 `state` 表的键值记录中保存客户端绑定、更新状态、等待状态、
同步任务和崩溃回执，登录授权仍在 `session.json` 中。SQLite schema 版本为 2，旧 JSON 状态或
schema 1 不会自动迁移。

如果安装了 `sqlite3`，可只读查询已知会话名称：

```sh
sqlite3 -readonly ./data-tdl/archive/index.sqlite 'SELECT peer, kind, id, name, username FROM peers ORDER BY peer;'
```

查看已保存文件与仍然存在的附件信息时，用 `sqlite3 -readonly` 打开同一数据库，然后运行：

```sql
SELECT f.peer, f.message_id, m.file_name, f.path, f.size_bytes, f.sha256
FROM files AS f
LEFT JOIN media AS m
  ON m.peer = f.peer AND m.message_id = f.message_id AND m.media_id = f.media_id
  AND m.media_kind = f.media_kind
WHERE f.peer = 'channel-789'
ORDER BY f.downloaded_at DESC;
```

将示例会话 ID 换为你的数据库中的值。
远端删除或替换附件后，已下载文件记录继续保留，因此连接查询中的媒体字段可能为 `NULL`。
记录表示曾经完成下载，不证明本地文件仍然存在或未被修改。

## JSONL 记录

每行一条普通云端消息，包括收到和发出的消息。会话文件按消息 ID 排序。
新记录使用 schema 2，也支持读取 schema 1。

```json
{"schema":2,"account_id":42,"peer":"user-123","message_id":100,"sender":"user-123","date":"2026-10-04T08:00:00Z","outgoing":false,"text":"示例消息","reply_to":99}
```

可选字段包括 `edited_at`、`media_type`、`message_url`、`album_id` 和 `media`。
时间为 UTC。附件说明文字进入 `text`，图片或语音的 `text` 可能为空。
`media_type` 保留 Telegram 协议类型，`media.kind` 表示具体细分类。

**JSONL 是当前状态快照，不是只追加的事件日志。** 消息变化时原子替换对应会话文件，
每条消息只保留当前版本。读取方应重新打开路径，不要长期持有旧文件描述符，
也不要假定持续跟踪文件尾部能观察到所有变化。

## 非文本内容

| 内容 | 保存的信息 |
| --- | --- |
| 图片、文档、视频、语音、音乐、贴纸、动画 | 类型、细分类、附件 ID、可用的文件名/MIME/大小/尺寸/时长，音频标题与演唱者、贴纸字符。 |
| 相册 | 分别保存成员消息，以 `album_id` 关联。 |
| 投票 | 问题、选项、已知计票、总票数、当前账户选择、关闭/多选/测验标志、可用答案说明，不保存投票者名单。 |
| 位置、实时位置、地点 | 经纬度、精度、实时位置有效期与朝向、可用地点名称/地址/提供方。 |
| 联系人 | 手机号、姓名、可用用户 ID，最多 8 KiB 的 vCard。 |
| 网页预览 | 可用 URL、标题、描述、站点和作者，不访问网页或保存 Instant View 正文。 |
| 骰子 | 表情字符与结果。 |
| 回复 | 被回复消息 ID，不复制引用正文。 |
| 自毁、受保护、受限制或付费媒体 | 整条跳过；原先存在的同条记录会清理。 |
| 秘密聊天与系统服务消息 | 不归档。 |

`media` 编码大小最多 32 KiB，超过限制时替换为 `kind` 与 `omission_reason`。
仅 vCard 超限时省略该字段并记录在 `omitted_fields`。
不保存缩略图、音频波形、二进制附件、完整网页缓存和原始协议消息。
编辑和删除对整条记录及结构化信息一起生效，投票更新替换当前结果，不增加事件历史。

结构化信息不保存会过期的媒体 `file_reference` 和媒体授权 `access_hash`。
会话 access hash 仅保存在内部状态中，供正常授权请求使用。
下载器重新获取原消息，以取得当前文件引用。

## 消息链接

超级群和频道的 `message_url` 使用官方格式
`https://t.me/c/<channel_id>/<message_id>`，相册成员增加 `?single`。
它是原消息入口，不是 HTTP 附件下载直链，访问仍受成员关系、账户权限及消息是否存在限制。
私聊和普通群仅保存标识，不编造通用 HTTPS 链接。

参考：[消息链接](https://core.telegram.org/api/links#message-links)和
[授权文件下载](https://core.telegram.org/api/files#downloading-files)。

## 敏感内容

记录可能包含他人消息、手机号、vCard 和精确位置。账户授权不代表取得这些内容的任意使用权。
导出、备份和下游副本需由操作者管理，详见 [Telegram 相关免责声明](../DISCLAIMER.zh-CN.md)。
