# OSS 直传 API

G1 从 `origin/main` 的 `5a326071f13ea303ee9abc6ffa759e1a02af687c` 开始。G2 以 G1 合并后的最新 `origin/main` 为基线。以下路径含 Nginx 和 Vite 对浏览器使用的 `/api` 前缀。

## 端点

| 方法与路径 | JSON 请求 | 成功响应 |
| --- | --- | --- |
| POST `/api/uploads` | `{"kind":"video","size":6291456,"content_type":"video/mp4"}` | 201，包含 `upload` 和 `grant` |
| POST `/api/uploads/:id/credentials` | 空 | 200，包含 `upload` 和刷新后的 `grant` |
| GET `/api/uploads/:id` | 空 | 200，包含 `upload` |
| POST `/api/uploads/:id/complete` | 空 | 200，包含 `upload` |
| POST `/api/uploads/:id/cancel` | 空 | 200，包含 `upload` |
| POST `/api/video/publish` | `{"title":"猫咪","description":"#猫咪","video_asset_id":"<id>","cover_asset_id":"<id>"}` | 200，作品对象沿用既有字段 |
| POST `/api/account/applyAvatar` | `{"asset_id":"<id>"}` | 200，`{"avatar_url":"/api/media/assets/<id>"}` |
| GET 或 HEAD `/api/media/assets/:id` | 空 | 307，`Location` 为 15 分钟内有效的公网 HTTPS OSS URL，响应禁止缓存 |

上传会话、发布与头像应用使用既有应用 Bearer 登录令牌。媒体路由允许匿名访问已发布作品的资产和当前生效的头像。React 使用站内作品详情页作为分享链接。

`upload` 含 `id`、`kind`、`size`、`content_type`、`status`、`expires_at`。时间为 RFC 3339 UTC；校验通过后会话 ID 就是资产 ID。`grant` 含 `bucket`、`region`（形如 `cn-guangzhou`）、`endpoint`（公网 HTTPS）、`key`、`access_key_id`、`access_key_secret`、`security_token`、`expires_at`。授权响应使用 `Cache-Control: no-store`。上传会话只靠 ID 查询，React 按账号与会话保存断点，将临时凭据保存在内存，OSS 请求由独立客户端发送。

## 状态与约束

- `pending` 可上传和续期，初始有效期 24 小时。STS 每次签发最多 15 分钟。停止给不足 15 分钟的会话签发新凭据。状态查询在超时后返回 `expired`。
- `ready` 可发布，保留至会话过期。`published` 已用于作品或头像。完成接口对 `ready`、`published` 返回同一状态，重复取消已取消会话返回 `cancelled`。
- `cancelled`、`failed`、`expired` 不可发布。发布和取消由数据库校验并互斥。重复发布同一资产与作品内容返回原记录，变更内容或复用其他作品的已发布资产返回 409。
- MP4 视频最大 200 MiB。封面与头像均接受 JPEG、PNG、WebP，封面最大 10 MiB，头像最大 5 MiB。声明大小必须与对象大小一致。每个账号最多 12 个有效未发布会话，声明大小总额最多 512 MiB。每个账号每小时最多建立 30 个会话，每分钟最多请求 10 次凭据续期。
- 后端生成 staging Key，STS 策略仅授权该对象的写入，视频还可列举已上传分片和中止分片。完成时校验端不采信客户端 URL、ETag 或上传结果。先条件复制到客户端无权写入的最终 Key，再验证最终对象大小和真实格式。ETag 仅用于复制条件。staging 前缀配置对象过期和未完成分片清理。数据库清理未发布的最终孤立对象。
- MySQL 保存对象 Key、会话及状态。作品和头像字段保存稳定的站内路径；浏览器每次从稳定路径获得短期地址。过期时重新请求稳定路径并恢复视频时间。OSS 启用后旧的 Go 文件接收端点不参与新作品发布；读取旧作品和头像的 `/static` 地址与数据库中已存的地址。
- 若配置独立播放域名，它必须是已绑定此 Bucket 且支持 OSS 签名请求的 HTTPS 域名。浏览器上传始终用配置的公网 OSS Endpoint；服务端校验与复制走内网。

## 错误

业务错误返回 `{"code":"upload_conflict","error":"可显示的信息"}`。应用 JWT 中间件沿用既有 401 错误格式。

| HTTP | code | 含义 |
| --- | --- | --- |
| 400 | `invalid_request` | JSON、类型、大小或发布参数不合法 |
| 401 | 既有认证响应 | 登录令牌缺失、无效或过期 |
| 404 | `not_found` | 会话不存在、属于其他账号，或媒体未公开 |
| 409 | `upload_conflict` | 冲突状态、资产被占用或 OSS 对象尚未完成 |
| 410 | `upload_expired` | 会话已过期或剩余时间不足以签发 |
| 422 | `invalid_media` | 已收到的文件实际大小或格式无效 |
| 429 | `upload_limit` | 超出配额或请求频率 |
| 503 | `upload_unavailable` | OSS/STS、数据库或限流服务暂不可用，可重试 |

直传部署需要真实 Bucket、可 AssumeRole 的服务端身份、精确站点的 OSS CORS 与 Nginx CSP。具体云授权、浏览器验证及部署记录随 G2/G3 交付。
