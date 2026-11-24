# 影子版验收记录

验收日期为 2026-11-24，时区 Asia/Hong_Kong。本次复验代码基线为 da84441，覆盖 Spec 第 11 节。真实模型调用为 0，自动折叠部署要求为 moderation_rollouts 无启用记录或全部 percent=0。

## 验证范围与结果

后端使用 Docker Desktop 29.8.0 的隔离 MySQL 8.4.13、RabbitMQ 4.4.1 和 Redis 7.4，端口分别为 23306、25672、26379。测试库与 vhost 均为 moderation_e2e_test。测试通过真实 HTTP 路由、数据库与消息代理验证流程，模型使用可控桩。

执行命令为 backend 目录中的以下命令，环境变量配置见运行手册。

```bash
go test -race -tags=moderation_e2e -count=1 -timeout=90s -v ./internal/moderation
```

整包通过，耗时 12.325 秒。日志位于本机 /tmp/moderation-shadow-acceptance.log。

| 第 11 节交付项 | 证据 | 结果 |
|---|---|---|
| 举报、Outbox、幂等建单 | TestReportEndToEnd，经路由提交、延迟启动发布、重复投递及代理重连 | 通过 |
| 并发证据与 Agent | 可选来源超时子测试、TestAgentReadsThreadAndChecksEvidence、TestAgentRejectsInvalidOutputs、调用预算测试 | 通过 |
| 人工终审与展示 | TestReportEndToEnd，折叠后公开响应脱敏、展开、恢复、删除及版本冲突断言 | 通过 |
| Worker 恢复 | TestShadowLeaseRecoveryMySQL、TestShadowWorkerKilledMySQL，含真实子进程终止后的恢复 | 通过 |
| 权限和实时队列 | TestReportEndToEnd，普通用户拒绝、事件送达、审核资格撤销后断流，以及浏览器重连用例 | 通过 |
| 结果通知与结果页 | TestReportEndToEnd 中重复决定、通知数量、同目标关联结果与权限断言，浏览器通知导航用例 | 通过 |
| 分批审核 | moderation.spec.ts 中保存后进入下一条并清空理由 | 通过，Chromium 4/4 |
| 离线回归和开发报告 | EvalCase、协议桩和计分回归测试，以及已签入的两个真实开发报告 | 本地测试通过，真实报告保留失败与草案限制 |
| 运行和演示说明 | CONTENT_MODERATION_RUNBOOK.md | 已交付 |
| 最终记录与遗留事项 | 本文 | 已交付 |

前端浏览器验证使用 API 拦截测试，后端真实中间件验证通过 HTTP 测试服务器运行。两部分共同覆盖交互与持久化协议。独立账号在完整部署上的人工演示步骤已交付，尚未执行人工演示。

Chromium 执行命令为 frontend 目录下的 npx playwright test e2e/moderation.spec.ts --project=chromium --workers=1，4 条通过，耗时 13.9 秒。日志位于 /tmp/moderation-browser-acceptance.log。

历史真实模型记录表明 shadow-v1 完成过两轮补证。当前 shadow-v2 的三条目标复测未通过严格标签要求，详见 Spec 第 9、10 节。该结果作为模型质量遗留项。

## 遗留事项

- 独立人工留出集、草案标签复核及至少 30 条线上独立确认进入后续正式放量阶段。
- shadow-v2 的必要父评论引用与草案标签适用性待人工核对。一条小样本请求达到总时限，继续保留转人工路径。
- 已有 CommentPanel React Hook lint 警告和构建大分块提示仍存在。
- 工作区保留此前删除弹窗与 study-guide 的独立修改。
- 所有真实模型新增调用须先说明预计规模并取得确认，日常测试使用本地模型桩。

## 最终核对

2026-11-24 最终执行 go test -race ./... 全部通过，日志为 /tmp/moderation-final-go.log。脚本化冒烟报告为 /tmp/moderation-final-scripted.json，模型为 scripted，流程回归使用当前提示词版本。此前前端 36 条单元测试、类型检查、lint（已有 1 条警告）和生产构建通过，当前 Chromium 审核复验 4/4 通过。

第 11 节各项交付已取得上述自动化证据，运行步骤与演示说明已交付。完整部署人工演示尚未执行，真实模型质量问题和正式放量保持后续事项。当前交付包含运行所需配置与检查方法，部署时必须按运行手册确认 rollout 比例为 0。
