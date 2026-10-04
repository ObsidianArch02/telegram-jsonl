# 数据格式

[English](data-format.md) | [简体中文](data-format.zh-CN.md)

[返回 README](../README.zh-CN.md)

## 文件

各组件的状态保存在自己的数据目录中：

```text
data-tdl/
  .lock                 单进程会话锁
  session.json          账户授权，需按凭证保护
  client.json           客户端应用与组件绑定，不含应用 hash
  updates.json          更新游标与频道 access hash
  cooldown.json         持久化的 FLOOD_WAIT 等待截止时间
  archive/
    metadata.json       账户绑定、会话、扫描进度与删除标记
    user-123.jsonl      私聊
    chat-456.jsonl      普通群
    channel-789.jsonl   超级群或频道
```

下载器在 `fetch-data/` 保存授权和等待状态，默认在 `fetch-data/attachments/` 保存附件，
可用 `--output` 指定其他目录。

不要把状态目录作为诊断附件分享。内部元数据含访问所需信息，不是下游数据格式。
限制文件权限不等于加密，还需使用系统访问控制和磁盘保护。

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
