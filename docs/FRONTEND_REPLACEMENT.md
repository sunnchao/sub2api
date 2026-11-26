# 独立前端替换方案（不改 `frontend/`、不改后端服务）

> 调研时间：2026-11-11。事实来源：本仓库源码 + GitHub 仓库 raw `package.json` / repo API 实读，不采信二手博客。
> 配套配置：`deploy/Caddyfile.frontend-split`

## 0. 结论

1. **不存在**「成熟 + 持续更新 + 可直接替换 Sub2API 完整管理后台」的第三方前端项目。生态里的第三方前端要么只读监控面板，要么浏览器扩展，要么是整仓 fork，绝大多数 0–7 star。别在这条路上浪费时间。
2. 你的三条约束（不改 `frontend/`、不影响后端、用成熟持续更新的开源框架）唯一自洽的解法：**在仓库外新建一个独立前端项目，用 Caddy 在同源下按路径分流**。Go 二进制、代码、配置、`frontend/` 全部零改动；回滚只改 Caddy 一行。
3. 框架首选 **`vue-vben-admin` 5.7.0**（33.8k star，MIT，2026-11-09 仍在提交，monorepo + 多 UI 适配），备选 `soybean-admin`（15.3k，MIT，Naive UI）。
4. **别误会「替换页面」的成本**：任何 Admin 模板自带的是示例页（mock 数据），它只能给你壳——布局、路由、权限、表格、表单、主题、i18n。Sub2API 的 12 类视图、26 个 API 模块、支付 / 验证码 / OAuth / Passkey / MCP / Setup 向导，全都要重新接 `/api/v1`。这是重写前端，不是换皮肤。

## 1. 三条约束怎么同时满足

| 约束 | 做法 | 依据 |
|---|---|---|
| 不修改当前 `frontend/` 目录 | 新项目放在仓库外（如 `../sub2api-ui`），构建产物 rsync 到部署机 `/srv/sub2api-ui/dist` | 与现有目录无任何文件级耦合 |
| 不影响后端服务 | 只在 `deploy/Caddyfile` 层分流；不重新编译、不重启 Go | 后端 `frontendServer.Middleware()` 是全局兜底中间件（`backend/internal/server/router.go:86`），分流后页面请求根本不到 Go |
| 第三方成熟且持续更新 | 用通用 Admin 框架当壳，业务层对接现有 HTTP API | 通用框架有社区维护；业务项目没有 |

**关键前提：必须同源部署**（`https://your.domain.com`），不要给新前端单独域名。否则 CORS、Cookie、WebAuthn RP ID、OAuth 回调、支付 SDK 全部要重配，那就等于动了后端。

## 2. Sub2API 生态里现有的「第三方前端」为什么都不行

| 项目 | Star / 最近提交 / 协议 | 形态 | 判定 |
|---|---|---|---|
| `qixing-jk/all-api-hub` | 5.1k / 2026-11-11 / AGPL-3.0 | 浏览器扩展，聚合多个 New-API / Sub2API 站点的余额、用量、Key | 不是服务端管理台，覆盖不到管理后台 |
| `NerverDevs/nervecenter` | 374 / 2026-10-24 | 多站点账号中心，README 明确标注 **closed source, showcase only** | 闭源，排除 |
| `lihuazou-gulie/GatewayLens` | 7 / 2026-09-10 | 独立监控面板（总览 / 分组 / 模型专题） | 只读看板，不提供管理功能 |
| `Tiath/S2A-Dashboard` | 7 / 2026-02-27 | Node + Express + 直连 PostgreSQL 的看板 | 只读、绕开 API 直连 DB，风险高 |
| `Wei-Shaw/sub2api_uienhance`（`yubai314` 镜像） | — | 官方整仓 fork：玻璃拟态 UI + VitePress 文档 | 改的就是 `frontend/`，且带整套后端，会把后端版本绑死；不符合约束 |
| `iris-cat-dev/sub2api-user-frontend` | 0 / 2026-10-12 / 无 license | 原前端 fork，README 自述「复用主站布局、登录态」 | 不是独立框架 |
| `ZYHUO/sub2api-frontend`、`yreidev/sub2api-frontend` | 0 | 前者 README 还是 Vite 初始模板文案 | 未完成 |
| `cnshiyi/sub2api-codexzh` 等 | 1 | 整仓 fork | 同上 |

