# HTTPS Cookie 会话

在现有 Go 服务设置 `SESSION_COOKIE_ORIGIN=https://demo.example.com`，在 React 镜像构建设置 `VITE_SECURE_SESSIONS=true`。两项同步启用。生产入口须验证证书及同源 HTTPS。发布脚本用 `RELEASE_SECURE_SESSIONS=true` 构建该 React 镜像，公开 Compose 从 `FEEDSYSTEM_PUBLIC_HOST` 生成 Go 的会话来源。旧版环境继续使用已有令牌协议。详见[部署与回滚](../deploy/README.md)。

| 请求 | 内容 | 成功响应 |
| --- | --- | --- |
| POST `/api/account/login` | 用户名和密码 JSON，精确站点 `Origin` | 200，包含 access token 和账号信息，同时设置刷新 Cookie |
| POST `/api/account/refresh` | 同源请求及刷新 Cookie | 200，轮换 Cookie 及 access token |
| POST `/api/account/logout` | 同源请求及刷新 Cookie | 200，撤销数据库会话及清除 Cookie。可以在 access token 过期后调用 |
| POST `/api/account/changePassword` | 同源请求及现有改密 JSON | 200，撤销对应账号的会话 |

Cookie 使用 `__Host-` 前缀、`Secure`、`HttpOnly`、`SameSite=Strict`，不设置 Domain。获取和轮换凭据的响应禁止缓存。错误来源返回 403，缺失或过期的有效会话返回 401，存储不可用返回 503。

MySQL 保存 access token 和刷新凭据的 SHA-256 摘要，刷新会话最长七天且采用固定截止时间，access token 最长 15 分钟。账号保持单活跃会话。刷新在事务中锁定对应账户，旧 Cookie 的延迟拒绝响应不会改写 Cookie。认证以 MySQL 中当前会话为准；重新登录、改密或退出后，过往令牌失效。通知流使用应用 Bearer。

React 只在内存持有 access token，从同源 Cookie 恢复登录。支持 Web Locks 的浏览器将 Cookie 改写串行化。旧 localStorage 凭据在启用时清理，显式退出标记只保存状态。在退出 API 不可用时阻止本机凭据恢复，服务端撤销仍须请求成功确认。切换账号时更新会话版本以中止之前归属的 OSS 上传流程。
