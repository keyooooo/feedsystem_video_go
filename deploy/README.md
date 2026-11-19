# 轻量应用服务器发布与回滚

当前发布工作从 `origin/main` 的 `79d741fd2593b20bc643636f173bb46292d3b75f` 开始。部署以测试通过的 40 位提交 SHA 和镜像 ID 或 digest 为准。在单独的发布目录构建，工作树必须干净。

## 现有数据与恢复记录

轻量应用服务器位于广州。迁移盘点时，`feedsystem_video_go` 有 1,010 个账号和 10,002 条 `/static` 作品记录，API 在 8080。独立的 `feedsystem-demo` 有 1 个账号和 20 条使用旧 `/api/media/…` 路径的 OSS 作品，网页绑定到主机回环地址的 15173。

2026-11-19 为两套服务分别导出 MySQL 单事务快照及本地上传目录，服务器备份目录为 `~/feedsystem-g3-backup-20261119T072058Z-9PsoN1`。压缩归档均通过 SHA-256 校验。两套 SQL 已在网络隔离的新 MySQL 实例恢复，账号和作品数量吻合。演练使用临时实例，两个旧项目继续运行。

首次预发布用原项目的快照检验账号、作品和本地媒体兼容性。旧演示站的 OSS 对象与路径留给原服务读取。正式迁移该数据集时，先逐条核对素材授权、原作品和作者映射，并按新上传协议重新校验、发布。保留原数据和关联 ID 的备份。作品记录恢复成功与媒体文件可播放分别验收。

## 构建与配置

服务器使用支持 `!override` 的 Docker Compose。从测试通过的提交构建三个镜像。用归档分发源代码时保留文件权限，确保 RabbitMQ 用户能读取挂载的部署配置。私密环境文件单独设置为 `600`。

```bash
git rev-parse HEAD
deploy/build-images.sh true https://media.example.com https://media.example.com
```

构建参数只接受公开的 OSS Origin。验证基础部署时可用 `deploy/build-images.sh false`，同时在回环预发布环境设置 `OSS_DIRECT_UPLOAD=false`。真实 OSS 直传验收使用两个开关均启用的镜像和配置。

将 `deploy/.env.production.example` 复制到源码目录之外，例如 `~/.config/feedsystem/release.env`。目录权限设为 `700`，文件权限设为 `600`。在服务器上使用 `openssl rand -hex 32` 为 MySQL root、MySQL 应用账号、Redis、RabbitMQ 和 JWT 分别生成密钥。业务容器使用独立的 MySQL 用户。更新镜像时保留上一版镜像与环境文件。

填写构建脚本输出的三个应用镜像名，记录镜像 ID 与 `org.opencontainers.image.revision`。从官方镜像选择与当前 MySQL 逻辑快照兼容的 MySQL 8.0 镜像，以及同一轮环境验收的 Redis 7.4 和 RabbitMQ 4.4 镜像。拉取后用 `docker image inspect` 取得对应 digest 并填入环境文件。服务器构建受内存和磁盘约束时，从具备资源的构建机传入经校验的 `docker save` 归档，再用 `docker load` 导入。保留镜像 ID、架构、SHA 和 CI 链接。

对照 [直传 API 和广州 OSS 配置](../backend/OSS_UPLOAD_API.md) 填写私有 Bucket、服务器 RAM 用户、上传角色 ARN、分片生命周期及精确 CORS 来源。RAM 访问凭据只由 API 读取。前端镜像的上传、播放 Origin 标签须与发布配置匹配。广州的新 OSS 用户可能需要已备案的自定义域名及其 HTTPS 证书才能通过公网调用数据 API。

## 预发布与迁移

在发布目录核对项目名、网络和空闲端口。预发布默认使用 15174，另给 Compose 设置一个独立项目名。自定义子网时，固定的前端代理 IP 要在子网内，并位于动态地址池之外。环境中保留旧项目可用的内存和磁盘空间。

```bash
preview=(docker compose --env-file "$HOME/.config/feedsystem/release.env" \
  -p feedsystem-g3-preview -f compose.production.yml)
"${preview[@]}" config --quiet
"${preview[@]}" up -d --wait mysql redis rabbitmq
```

如需验收数据迁移，在 API 首次启动前将快照导入该项目的空库。先执行 `sha256sum -c SHA256SUMS`，再恢复数据库和本地上传目录。复用相同的项目会复用数据卷，恢复前核对现有数据并保留快照。下面以原项目的备份为例。

