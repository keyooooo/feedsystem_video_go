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
| GET `/api/media/assets/:id?resolve=1` | 空 | 200，`{"url":"https://..." }`，复用媒体公开状态校验和签名有效期，响应禁止缓存 |

上传会话、发布与头像应用使用既有应用 Bearer 登录令牌。媒体路由允许匿名访问已发布作品的资产和当前生效的头像。React 使用站内作品详情页作为分享链接。

`upload` 含 `id`、`kind`、`size`、`content_type`、`status`、`expires_at`。时间为 RFC 3339 UTC；校验通过后会话 ID 就是资产 ID。`grant` 含 `bucket`、`region`（形如 `cn-guangzhou`）、`endpoint`（公网 HTTPS）、`key`、`access_key_id`、`access_key_secret`、`security_token`、`expires_at`。授权响应使用 `Cache-Control: no-store`。上传会话只靠 ID 查询，React 按账号与会话保存断点，将临时凭据保存在内存，OSS 请求由独立客户端发送。

## 状态与约束

- `pending` 可上传和续期，初始有效期 24 小时。STS 每次签发最多 15 分钟。停止给不足 15 分钟的会话签发新凭据。状态查询在超时后返回 `expired`。
- `ready` 可发布，保留至会话过期。`published` 已用于作品或头像。完成接口对 `ready`、`published` 返回同一状态，重复取消已取消会话返回 `cancelled`。
- `cancelled`、`failed`、`expired` 不可发布。发布和取消由数据库校验并互斥。重复发布同一资产与作品内容返回原记录，变更内容或复用其他作品的已发布资产返回 409。
- MP4 视频最大 200 MiB。封面与头像均接受 JPEG、PNG、WebP，封面最大 10 MiB，头像最大 5 MiB。声明大小必须与对象大小一致。每个账号最多 12 个有效未发布会话，声明大小总额最多 512 MiB。每个账号每小时最多建立 30 个会话，每分钟最多请求 10 次凭据续期。
- 后端生成 staging Key，STS 策略仅授权该对象的写入，视频还可列举已上传分片和中止分片。完成时校验端不采信客户端 URL、ETag 或上传结果。先条件复制到客户端无权写入的最终 Key，再验证最终对象大小和真实格式。ETag 仅用于复制条件。staging 前缀配置对象过期和未完成分片清理。数据库清理未发布的最终孤立对象。
- MySQL 保存对象 Key、会话及状态。作品和头像字段保存稳定的站内路径；浏览器每次从稳定路径获得短期地址。过期时重新请求稳定路径并恢复视频时间。OSS 启用后，新作品通过资产 ID 发布。旧作品和头像仍可通过 `/static` 读取。
- 浏览器上传与播放使用公网 HTTPS。可以使用同一个已绑定到 Bucket 的域名，也可以分开配置。服务端校验与复制走内网。

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

## G1 配置与联调

G1 默认关闭。后端使用 `OSS_DIRECT_UPLOAD=true` 启用并要求 Redis 可用。配置 `OSS_REGION`、`OSS_BUCKET`、`OSS_UPLOAD_ROLE_ARN`、`OSS_ACCESS_KEY_ID`、`OSS_ACCESS_KEY_SECRET`。可设置 `OSS_UPLOAD_ENDPOINT` 为绑定到该 Bucket 的公网 HTTPS 域名。配置 `OSS_PLAYBACK_ENDPOINT` 可单独指定播放域名，否则继承上传配置。两个变量均留空时，使用默认公网 OSS 端点。`OSS_FFPROBE_PATH` 默认为 `ffprobe`。浏览器只使用响应内的临时 STS 字段。

服务端身份需要以下权限，资源均限定到配置中的 Bucket：

- `sts:AssumeRole` 限定到上传角色 ARN。该角色的信任策略允许服务端身份承担角色。
- `oss:GetObject` 限定到 `direct-upload/staging/*` 与 `direct-upload/assets/*`。
- `oss:PutObject` 和 `oss:DeleteObject` 限定到 `direct-upload/assets/*`。
- 上传角色只允许 `oss:PutObject`、`oss:ListParts`、`oss:AbortMultipartUpload` 操作 `direct-upload/staging/*`。后端的会话策略把资源进一步收紧到单个确切的 Key，图片凭据只发 `oss:PutObject`。

使用从未启用版本控制的私有 Bucket，启用阻止公共访问。仅对 `direct-upload/staging/` 配置两天后清理对象和未完成分片，避免规则触及可发布资产或既有视频。申请额度按声明大小计算，OSS 写入可能超过声明大小，完成时拒绝超额对象。配置 staging 生命周期与实际存储、请求告警。单进程最多同时验证两个对象。视频验证会从 OSS 内网读取已复制对象到临时目录，每个最多 200 MiB，为两个并发校验留出磁盘余量。

