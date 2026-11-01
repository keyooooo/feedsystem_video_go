# ShortVideo React frontend

React 19 + TypeScript + Vite 7 + React Router 7 + Zustand 5。界面采用炭黑、青绿与珊瑚红设计，支持电脑侧栏、平板图标导航和手机底部导航。

## 启动

使用 Node.js 24。在当前目录执行：

```bash
npm ci
cp .env.example .env.local
npm run dev
```

默认通过本地 `/api` 与 `/static` 代理连接 `http://8.134.84.151:8080`。修改 `.env.local` 中的 `API_PROXY_TARGET` 可切换后端。`VITE_API_BASE` 默认 `/api`，属于构建时变量；不要在 `VITE_` 变量中存储密码。

## 页面

- `/` 推荐、关注、点赞榜；搜索已加载的视频，窗口内最多挂载三个视频。
- `/hot` 热榜；`/video/:id` 视频详情与评论。
- `/video` 拖放上传、封面预览、分片续传和发布；上传时离开页面需要确认。
- `/account` 登录或自己的作品/喜欢；注册与改密 URL 保持不变。
- `/u/:id` 主页、粉丝、关注；`/messages` 联系人；`/messages/:peerId` 会话。
- `/settings` 资料编辑、头像、退出登录。

请求层兼容原后端协议与 token 存储键。401 刷新合并并发请求；退出后旧响应不能恢复会话。关注操作兼容后端异步落库。弹窗、Tabs 使用 Radix，图标使用 Lucide；支持键盘、减少动态效果和清晰焦点。

## 验证

```bash
npm run lint
npm run typecheck
npm test
npx playwright install --with-deps chromium webkit
npm run test:e2e
npm run build
# 一次执行上述代码检查、测试、构建和浏览器回归
npm run verify
```

浏览器测试使用自主生成的 MP4/封面与独立 Mock，覆盖四种视口、实际播放、发布、离页取消、焦点和 axe 检查。截图及 trace 存放在忽略的 `test-results/`。CI 同时运行 Chromium、WebKit 和生产镜像路由/API/Range smoke test。

真实服务器联调显式执行 `npm run test:live`，只使用专用账号和合成作品，凭据保存在被忽略的 `.e2e-accounts.local`，权限 600。不存在删除 API 的视频与私信会保留在测试账号下；可撤销的关注、点赞和评论会清理。运行 `npm run test:live:browser` 可只读验证已发布测试视频与桌面/手机截图。

## 构建与部署

`npm run build` 输出 `dist/`。从仓库根目录构建：

```bash
docker build -f frontend/Dockerfile -t feedsystem-frontend .
```

镜像 smoke test 使用合成哨兵文件验证 `*.local`、本地环境文件和测试产物不会进入构建层。

保持现有 Compose 与后端服务配置，Nginx 托管 React 构建产物，支持 history URL 回退，并代理 `/api` 与 `/static`。本次重构不自动替换服务器容器。

## 维护

开始修改前阅读 `AGENTS.md`、`Agent.md` 和 `REFACTOR_PLAN.md`。每批变更同步测试、验证、独立提交和检查 CI。Vue 基线可从 `58a7c0c` 或迁移过程提交中恢复。