```bash
backup="$HOME/feedsystem-g3-backup-20261119T072058Z-9PsoN1"
gzip -dc "$backup/feedsystem_video_go.sql.gz" | "${preview[@]}" exec -T mysql \
  sh -ec 'export MYSQL_PWD="$MYSQL_ROOT_PASSWORD"; exec mysql -uroot "$MYSQL_DATABASE"'
gzip -dc "$backup/feedsystem_video_go.uploads.tar.gz" | "${preview[@]}" run -T --rm \
  --no-deps --user 0 --entrypoint sh backend \
  -ec 'tar -C /app/.run/uploads -xf - && chown -R app:app /app/.run/uploads'
printf "UPDATE accounts SET refresh_token = '';\n" | "${preview[@]}" exec -T mysql \
  sh -ec 'export MYSQL_PWD="$MYSQL_ROOT_PASSWORD"; exec mysql -uroot "$MYSQL_DATABASE"'
"${preview[@]}" up -d --wait
"${preview[@]}" ps
```

清除刷新令牌只在恢复后的副本执行，让用户以原账号重新登录。这一步和新 JWT 密钥一起验证旧会话失效。MySQL、Redis、RabbitMQ 和上传卷均归独立项目。Worker 等 API 完成迁移和依赖就绪再启动；对数据库恢复的 Outbox 核对待处理事件和重试。

```bash
ssh -N -L 15174:127.0.0.1:15174 admin@8.134.84.151
```

在 `http://127.0.0.1:15174` 验收健康端点 `/api/readyz`、主页及深层路由、登录与注册、旧媒体的 HTTP Range、私信和互动事件。启用真实测试 Bucket 后，再用独立测试账号核对 CORS、STS 范围、直传与签名续播。确认影子项目端口只在回环监听，并记录内存、磁盘、API/Worker 日志和重启表现。

### 回环验收记录

服务器上的 `feedsystem-g3-preview` 从原项目恢复了 1,010 个账号、10,002 条作品记录及上传目录。构建机将已校验的镜像归档传入 SWAS；已核对传入前后的镜像层摘要。六个容器均通过健康检查，预发布前端只监听 `127.0.0.1:15174`。发布目录中的 `.release-sha` 和 `.deployed-images` 记录实际部署的提交与镜像 ID。

当前回环环境使用本地媒体模式，已验证匿名 Feed、注册、登录、视频与封面上传、发布、深层路由和 `206 Content-Range`。独立验收数据仅写入预发布副本。真实 OSS 直传需要另行配置测试 Bucket、上传角色和浏览器 Origin。

## 公开入口与回滚

在域名、证书、适用的公开服务手续和下列阻断项通过后，使用独立项目运行 `-f compose.production.yml -f deploy/compose.public.yml`。将已验证的证书目录按样例挂载到 Nginx。证书首次申请和续期可用 DNS 验证。续期后用 `nginx -t` 检查并在容器内重载。80 将指定域名转到 HTTPS，443 终止 TLS，HSTS 初始为一天。Compose 内部只信任 React Nginx 的固定 IP 作为代理，代理用连接地址覆盖客户端转发头。

| 阻断项 | 公开切换前的验收 |
| --- | --- |
| 入口和代理 | 域名、证书链、重定向、HSTS、Host、登录、上传、Range、SSH 和防火墙来源。确认旧 8080 的使用方已迁移，再关闭公网暴露。 |
| 会话 | 完成独立 `codex/session-hardening` 修改及联调，验证安全 Cookie、CSRF、过期、轮换、撤销、换号和重启。 |
| OSS | 使用真实 Bucket 验证私有权限、同地域内网校验、续期、隔离和取消、浏览器 CORS、范围请求及移动 Safari。 |
| 密钥和容量 | 验证旧密钥和旧会话失效，检查监听端口与容器实际配置。完成 Go 依赖与最终镜像扫描、备份恢复、容量和回滚演练，并配置磁盘、内存、依赖就绪、队列积压、5xx 和 OSS 用量告警。 |

公开切流前先暂停旧入口写入，做最终快照并验收增量数据。回滚优先将入口恢复到上一个已验收的 HTTPS 版本，使用保留的 Compose 配置、镜像和数据卷。停用新入口只影响该发布项目的前端容器。存在不兼容数据库变更时，先导出新库和新增上传，恢复到独立项目后再切入口；保留两个版本的数据仓并核对切流期间写入。旧服务及旧卷保留供恢复。切流和回滚都验证登录、已有视频、Range 以及对外端口。