## 3. 值得用的成熟框架（当「壳」）

| 框架 | Star / 最近提交 / 协议 | 技术栈 | 适配本项目的理由 |
|---|---|---|---|
| **vue-vben-admin 5.7.0** | 33.8k / 2026-11-09 / MIT | Vue 3 + Vite + TS，monorepo/turbo，可切 `web-antd` / `web-ele` / `web-naive` / `web-tdesign` | **首选**。schema 驱动 Table/Form、动态路由权限、i18n、主题全现成；纯 SPA 产物，可直接被 Caddy `file_server` 托管；MIT 商用友好 |
| soybean-admin | 15.3k / 2026-10-21 / MIT | Vue 3.5 + Naive UI 2.45 + UnoCSS + Pinia 4 + vue-router 5 + ECharts 6 | 代码最干净、视觉最好；适合中小规模管理台 |
| vue-pure-admin | ~20k / MIT | Vue 3 + Element Plus + Tailwind + TS | 轻量，依赖少 |
| Geeker-Admin | ~17k / MIT | Vue 3 + Element Plus + ProTable | ProTable 抽象值得直接抄 |
| new-api `web/` | 51.7k / 2026-11-11 / **AGPL-3.0** | React 19 + Rsbuild + Tailwind v4 + Base UI + TanStack | 同赛道最现代化，但 AGPL 传染 + React 换栈，仅作架构参考，不复制代码 |

许可提醒：new-api 是 AGPL-3.0，本仓库自身是 LGPL-3.0。Vben / Soybean / pure-admin 均为 MIT，最安全。

## 4. 目标架构

```mermaid
flowchart LR
    B[浏览器] --> C[Caddy :443 同源]
    C -->|@backend 白名单| G[Go :8080<br/>API / 网关 / 回调 / 老前端 embed]
    C -->|其余路径| N[/srv/sub2api-ui/dist<br/>独立构建的新前端]
    G --> P[(PostgreSQL / Redis)]
    N -.->|同源 /api/v1|B
```

Go 服务仍内嵌老前端，只是页面请求不再到达它。**这就是回滚开关**：把默认 `handle` 改回 `import go_backend`，老界面立刻恢复，无需回滚任何代码。

## 5. 已核实的代码事实（方案成立的依据）

| 事实 | 位置 |
|---|---|
| 前端静态资源由 `-tags embed` 打进二进制，全局中间件兜底 | `backend/internal/web/embed_on.go:24-31`、`backend/internal/server/router.go:75-86` |
| 后端自己有一份「必须让给 API」的路径白名单 `shouldBypassEmbeddedFrontend()` | `backend/internal/web/embed_on.go:355` |
| 本地覆盖目录 `data/public` 只对 embed 中已存在的路径生效；`index.html` 走 `serveIndexHTML` 注入，不经过 override | `embed_on.go:75, 92-104, 130-140` → **所以它不能用来整体换页**（详见风险 10.4） |
| 公开配置有 HTTP 接口 `GET /api/v1/settings/public` | `backend/internal/server/routes/auth.go:246`、`frontend/src/api/auth.ts:354` |
| 默认 API 前缀 `/api/v1`，可用 `VITE_API_BASE_URL` 覆盖 | `frontend/src/api/url.ts:1` |
| 鉴权 JWT 存 `localStorage.auth_token`，axios 带 `Authorization: Bearer`，`withCredentials: true` | `frontend/src/api/client.ts:22-23, 44` |
| 开发代理 `/api`、`/v1`、`/setup` 指向后端 | `frontend/vite.config.ts:156-169` |
| 所有 OAuth 回调在 `/api/v1/auth/oauth/*/callback` | `backend/internal/server/routes/auth.go:83-220` |
| 管理端 WebSocket 在 `/api/v1/admin/.../ws` | `backend/internal/server/routes/admin.go:238` |
| CSP 白名单（Stripe / Airwallex / 腾讯天御 / Cloudflare Insights） | `backend/internal/server/middleware/security_headers.go:18-83` |

