# 影子审核运行与演示

本手册对应 CONTENT_MODERATION_SPEC.md 第 11 节。演示使用独立开发环境。需要自动折叠时，Worker 环境配置 `MODERATION_AUTO_FOLD_MODE=threshold` 并重建 Worker；模型有效垃圾营销决策置信度 >= 0.90 时可自动折叠。`shadow` 只保存建议；未配置或 `approved` 保持原有放量和校准门槛。模型建议由审核员逐单确认，完整人工标注与正式放量见 Spec 第 12 节。

## 启动与配置

需要 Go（版本见 backend/go.mod）、Node（版本见 frontend/package.json）、Docker Compose，以及 MySQL、Redis、RabbitMQ。根目录 docker-compose.yml 提供完整开发栈，部署前按 .env.example 设置数据库、Redis、RabbitMQ 和 JWT 配置。

从项目根目录的 Bash 终端设置模型环境。输入的 Key 只存在于当前终端及子进程环境。

```bash
export MODERATION_MODEL='deepseek-v4-1-flash-economy-261017'
export MODERATION_MODEL_BASE_URL='https://ark.cn-beijing.volces.com/api/v3'
read -rsp '请输入方舟 API Key: ' MODERATION_API_KEY
printf '\n'
export MODERATION_API_KEY
docker compose up -d --build
```

启用真实模型后，新工单会消耗 API 额度。日常自动化测试使用下文的本地模型桩。

API 启动通过 internal/db.AutoMigrate 创建 moderation 表及评论治理字段。Worker 负责 Outbox、RabbitMQ 建单与影子执行。检查 API 和 Worker 日志以确认迁移和依赖连接成功。

```bash
docker compose ps
docker compose logs --tail=100 backend worker
```

直接运行 Go 程序时，在 backend 目录分别执行以下命令。配置默认读取 configs/config.yaml，可用 CONFIG_PATH 指定其他文件。API 和 Worker 使用相同数据库及中间件配置。

```bash
go run ./cmd
go run ./cmd/worker
```

这两条命令分别占用终端，模型环境需在启动 Worker 的终端设置。前端在 frontend 目录安装依赖并使用真实 API 模式启动。

```bash
npm ci
API_PROXY_TARGET=http://127.0.0.1:8080 npm exec vite -- --mode test --port 5173
```

打开终端显示的本地地址。

## 审核员与影子模式

先通过应用注册独立演示账号，从 accounts 表核对其 ID。由开发环境数据库管理员将该 ID 写入 moderation_reviewers。以下 SQL 中的 123 替换为实际 ID。

```sql
INSERT INTO moderation_reviewers (account_id) VALUES (123)
ON DUPLICATE KEY UPDATE account_id = VALUES(account_id);
SELECT policy_id, percent FROM moderation_rollouts;
```

演示使用新建开发数据库，moderation_rollouts 默认没有启用记录。若该开发库存在历史记录，将其 percent 设为 0 后再演示，并查询确认。

```sql
UPDATE moderation_rollouts SET percent = 0;
SELECT policy_id, percent FROM moderation_rollouts;
```

## 最短演示流程

1. 用作者账号发布视频和评论，准备一条明确营销评论，以及一条引用上文劝阻广告的回复。
2. 用举报者账号在评论中选择举报，提交原因，记录返回的举报编号。
3. 查看 Worker 日志和审核台 /moderation，确认工单进入待终审。模型暂不可用或超时也应保留工单供人工处理。
4. 审核员先阅读目标和回复语境，填写处置方式、条款与理由，保存决定。需要查看模型建议时点击对应按钮，审核台记录这次查看。
5. 对营销评论人工折叠，刷新评论列表，确认占位及主动展开。对劝阻回复维持展示。保存后可点击“审核下一条”，分批处理。
6. 举报者打开通知和 /reports/举报编号，核对处理结果。刷新页面后结果仍可查询。
7. 审核员恢复此前折叠的评论，确认展示同步更新。作者删除后的正文保持删除状态。
8. 用普通账号访问 /moderation，核对无工单访问权限。

演示记录应写明提交版本、样例编号、操作者角色、实际结果和时间。展示与通知闭环以实际操作结果为准。

## 本地回归

从 backend 目录运行普通测试和不联网的离线冒烟评测。

```bash
go test ./...
go run ./cmd/moderation-eval -scripted -input internal/moderation/testdata/eval-smoke.jsonl
```

真实中间件集成测试会写入隔离测试库和测试 vhost。必须使用可丢弃的 moderation_e2e_test 数据库与同名 RabbitMQ vhost。服务创建示例见 .github/workflows/moderation-e2e.yml。

```bash
export MODERATION_TEST_MYSQL_DSN='moderation:test-password@tcp(127.0.0.1:3306)/moderation_e2e_test?parseTime=true'
export MODERATION_TEST_AMQP_URL='amqp://moderation:test-password@127.0.0.1:5672/moderation_e2e_test'
export MODERATION_TEST_REDIS_ADDR='127.0.0.1:6379'
go test -race -tags=moderation_e2e -count=1 -timeout=90s -v ./internal/moderation
```

从 frontend 目录验证界面。审核浏览器用例拦截 API 并使用固定数据。

```bash
npm run lint
npm run typecheck
npm test -- --run
npm run build
npx playwright test e2e/moderation.spec.ts --project=chromium --workers=1
```

## 排障与额度

工单未出现时依次检查举报记录、Outbox、RabbitMQ 连接和 Worker 日志。待终审但没有建议时检查 Worker 模型配置、失败原因和证据状态。保存返回 409 时重新核对当前版本，网络失败时重试同一决定。SSE 断线后客户端重新拉取队列，也支持手动刷新。

真实模型请求前确认样本数、预计请求次数和 token。已有历史报告及其标签限制见 Spec 第 9、10 节。开发报告中的 token 为已返回用量，实际计费与剩余额度以方舟控制台为准。

## 阈值自动折叠验证

在启动 Worker 前显式设置 `MODERATION_AUTO_FOLD_MODE=threshold`。Compose 需要重新创建 Worker 才会读取新环境变量。启动日志应包含 `Moderation execution mode: threshold; fold confidence threshold: 0.90`。

用独立演示账号发表营销评论并用另一账号举报；工单完成后查看 `execution_mode=threshold`、`execution_outcome=applied`、工单 `resolved`、评论 `moderation_folded=true` 和审核决定 `reviewer_id=0`。确认评论区默认只在底部展示“已折叠评论”，点击后才出现完整评论行；自动化路径不能删除正文。低于 0.90、证据不足或工具协议失败的工单应进入人工审核。不要伪造放量数据来启用此模式，不要将历史待终审工单批量重放。脚本模型回归不消耗真实额度；真实模型验证限量执行。