授权、查询、续期使用常规 API 超时。React 对完成校验使用至少 100 秒的请求超时，只有资产进入 `ready` 或 `published` 后才可继续。中断请求可重新查询或重试。发布与头像应用通过 MySQL 事务登记，给编辑资料接口传 `bio` 即可。播放遇到下载头或浏览器兼容性问题时，使用经真实浏览器验证的 OSS 绑定域名。

**测试边界**。Go HTTP 集成用专用测试数据库，云对象和 STS 用 Mock。正式 Bucket 端的权限隔离、真实凭据、浏览器 CORS、OSS Range 与签名过期恢复需要独立验收。


## React 接入与广州 Bucket 配置

React 的 `src/react/directUpload.ts` 管理会话、续期和断点，`ossTransport.ts` 通过阿里云 JavaScript SDK 签名，请求直接发往 OSS。视频按 5 MiB 切片，最多三个并发，请求最多尝试三次。封面和头像用独立的图片上传会话。出现网络中断后，在同一浏览器登录同一账号，重新选择同一文件即可恢复；主动取消对应的后端会话。应用 Bearer 只走应用 API，OSS 临时凭据只保存在内存。浏览器通过稳定媒体路径播放，签名接近过期时的拖动和媒体请求失败可触发刷新。分享链接使用站内作品详情页。

| 配置 | 示例或要求 |
| --- | --- |
| Bucket | `cn-guangzhou`，私有，阻止公共访问，staging 生命周期和 RAM 权限按上节 |
| `OSS_UPLOAD_ENDPOINT` | `https://media.example.com`，绑定该 Bucket 的 CNAME，证书覆盖该域名 |
| `OSS_PLAYBACK_ENDPOINT` | 可选，留空时继承上传端点。另设域名时须能承接有效的 OSS V4 签名和 Range |
| `VITE_OSS_DIRECT_UPLOAD` | React 镜像构建参数，设为 `true`，与后端开关成对配置 |
| `VITE_OSS_UPLOAD_ORIGIN` | 精确公网 HTTPS Origin，无尾斜杠，与授权中的上传地址匹配。默认 OSS 端点模式下填 Bucket 虚拟主机，例如 `https://your-bucket.oss-cn-guangzhou.aliyuncs.com` |
| `VITE_OSS_PLAYBACK_ORIGIN` | 精确公网 HTTPS 播放 Origin，留空时继承上传 Origin。若后端单独配置播放端点，此项填相同值 |

自 2025 年 3 月 20 日起，新开通 OSS 的用户在中国内地地域调用数据操作 API 需要使用已备案的自定义域名。广州 Bucket 的默认域名还可能返回强制下载头。正式环境配置自定义 HTTPS 域名，并从浏览器验证播放和图片预览。[阿里云地域与端点](https://www.alibabacloud.com/help/en/oss/user-guide/regions-and-endpoints) · [阿里云 OSS 自定义域名](https://www.alibabacloud.com/help/en/oss/user-guide/access-buckets-via-custom-domain-names)

**OSS CORS 规则**

- 来源填写实际 React 页面访问的 Origin，例如 `https://demo.example.com`。如果通过 SSH 隧道用 `http://localhost:18080` 验收，单独添加该精确来源，验收后按需移除。
- 方法允许 `GET, HEAD, POST, PUT, DELETE`。OSS 自动处理 OPTIONS 预检。请求头允许 `Content-Type, Range`；暴露 `ETag, Accept-Ranges, Content-Range`。启用 `Vary: Origin`，预检缓存可设 600 秒。
- React Nginx 镜像构建生成 CSP，`connect-src` 仅添加上传 Origin，`img-src` 和 `media-src` 仅添加播放 Origin。已有 `/static` 媒体继续从站内读取。保留任何其他应用已依赖的 CORS 规则，控制台保存前核对规则顺序。[阿里云 OSS CORS](https://www.alibabacloud.com/help/en/oss/user-guide/configure-cross-origin-resource-sharing)

**真实 Bucket 验收**

先用专用测试 Bucket 和测试账号开启直传，通过浏览器上传一个超过 5 MiB 的 MP4、封面和头像。验证 OSS 预检、跨 Key 或读取操作被 STS 拒绝、网络中断续传、取消与后端校验状态、发布后稳定路径 307 和最终 `206 Content-Range`。等待实际签名失效后再次拖动，检查 Range、时间位置与刷新。确认浏览器没有向应用 API 发送文件字节，也没有把应用 Bearer 发到 OSS。GitHub Actions 中的 OSS、STS 和签名过期模拟只覆盖应用行为，不能代替这次测试。