**`window.__APP_CONFIG__` 不是硬依赖**：老前端把它当首屏优化（`embed_on.go` 的 `injectSettings`），拿不到时会回落请求 `/api/v1/settings/public`（`frontend/src/stores/app.ts:318-413`）。新前端直接调这个接口即可，**这是「零后端改动」能成立的核心**。

## 6. 必须回源 Go 的路径（`deploy/Caddyfile.frontend-split` 已按此配置）

| 分类 | 路径 |
|---|---|
| 管理 / 用户 API | `/api/*`（含 `/api/v1/settings/public`、全部 OAuth 回调、支付 webhook、MCP、管理端 WebSocket） |
| 网关协议 | `/v1/*`、`/v1beta/*`、`/backend-api/*`、`/antigravity/*` |
| 其他协议端点 | `/models`、`/models/*`、`/responses`、`/responses/*`、`/chat/completions`、`/embeddings`、`/messages/count_tokens`、`/alpha/search`、`/web_search`、`/x_search`、`/tts`、`/stt`、`/realtime`、`/custom-voices*`、`/contents/generations/tasks*`、`/v3/contents/generations/tasks*`、`/images/*`、`/videos*` |
| 初始化 / 健康检查 | `/setup/*`、`/health` |
| MCP OAuth 元数据 | `/.well-known/*`（配置里用通配覆盖 `/.well-known/oauth-protected-resource`、`/.well-known/oauth-authorization-server` 及其子路径） |
| 新前端独占 | `/`、`/assets/*` 及所有 SPA 页面路由 |

注意 Caddy `path` 匹配规则：`/foo/*` 是前缀匹配且可跨 `/`，但**不**匹配 `/foo`。所以精确路径与其通配形式必须成对写出。配置里已加 `@json_request` / `@json_accept` 保险丝，防止后端新增端点未同步白名单时被 SPA 兜底吞成 200 HTML。

## 7. 兼容要点（逐条都会咬人）

1. **鉴权**：同源 + `localStorage` ⇒ 老前端登录态新前端可直接复用。新项目沿用 `auth_token` / `refresh_token` / `auth_user` 等 key，并实现 refresh 拦截器（照抄 `frontend/src/api/client.ts` 与 `tokenRefresh.ts`）。
2. **公开设置**：启动即 `GET /api/v1/settings/public`，别依赖 HTML 注入。站点名、Logo、注册开关、支付开关、验证码类型、`table_default_page_size` 都从这里来。
3. **Setup Wizard**：`/setup/*` 是 API（已回源），但 `/setup` **页面本身是 SPA 路由**。新前端必须自己实现初始化向导，否则全新实例装完打不开。最容易漏。
4. **CSP**：Go 的 `SecurityHeaders` 只作用于回源响应；新前端 HTML 由 Caddy 提供，需在 Caddy 或新项目自行下发 CSP。漏了会表现为验证码 / Stripe / Airwallex SDK 静默加载失败。白名单照抄 `security_headers.go:18-83`。
5. **i18n**：客户端请求头 `Accept-Language` 决定后端译文（`client.ts` 拦截器）；GET 请求还自动带 `timezone` 参数。新项目要复刻，否则日期区间、错误文案会不对。
6. **SSE / WebSocket / 长连接**：都在回源白名单内。Caddy `encode` 的 `match` 已排除 `text/event-stream`（原配置就没列），别手贱加 `text/*` 通配，会缓冲流式响应。
7. **自定义请求头**：调度用的 `session_id` 等带下划线头，Caddy 默认透传；若前方还有 Nginx，需 `underscores_in_headers on`。
8. **Passkey / WebAuthn**：依赖 origin 与 RP ID，同源下不受影响；换域名会全量失效。
9. **支付与验证码组件**：Stripe.js、Airwallex、Turnstile、腾讯天御、阿里云验证码都绑着具体 DOM 与域名白名单，新项目要重新接 SDK，属于最后补齐的一批页面。
10. **API 契约面**：26 个 `frontend/src/api/*.ts` 模块、12 类视图、48 个 i18n 文件、323 个 `.vue`。建议把 `frontend/src/types/` 的类型定义直接复制进新项目（同为本项目 LGPL-3.0，无跨协议问题），不要手写。

## 8. 落地步骤

**P0 — 起壳并验证链路（不动生产）**
- 在仓库外 `git clone --depth 1 https://github.com/vbenjs/vue-vben-admin.git ../sub2api-ui`，选 `web-ele` 或 `web-antd` 变体，删掉演示页。
- `vite.config` 加 `/api`、`/v1`、`/setup` 代理指向 `localhost:8080`（照抄 `frontend/vite.config.ts:156-169`）。
- axios 实例设 `baseURL: '/api/v1'`、`withCredentials: true`、`Authorization: Bearer <localStorage.auth_token>`。
- 冒烟：登录接口 + `/api/v1/settings/public` 能通，拿到 200。

**P1 — 混合灰度上线（推荐，不要一刀切）**
- 用 `Caddyfile.frontend-split` 注释里的「混合过渡」写法：`@newui` 只列已实现的页面路径，默认 `handle` 回源老前端。
- 先实现：登录 / 注册、Setup 向导、Key 管理、用量看板、账号池、分组、系统设置。
- 每次加一个路径进 `@newui`，观察日志与错误率。

**P2 — 全量切换**
- 补齐支付、验证码、OAuth、Passkey、MCP、审计日志、提示词审计、渠道监控。
- 切到配置文件第 4 段的一刀切写法；保留回滚注释。

## 9. 验收清单

```bash
# 页面由新前端提供
curl -sI https://your.domain.com/ | head -5
curl -s https://your.domain.com/ | grep -o '<title>[^<]*'

# 后端链路全部无损
curl -s https://your.domain.com/health
curl -s https://your.domain.com/api/v1/settings/public | head -c 200
curl -s https://your.domain.com/.well-known/oauth-authorization-server | head -c 200
curl -sN -X POST https://your.domain.com/v1/messages \
  -H "x-api-key: $KEY" -H 'content-type: application/json' \
  -d '{"model":"claude-...","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}'

# SPA 深链回退
curl -s -o /dev/null -w '%{http_code}\n' https://your.domain.com/admin/settings

# 后台管理端 E2E：登录 → 创建 Key → 查看用量 → 支付页 SDK 加载 → 验证码弹出
```

## 10. 风险与未决项

| # | 风险 | 处理 |
|---|---|---|
| 10.1 | **白名单漂移**：后端升级新增网关端点未同步到 Caddy，会被 SPA 兜底吞成 200 HTML，静默故障 | 配置已加 JSON 头保险丝；每次升级后端对照 `embed_on.go:355` 复核一遍，纳入发布清单 |
| 10.2 | **业务重写工作量被低估**：模板示例页 ≠ 可用后台 | 按 P1 混合过渡逐页替换，不要一次性全切 |
| 10.3 | **Setup / 支付 / 验证码漏实现** | P1 就把 Setup 算进最小页面集；P2 前不要关掉老前端 |
| 10.4 | **`data/public` 覆盖机制的诱惑**：`embed_on.go:75` 有 `overrideDir = data/public`，看起来能不改后端换页。实际上 `index.html` 走 `serveIndexHTML`（`embed_on.go:104`）注入 `window.__APP_CONFIG__`，不经过 `tryServeOverride`；且新前端的 hashed 资源名在 embed 中不存在会直接回落老 HTML | **放弃这条路**。若允许改后端，最小改法是在 `Middleware()` 的 index 分支前加一次 `tryServeOverride(c, "index.html")`（约 3 行），但那就违反「不影响后端」，且升级即失效 |
| 10.5 | Caddy 配置未在本机用 `caddy` 二进制校验（环境未安装） | 已做静态自检：去注释后大括号 26/26 平衡、全局选项块为首个块、`@backend` 白名单与 `embed_on.go:355` 的 13 条前缀规则 + 19 条精确规则逐条比对**零缺失**（唯一的额外项 `/.well-known/*` 是刻意加宽以覆盖三个精确 well-known 路径）。上线前仍必须实跑 `caddy validate --config Caddyfile.frontend-split`；语法依据 Caddy v2 官方 matcher 文档（尾部 `*` 前缀匹配、`handle` 互斥、snippet `import`） |
| 10.6 | 框架选型尚未拍板 | 本文推荐 Vben 5.7；若偏好轻量干净选 Soybean。选定后即可按 P0 搭骨架 |
