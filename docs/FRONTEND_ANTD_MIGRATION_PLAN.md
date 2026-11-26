# 前端框架 Ant Design 迁移总体方案

> 版本：v1.0 · 2026-11-13
> 范围：把现有 `frontend/`（Vue 3 + Tailwind 自研设计系统）完整替换为 **Vue 3 + Ant Design Vue 4** 的新前端 `frontend-antd/`
> 方法：源码实读盘点（960 个文件 / 273,530 行 / 66 条路由 / 323 个 `.vue` / 384 个测试文件），逐页建立功能核对表

---

## 0. 结论（TL;DR）

| 项目 | 决策 |
|---|---|
| 技术路线 | **Vue 3.5 + ant-design-vue 4.2.6 + TypeScript**，保留 Pinia、vue-router、vue-i18n、axios、chart.js |
| 落地方式 | **新建 `frontend-antd/` 目录**，旧 `frontend/` 原样冻结（参考实现 + 回滚基线），不原地改造 |
| 视觉口径 | **AntD 原生设计语言 + 品牌主色 `#14b8a6`**；暗色用 `theme.darkAlgorithm`；不 1:1 复刻玻璃拟态 |
| 样式体系 | 移除 Tailwind 与 `style.css` 的 `@layer components`；只保留极少量全局 CSS（重置、滚动条、Markdown、driver.js 主题） |
| 功能保障 | 66 条路由、39 个通用组件、220 个业务组件、约 9,300 个 i18n key 全部建立迁移核对项；每页验收以「功能核对表」为准 |
| 构建切换 | 新目录先输出到 `backend/internal/web/dist-antd`；切换时改为 `dist`，一行回滚 |
| 周期 | 130 人日：单人约 26 周，双人并行约 13–15 周（详见 §15） |

**这不是「换皮肤」，是一次等价重写。** 现有前端没有任何 UI 框架，UI 层由 39 个自研组件 + Tailwind 工具类手工搭建；Ant Design 只能替换「外观与交互原语」，业务逻辑、支付流程、OAuth、实时连接、权限与开关语义必须逐条复刻。

---

## 0.1 实施进度（2026-11-13 实测）

> 本节记录**已经跑过的命令和真实输出**，与上面 v1.0 的方案推演区分开。方案里写"计划怎么做"，这里写"现在是什么样"。

### 0.1.1 质量闸门

| 闸门 | 命令 | 结果 |
|---|---|---|
| 类型检查 | `npx vue-tsc --noEmit` | **0 error** |
| 单元测试 | `npx vitest run` | **106 files / 863 tests 全绿**，exit 0 |
| 生产构建 | `npx vite build` | **✓ built in ~35s**，产物在 `backend/internal/web/dist-antd` |
| 视图挂载冒烟 | `npx vitest run src/__tests__/views.smoke.spec.ts` | **91/91 通过**（见 §0.1.3） |
| 浏览器渲染 | Vite dev + 预览面板 | `/` 落地页、`/login` 登录页正常渲染，无渲染期异常 |

### 0.1.2 组件替换实测量

| antd 组件 | 使用处数 | 说明 |
|---|---:|---|
| `AButton` | 612 | 含 `type/size/danger/loading/block` 全量映射 |
| `AInput` | 375 | `v-model:value` 绑定 |
| `AppNumberInput` | 235 | `AInputNumber` 包装层，见 §0.1.4 |
| `ACheckbox` | 161 | |
| `ASelect` | 49 | |
| `ATag` | 40 | 状态徽标 |
| `ARadio` | 28 | |
| `ACard` / `AModal` / `ADrawer` / `ADropdown` / `ASwitch` / `ATooltip` | 6 / 5 / 1 / 2 / 2 / 1 | |
| `AppDataTable`（封装 `a-table`） | 1（承接 21+ 业务页的 `DataTable` 兼容层） | |

**仍保持原生控件的部分（有意为之，非遗漏）**：

- `<button>` 94 处：品牌支付按钮（Stripe / Airwallex / 支付宝 / 微信）、`<label class="btn">` 文件上传触发器、需要 DOM 引用的按钮。
- `<input>` 60 处：`datetime-local` 15、`number` 12（带 `@input` 定制逻辑）、`date` 4、`radio` 1、`file` 1。原生日期控件的行为与 `ADatePicker` 的 Dayjs 值模型不同，改动会变更提交格式，故留作后续专项。
- `.card` / `.btn` / `.input-label` 兼容层：语义类已按 antd token 重写（见 `src/style.css` 的 `@layer components` 段），新代码直接用组件。

### 0.1.3 新增：视图挂载冒烟测试

迁移后原有的 772 个测试**全部是 api / composable / 工具函数的纯逻辑测试**，没有任何用例真正挂载业务视图；`vue-tsc` 也只看类型。模板层的问题（组件没注册、props 契约变了、setup 里读了已删除的导出）两道闸门都拦不住。

`src/__tests__/views.smoke.spec.ts` 补上了这一层：把 `src/views/**` 下全部 91 个 `.vue` 逐个挂载，断言无「未注册组件 / props 校验失败 / 渲染期异常」。数据层用「任意字段都能取到值的空数组代理」兜住，只验证组件本身。

**这个用例在写出来的当天就抓到一个会让线上白屏的真 bug**：`AppNumberInput.vue` 在批量替换 `<AInputNumber>` → `<AppNumberInput>` 时，把包装层自己的模板也替换了，于是组件渲染自身 → `Maximum call stack size exceeded`。`vue-tsc` 和既有 772 个测试全绿，浏览器点登录页也正常，只有真正渲染带数字输入框的页面才会崩。

### 0.1.4 关键的 5 个迁移适配层

| 文件 | 解决的真实问题 |
|---|---|
| `src/components/common/AppNumberInput.vue` | antd 的 `InputNumber` 不接受 `null`，业务表单用 `null` 表示「未填写」；该层做 `null ↔ undefined` 转换 |
| `src/utils/checkboxEvent.ts` | `ACheckbox` 派发的 `CheckboxChangeEvent.target` 不是 `HTMLInputElement`，与原生 handler 签名不兼容 |
| `src/utils/badgeColor.ts` | 旧徽标类名 → `Tag` 颜色的映射 |
| `src/ui/feedback.ts` | antd 4 的静态 `message` / `Modal.confirm` 不继承 `ConfigProvider` 上下文，需经 `App.useApp()` + `Modal.useModal()` 桥接 |
| `src/components/common/DataTable.vue` | 21+ 业务页的既有 import 兼容层，内部转发到 `src/ui/AppDataTable.vue` |

### 0.1.5 尚未完成（本轮未做，不要当成已完成）

1. **278 个组件 DOM 测试未迁移**。旧前端 383 个 spec，新前端 106 个；被落下的是 `components/**/__tests__/*.spec.ts` 和 `__tests__/integration/*`。这些用例断言的是旧设计系统的 DOM 结构与 class 名，无法 1:1 搬运，需要按 antd 结构重写。**当前覆盖率缺口主要在这里**，挂载冒烟只保证「不崩」，不保证「交互行为一致」。
2. **没有跑通端到端联调**。`backend/config.yaml` 指向远程生产库，本地起后端需要独立 Postgres/Redis；本轮只做了组件级挂载验证与公开页浏览器验证，认证后的页面没有真实点击验证过。
3. **构建产物切换未执行**。`outDir` 已改为读 `VITE_OUT_DIR`，默认仍是 `dist-antd`；切到 `dist` 只需 `VITE_OUT_DIR=dist npx vite build`，但 Dockerfile / Makefile / CI 的正式切换没有动。
4. **`.card`（230 处）与分段切换器（`.tabs`/`.tab`）未改为 `ACard`/`ASegmented`**。这两项纯属外观，且在没有后端可点的情况下批量机械替换风险大于收益，留到联调环境里逐页处理。

---

## 1. 范围与约束

### 1.1 包含

- `frontend/src` 全部 UI：89 个页面视图、220 个业务组件、39 个通用组件、布局、图表外壳、引导。
- 与新目录配套的工程化：Vite、TS、ESLint、Vitest、构建产物路径、CI/Docker 切换。
- 与新目录配套的验收资产：页面功能核对表、E2E 冒烟脚本、i18n key 完整性校验。

### 1.2 不包含（迁移期间保持不动）

- Go 后端任何代码与接口契约。
- `backend/internal/web/embed_on.go` 的嵌入机制与 `index.html` 注入逻辑。
- 部署拓扑（同源部署）、CSP 策略、OAuth / WebAuthn / 支付回调域名。
- 旧 `frontend/` 的功能演进；迁移期间旧目录只接受阻断性修复。

### 1.3 硬约束

1. **功能零缺失**：以本文件 §10 逐页清单为验收基线，缺一项即不通过。
2. **URL 不变**：66 条路由、别名、重定向、query 参数语义保持一致。
3. **状态不丢**：`localStorage` 键名与语义不变，老用户升级后登录态、主题、语言、分页大小、引导进度不重置。
4. **同源部署**：支付 SDK、WebAuthn RP ID、OAuth 回调、Cookie 依赖 origin，不做独立域名。
5. **可回滚**：切换前旧前端必须仍可构建、可部署。

---

## 2. 现状基线

### 2.1 规模

| 指标 | 数值 |
|---|---|
| `src` 文件总数 | 960 |
| 代码总行数 | 273,530 |
| `.vue` 文件 | 323（views 89 / components 220 / App 等） |
| `.ts` 文件 | 618（其中测试 384） |
| 视图代码 | 78,157 行 |
| 组件代码 | 72,472 行 |
| 非测试 TS | 54,007 行（含 i18n 21,236 行） |
| 测试代码 | 68,724 行 / 384 个 spec |
| 路由 | 66 条（含 4 条重定向、1 条 404 通配） |
| i18n key | 约 9,300（zh / en 双份） |
| Tailwind class 使用 | 约 110,000 处，覆盖 300 个 `.vue` |

### 2.2 现状技术事实（决定迁移方式）

- **无 UI 库**：`package.json` 里没有 naive-ui / element-plus / antd；全部 UI 由 `src/components/common/*` + `src/style.css`（778 行，`@layer components` 约 70 个类）+ Tailwind 工具类构成。
- **设计系统是隐式的**：主色 teal（`primary-50…950`）、深色板 `dark-800/900/950`、玻璃拟态 `glass`、渐变 `bg-mesh-gradient`、圆角 `rounded-xl`。
- **暗色模式**：`html.dark` class + Tailwind `dark:` 变体；入口在 `main.ts` 的 `initThemeClass()`，键为 `localStorage.theme`。
- **表格是核心资产**：`DataTable.vue` 1,152 行，承担桌面表格 + <768px 卡片列表 + `@tanstack/vue-virtual` 虚拟滚动 + 粘性列 + 操作列自动折叠 + 排序持久化 + 移动端框选；28 个页面直接使用。
- **弹窗/下拉是自研**：`BaseDialog` 80 个文件使用，`Select` 32 个视图使用，`Toast` 通过 `appStore.showSuccess/showError` 被调用 1,018 次 / 121 个文件。
- **图表是 chart.js**：15 个图表组件，含指标/来源切换与行内钻取，不是 ECharts。
- **实时能力**：管理端 Ops 使用 WebSocket；账户测试、渠道监控使用轮询；支付结果使用轮询与 localStorage 恢复快照。
- **构建产物内嵌 Go 二进制**：`vite.config.ts` 的 `outDir = ../backend/internal/web/dist`；`backend/internal/web/embed_on.go` 用 `//go:embed all:dist` 打包。
- **CSP 已允许内联样式**：`style-src 'self' 'unsafe-inline' …`，antd v4 的 CSS-in-JS 可正常工作；`script-src` 使用 nonce，不引入外链脚本即可。

### 2.3 现有目录职责

```
frontend/src
├── api/            60 个模块（用户 24 + 管理 36），与 UI 无关
├── types/          全局类型、支付类型
├── utils/          45 个纯逻辑工具（格式化、权限、计费、错误、脱敏等）
├── constants/      平台目录、账号/渠道常量
├── i18n/           zh / en，按命名空间拆分
├── stores/         9 个 Pinia store
├── router/         路由表 + 守卫 + 标题解析 + setup 重定向
├── composables/    22 个组合式函数
├── components/     220 个业务组件（common/layout/account/admin/user/payment/...）
├── features/       渠道监控 v2、提示词审计
└── views/          89 个页面
```

---

## 3. 技术选型

### 3.1 依赖决策

| 类别 | 选型 | 理由 |
|---|---|---|
| UI 库 | **ant-design-vue `4.2.6`** | Vue 3 原生、组件覆盖最全、主题 token 完善 |
| 图标 | **@ant-design/icons-vue `7.0.1`**；保留 `@lobehub/icons` | 业务图标迁移到 antd；模型品牌图标继续用 lobehub |
| 日期 | **dayjs `1.11.23`** | antd 日期组件原生值类型 |
| 按需引入 | **unplugin-vue-components + AntDesignVueResolver** | 控制包体；v4 无组件级 CSS 需引入 |
| 状态 | 保留 Pinia 2 | 无迁移收益 |
| 路由 | 保留 vue-router 4 | 路由表可原样搬运 |
| i18n | 保留 vue-i18n 11 | 9,300 个 key 零成本复用；仅补 antd locale 映射 |
| HTTP | 保留 axios + 现有 `client.ts` | 拦截器、刷新令牌、错误语义是资产 |
| 图表 | 保留 chart.js 4 + vue-chartjs 5 | antd 无图表；重写图表配置成本高、收益低 |
| 拖拽 | 保留 vue-draggable-plus | 支付方式排序、分组排序需要 |
| 虚拟滚动 | 视表格方案决定保留 `@tanstack/vue-virtual` | a-table 无内置虚拟滚动 |
| Markdown | 保留 marked + DOMPurify | 公告、自定义页 |
| 二维码 | 保留 qrcode（或换 `a-qrcode`） | 支付二维码需要可控渲染 |
| 导出 | 保留 xlsx + file-saver（按实际使用） | 管理端导出 |
| 引导 | 优先 `a-tour`，保留 driver.js 作为兜底 | 减少一个依赖；步骤锚点需重写 |
| 移除 | **tailwindcss、postcss 插件、`style.css` 组件层** | 与新设计系统冲突，双体系会长期失配 |

> 版本策略：迁移期 Vite 保持 5.x，不升级到 6/7，避免「换 UI + 升构建」双重变量。

### 3.2 包体控制

- antd 按需自动引入（`AntDesignVueResolver({ importStyle: false })`，v4 样式为 CSS-in-JS）。
- 手动分包沿用现有策略：`vendor-vue` / `vendor-antd` / `vendor-chart` / `vendor-i18n` / `vendor-stripe` / `vendor-misc`。
- 目标预算：首屏 JS gzip ≤ 现有 +15%；登录页 ≤ 180 KB gzip。

---

## 4. 新目录与工程结构

```
frontend-antd/
├── index.html
├── package.json
├── vite.config.ts            # outDir: ../backend/internal/web/dist-antd（切换时改 dist）
├── tsconfig.json
├── vitest.config.ts
├── eslint.config.js
└── src/
    ├── main.ts               # 主题初始化 → pinia → i18n → router → mount
    ├── App.vue               # ConfigProvider + App + 全局副作用宿主
    ├── theme/
    │   ├── tokens.ts         # 品牌 token / 组件 token
    │   └── useTheme.ts       # localStorage.theme + 系统偏好 + darkAlgorithm
    ├── api/                  # 从旧目录原样复制（P0 建立 diff 守卫）
    ├── types/
    ├── utils/                # 复制后清理 Tailwind 颜色返回（381 处）
    ├── constants/
    ├── i18n/                 # 复制；新增 antdLocale.ts
    ├── stores/               # 复制；toast 实现改 antd message
    ├── router/               # 复制；懒加载路径指向新 views
    ├── composables/          # 复制；重写 useSwipeSelect
    ├── layouts/
    │   ├── AppLayout.vue     # a-layout + a-layout-sider + a-layout-header
    │   ├── AppSider.vue     # a-menu（含分组、折叠、功能开关、自定义菜单）
    │   ├── AppHeader.vue    # 标题/公告/语言/主题/余额/用户菜单
    │   ├── AuthLayout.vue
    │   └── PageLayout.vue   # 替代 TablePageLayout
    ├── ui/                   # 设计系统适配层（关键）
    │   ├── AppDataTable.vue
    │   ├── AppSelect.vue
    │   ├── AppDateRangePicker.vue
    │   ├── AppModal.vue
    │   ├── AppConfirm.ts
    │   ├── AppToast.ts
    │   ├── AppPageHeader.vue
    │   ├── AppStatCard.vue
    │   ├── AppHelpTooltip.vue
    │   ├── AppImageUpload.vue
    │   ├── AppEmpty.vue
    │   └── types.ts
    ├── components/           # 业务组件（按域重写）
    ├── features/
    └── views/
```

### 4.1 复用边界

| 层 | 策略 | 说明 |
|---|---|---|
| `api/`、`types/` | **原样复制** | P0 建立 `scripts/check-api-sync.sh`，与旧目录 diff，防止后端契约漂移 |
| `utils/`、`constants/` | 复制 + 清理 | 381 处 Tailwind 类名返回值改为语义枚举（如 `status: 'success'`），由 UI 层映射颜色 |
| `i18n/` | 原样复制 | key 不变是「不遗漏功能」的最强约束 |
| `stores/` | 复制 + 局部改造 | `app.ts` 的 toast 改为 antd 适配；其余保持 API |
| `router/` | 复制 + 改路径 | 守卫逻辑逐行保留（setup、backend mode、simple mode、功能开关） |
| `composables/` | 大部分复制 | `useSwipeSelect` 重写；`useChannelMonitorFormat` 去掉 Tailwind 类名 |
| `components/`、`views/` | **全部重写** | antd 组件 + 新的布局与交互 |

---

## 5. 不变量清单（逐条验收）

1. **路由**：66 条路径、4 条重定向、`/auth/callback` 别名、`/batch-image` 别名 `/docs/batch-image`、404 通配。
2. **守卫**：`requiresAuth / requiresAdmin / requiresPayment / requiresRiskControl / requiresSubscription`、setup 未完成强制跳转、`backendMode`、`isSimpleMode`、`/model-plaza` 双重开关、管理端合规 423。
3. **存储键**：`auth_token`、`refresh_token`、`auth_user`、`theme`、语言键、`table-page-size`、排序键、引导键、支付恢复快照键。键名与语义不得改变。
4. **HTTP 行为**：`Authorization: Bearer`、`withCredentials`、401 自动刷新（并发合并）、`Accept-Language`、GET 自动附加 `timezone`、错误 envelope 解析、`STEP_UP_REQUIRED` 等错误码。
5. **功能开关**：`utils/featureFlags.ts` 的 opt-in / opt-out 语义与未加载时的宽容策略；后端注入字段与 API 回退。
6. **公开设置**：站点名/Logo/副标题/文档链接/自定义菜单/首页内容/计费模式/分页偏好等字段的消费方式。
7. **i18n**：zh / en 文案、命名空间、key 完整性测试；语言切换后整页 reload 的行为。
8. **暗色模式**：初始值优先级「localStorage → 系统偏好」，`html` 上的 class/属性，图表配色联动。
9. **移动端**：表格 <768px 卡片化、布局 <1024px 恢复正常文档流、侧栏抽屉、支付页移动端跳转。
10. **可访问性**：弹窗焦点管理、ESC 栈、aria-label、键盘可达。
11. **构建注入**：`window.__APP_CONFIG__` 可选注入；缺失时回退 `GET /api/v1/settings/public`。
12. **同源约束**：WebAuthn、OAuth、支付 SDK、CSP 不做变更。

---

## 6. 设计系统与主题

### 6.1 全局 token

```ts
// theme/tokens.ts
import type { ThemeConfig } from 'ant-design-vue/es/config-provider/context'

export const brandTokens = {
  colorPrimary: '#14b8a6',      // Tailwind primary-500
  colorInfo: '#14b8a6',
  colorSuccess: '#10b981',
  colorWarning: '#f59e0b',
  colorError: '#ef4444',
  colorLink: '#0d9488',
  borderRadius: 8,
  borderRadiusLG: 12,
  controlHeight: 36,
  fontSize: 14,
  fontFamily: `system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif`,
}
```

### 6.2 暗色模式

- `ConfigProvider :theme="{ algorithm: isDark ? theme.darkAlgorithm : theme.defaultAlgorithm, token }"`。
- `isDark` 由 `useTheme()` 统一管理：读 `localStorage.theme` → 否则 `prefers-color-scheme`；切换时写回并同步 `document.documentElement.classList.toggle('dark')`（供少量自定义 CSS 使用）。
- 图表、引导气泡、Markdown 正文通过 `isDark` 计算变量，不依赖 `document.documentElement.classList` 的直接读取。

### 6.3 组件级 token（建议值）

| 组件 | token |
|---|---|
| Layout | `headerBg: '#fff'`、`siderBg: '#fff'`、`bodyBg: '#f8fafc'`、`headerHeight: 64` |
| Menu | `itemBorderRadius: 8`、`itemSelectedBg: 'rgba(20,184,166,.12)'`、`itemSelectedColor: '#0d9488'` |
| Table | `headerBg: '#f8fafc'`、`headerColor: '#475569'`、`rowHoverBg: '#f0fdfa'`、`cellPaddingBlock: 12` |
| Card | `borderRadiusLG: 12` |
| Button | `borderRadius: 8`、`primaryShadow: 'none'` |
| Modal | `borderRadiusLG: 12` |
| Statistic | `contentFontSize: 24` |

> 暗色下不要逐项硬编码 `#1e293b`，优先让 `darkAlgorithm` 生成；只覆盖品牌色与容器背景。

### 6.4 品牌资源

- Logo、favicon、站点名仍由公开设置驱动（`updateFavicon`、`sanitizeUrl`）。
- 自定义菜单项的 `icon_svg` 继续走 `sanitizeSvg` + `v-html`，容器样式改为 antd 菜单图标位。
- 支付品牌图标（Stripe / Airwallex / 支付宝 / 微信）沿用 `src/assets/icons/*.svg`。

---

## 7. 组件替换映射

### 7.1 通用组件（`src/components/common`）

| 旧组件 | 行数 | antd 对应 | 封装策略 | 风险 |
|---|---:|---|---|---|
| `DataTable` | 1152 | `a-table` | `AppDataTable`：保留旧 `Column` 契约、`cell-{key}` 插槽改 `#bodyCell` 分派、移动端卡片模式、排序持久化 | **高** |
| `Select` | 639 | `a-select` | `AppSelect`：`options`/分组/`creatable`/`remote` 防抖/双 slot 映射 | 中 |
| `Pagination` | 247 | `a-pagination` | 由 `AppDataTable` 内置；保留页大小持久化与配置项 | 低 |
| `BaseDialog` | 167 | `a-modal` | `AppModal`：五档宽度、ESC 栈、焦点恢复 | 中 |
| `ConfirmDialog` | 71 | `Modal.confirm` | `useConfirm()` 返回 Promise；危险操作二次确认 | 低 |
| `Toast` | 153 | `a-message` | `appStore` 内部实现替换，**对外 API 不变**（1,018 处调用零改动） | 中 |
| `Input` | 103 | `a-input` | `a-form-item` 内使用；`enter` 事件保留 | 低 |
| `TextArea` | 81 | `a-textarea` | 同上 | 低 |
| `Toggle` | 29 | `a-switch` | 直接替换 | 低 |
| `SearchInput` | 45 | `a-input-search` | 保留 300ms 防抖语义 | 低 |
| `DateRangePicker` | 438 | `a-range-picker` | `AppDateRangePicker`：8 个预设、`preset` 回传、`YYYY-MM-DD` 输出、明天上限 | **高** |
| `EmptyState` | 80 | `a-empty` | 支持 `actionTo` 路由跳转 | 低 |
| `LoadingSpinner` | 65 | `a-spin` | 尺寸/颜色映射 | 低 |
| `Skeleton` | 46 | `a-skeleton` | 变体映射 | 低 |
| `StatCard` | 81 | `a-card` + `a-statistic` | `value` 格式化、趋势箭头 | 低 |
| `HelpTooltip` | 159 | `a-popover` / `a-tooltip` | hover 桥接区、click 模式、富内容 | 中 |
| `ImageUpload` | 164 | `a-upload` | `customRequest` 输出 dataURL / SVG 文本；300KB 限制 | 中 |
| `AnnouncementBell` | 477 | `a-badge` + `a-modal` | 业务逻辑保留，只换弹层外壳 | 中 |
| `AnnouncementPopup` | 201 | `a-modal` | 同上 | 中 |
| `ExportProgressDialog` | 68 | `a-modal` + `a-progress` | 直接替换 | 低 |
| `GroupSelector` | 134 | `a-checkbox-group` | 平台过滤/简单模式回写是业务逻辑 | 中 |
| `GroupOptionItem` | 146 | `a-select` option | 展示件 | 低 |
| `ProxySelector` | 420 | `a-select` + 自定义 option | 单条/批量测速状态机保留 | **高** |
| `LocaleSwitcher` | 105 | `a-dropdown` | 保留「切换后整页 reload」 | 低 |
| `VersionBadge` | 954 | `a-popover` | 更新/重启/回滚状态机整段保留 | **高** |
| `StatusBadge` | 39 | `a-badge` | 状态点 | 低 |
| `PlatformTypeBadge` | 322 | `a-tag` 组 | 平台/鉴权/档位/隐私/到期 | 中 |
| `GroupBadge` | 248 | `a-tag` + `a-tooltip` | 倍率删除线、剩余天数、高峰时段 | 中 |
| `GroupCapacityBadge` | 84 | `a-tag` | 阈值配色 | 低 |
| `SubscriptionProgressMini` | 358 | `a-popover` + `a-progress` | 多订阅聚合排序 | 中 |
| `NavigationProgress` | 109 | 自留 | 顶部进度条，antd 无对应 | 低 |
| `MonitorQuotaView` | 131 | `a-descriptions` + `a-progress` | tier 映射与倒计时 | 中 |
| `AutoRefreshButton` | 82 | `a-dropdown` + `a-switch` | 倒计时/间隔持久化 | 低 |
| `ModelIcon` / `PlatformIcon` | 389 | 自留 | 品牌 SVG，不替换 | 低 |
| `IpGeoCell` / `IpGeoBatchToolbar` | 160 | `a-tooltip` + `a-button` | 业务逻辑保留 | 低 |
| `Icon` | 151 | `@ant-design/icons-vue` | 91 个自绘图标建立映射表，缺失的保留自绘 | 中 |

### 7.2 布局

| 旧 | 新 | 要点 |
|---|---|---|
| `AppLayout` | `a-layout` + `AppSider` + `AppHeader` | 折叠宽度 72/256、移动端 `a-drawer`、内容区内边距 16/24/32 |
| `AppSidebar` | `a-menu` | admin / 我的账户 / 普通用户三套菜单；`expandOnly` 分组行为；功能开关过滤；自定义菜单；引导锚点 id |
| `AppHeader` | `a-layout-header` | 标题解析链、公告、文档、模型广场、语言、订阅进度、余额胶囊、用户菜单 |
| `AuthLayout` | 新 `AuthLayout` | 渐变背景可简化，保留品牌区与页脚 |
| `TablePageLayout` | `PageLayout` | 四段式（操作/筛选/表格/分页）；`a-table` 用 `sticky` + `scroll.y` 替代高度链 |

### 7.3 必须自留的 antd 无对应能力

1. `DataTable` 的移动端卡片模式与操作列自动折叠。
2. `DataTable` 的排序持久化（`sortStorageKey`）与动态单元格插槽语义。
3. `DateRangePicker` 的预设 key 回传与「精确 24 小时」换算。
4. `Toast` 的 store 驱动通道（返回 id、按类型默认时长）。
5. `VersionBadge` 的更新/重启/回滚状态机。
6. `ProxySelector` 的内嵌测速。
7. `HelpTooltip` 的 hover 桥接与 click 模式。
8. `NavigationProgress`。
9. `useSwipeSelect` 拖拽框选。
10. `useStepUp` + TOTP 弹层协议。
11. 业务徽章族、品牌图标、图表四件套。
12. driver.js 引导的选择器体系与交互式步骤。

---

## 8. UI 基础设施实现方案

### 8.1 `AppDataTable`

**目标**：让 28 个使用点以最小改动迁移，同时保持行为等价。

```ts
// 兼容旧契约
interface Column {
  key: string
  label: string
  sortable?: boolean
  class?: string
  formatter?: (value: any, row: any) => string
}
```

实现要点：

- 旧 `columns` → antd `columns`：`dataIndex = key`、`title = label`、`sorter` 按需、`customRender` 走 `formatter` 或 `#bodyCell`。
- 旧 `cell-{key}` 具名插槽：改为一组 `#bodyCell="{ column, record, index }"` 内的 `v-if` 分派；保留 `expanded` 参数。
- `header-{key}` 插槽：改用 `#headerCell`。
- `rowSelection`：对接 `useTableSelection` 的 `selectedSet`，`preserveSelectedRowKeys` 保持跨页选择。
- 排序：`serverSideSort` 时不本地排序，只 emit `sort(key, order)`；`sortStorageKey` 持久化到同一 localStorage 键。
- 分页：`useTableLoader` 的 `pagination` 直接映射 `a-pagination`（`showSizeChanger`、`showQuickJumper`、`pageSizeOptions`）。
- 粘性列：`fixed: 'left' | 'right'`；表头 `sticky`。
- 移动端：`Grid.useBreakpoint()` 判定 `< md (768)`，渲染卡片列表（沿用旧 `DataTable` 的移动端模板结构，替换为 `a-card` / `a-descriptions` / `a-empty` / `a-skeleton`）。
- 空态：`a-empty`，保留 `empty` 插槽。
- 虚拟滚动：默认不启用；对 `pageSize > 100` 的页面（用量、日志）**强制服务端分页 + 固定页大小上限 100**，或提供 `AppVirtualTable`（`@tanstack/vue-virtual` + antd 样式）作为专项。

**验收**：28 个使用点逐一对比「列、排序、筛选、选择、分页、空态、移动端、操作列折叠」。

### 8.2 表单

- 使用 `a-form` + `a-form-item`，校验规则改为 antd `rules`。
- 旧 `useForm` 保留「loading + 成功/失败 toast + 重新抛出」语义，内部改用 `a-form.validate()`。
- 复杂表单（`SettingsView` 9 个分组、`GroupsView` 7,057 行、`RiskControlView` 2,472 行、`AccountsView` 2,635 行）**先拆分子表单组件**，每个子表单一个 `a-form` 或同一 `formRef` 的 `name` 路径。
- 大表单的脏检查、保存作用域、未保存离开提示必须逐项核对。

### 8.3 弹窗 / 确认 / 通知

- `AppModal`：`v-model:open`，宽度五档映射（narrow 400 / normal 560 / wide 800 / extra-wide 1000 / full 96%）。
- `useConfirm()`：封装 `Modal.confirm`，返回 `Promise<boolean>`；危险操作支持「输入名称确认」变体。
- Toast：`App.vue` 内渲染 `<a-app>`，在子组件 `AppToastBridge.vue` 中 `App.useApp()` 取得 `message` 实例并注册到模块级单例；`appStore.showToast` 调该单例。**不要用静态 `message.xxx`**，它拿不到 ConfigProvider 主题上下文。
- 通知类（长文本、带跳转）用 `notification`；按现有调用点区分。

### 8.4 选择器与日期

- `AppSelect`：`options` 支持 `{ value, label, disabled, kind: 'group' }`；`creatable` 用 `a-select` 的 `showSearch` + 自定义 option；`remote` 用 `@search` + 300ms 防抖 + 丢弃过期响应；`selected` slot 用 `#labelRender`。
- `AppDateRangePicker`：`a-range-picker` + `presets`；值转换 `dayjs ↔ 'YYYY-MM-DD'`；`change` 事件额外回传 `preset` key；`disabledDate` 上限为明天。

### 8.5 移动端策略

| 断点 | 旧行为 | 新实现 |
|---|---|---|
| `<768px` | `DataTable` 卡片化；侧栏抽屉；`TablePageLayout` 恢复文档流 | `Grid.useBreakpoint()`；`a-drawer`；`PageLayout` 响应式 |
| `<1024px` | 侧栏默认收起；`TablePageLayout` 切换 `mobile-mode` | 同上，显式判断 `lg` 断点（antd lg=992，需按 1024 判定处单独处理） |
| 支付页 | 移动端跳转/二维码/唤起 App | 原逻辑保留 |

### 8.6 图表

- 保留 chart.js；新增 `AppChartCard`（`a-card` + 标题/操作区 + loading + 空态 + 固定高度）。
- 暗色配色由 `useTheme().isDark` 驱动，不再读 DOM class。
- 图表钻取表格（`UserBreakdownSubTable`）改用 `a-table`。

### 8.7 图标

- 建立 `ui/iconMap.ts`：旧 `Icon` 的 91 个 key → `@ant-design/icons-vue` 组件；无对应的保留 `Icon.vue`。
- 菜单图标、状态图标、支付图标分域管理。

### 8.8 引导

- 优先 `a-tour`；步骤文案复用 `components/Guide/steps.ts` 的 i18n key。
- 为每个锚点补 `id` 或 `data-tour`，逐条核对选择器。
- 交互式步骤（点击后自动 next）保留现逻辑；存储键不变。

### 8.9 加载与错误

- 路由级：`NavigationProgress` 自留；`a-spin` 用于区块级；`a-skeleton` 用于首屏占位。
- 页面级错误：`a-result` + 重试按钮。
- 全局 `router.onError` 的 chunk 加载失败重载逻辑原样保留（10 秒防抖）。
- 表单错误：`a-form-item` 的 `help` / `validateStatus`；接口错误走 `extractI18nErrorMessage`。

### 8.10 基础设施复刻清单（非 UI，但决定「功能是否等价」）

以下能力必须在新目录逐项复刻，不能因为「不是界面」而遗漏：

1. **axios 单例**：`baseURL` 规范化（相对/绝对）、`withCredentials`、30s 超时、`{code,message,data}` 解包。
2. **请求注入**：`Authorization: Bearer`、`Accept-Language`、GET 自动 `timezone`、`X-Admin-UI-Request` / `X-User-UI-Request`（含用户侧路径白名单）。
3. **错误规范化**：`{status, code, reason, error, message, metadata, retry_after_seconds}`；取消请求透传；网络错误 `status: 0`。
4. **401 全套**：单次重放、鉴权端点豁免、刷新不可用（0/429/5xx）不登出、会话被替换检测（`AUTH_SESSION_CHANGED`）、清理 4 个 token 键 + `sessionStorage.auth_expired` + 跳 `/login`。
5. **刷新并发控制**：同文档 in-flight 合并 + 跨标签 Web Locks + 对等轮询采用 + refresh token 轮转 + 写入顺序（access → expires_at → refresh_token）+ 用户 ID 防串号。
6. **特殊错误钩子**：`OPS_DISABLED`（缓存 + window 事件 + 重定向）、`ADMIN_COMPLIANCE_ACK_REQUIRED`（window 事件 + metadata）。
7. **SSE**：`fetch` + `ReadableStream` 手写按行解析（账户测试）；无 `EventSource`。
8. **WebSocket**：子协议 `["sub2api-admin", "jwt.<token>"]` 传鉴权、重连退避、心跳/陈旧检测、`REALTIME_DISABLED` 关闭码停止重连。
9. **`/setup/*` 独立客户端**：不走主 API 前缀与拦截器。
10. **app store**：全局 loading 计数、toast 队列、公共设置缓存 + 单 in-flight + `__APP_CONFIG__` 双通道 + 默认设置兜底、版本检查缓存。
11. **auth store**：5 个持久化键、60s 用户轮询、提前 120s 主动刷新、OAuth `setToken` 恢复、pending session 摘要、simple/backend mode。
12. **其余 store**：payment（config 去重）、subscriptions（TTL + 代次 + 5min 轮询）、announcements（20 分钟节流 + 弹窗队列 + 会话去重）、adminSettings（4 个 localStorage 开关镜像 + 事件同步）、adminCompliance（门禁 + 状态版本号）、onboarding（driver 全局实例）。
13. **路由守卫与标题解析**：见 §5 与 §11.7。
14. **预加载**：10 条邻接路由 + `requestIdleCallback`（Safari 兜底）+ 2s 超时。
15. **导航加载态**：单例 + 100ms 防闪烁。
16. **i18n 质量闸门**：en/zh schema 对齐、静态引用键全覆盖扫描、全量消息预编译、顶层键冲突检测（4 类测试必须重建）。
17. **格式化层**：`Intl` 数字/紧凑/货币/日期 + 相对时间走翻译键（52 个视图使用）。
18. **安全辅助**：`sanitizeUrl`、`sanitizeSvg`、错误消息 i18n 映射（`<namespace>.<REASON>` + metadata 插值）。
19. **构建产物契约**：`outDir`、`assets/<stem>-<8位>.ext` 指纹命名（后端据此设 immutable 缓存）、`index.html` 中 `</head>` / `<title>` / `<link rel="icon">` 必须可被后端字符串替换。
20. **构建期依赖**：`docs/legal/*.md?raw`（合规文档），Dockerfile 需继续拷贝 `docs/legal/` 并在 `.dockerignore` 保留。

---

## 9. 分批实施计划

每个批次以「页面可独立验收」为粒度，批次内的页面互不共享未迁移组件。批次顺序按「被依赖程度」排列：基建 → 认证 → 用户主链路 → 支付 → 管理端核心 → 管理端外围 → 设置 → 专项。

| 批次 | 内容 | 页面数 | 依赖 | 人日 |
|---|---|---:|---|---:|
| B0 | 基建：主题、布局、`AppDataTable`、`AppSelect`、`AppDateRangePicker`、`AppModal`、toast、confirm、图标映射、HTTP/store/router/i18n 复制 | 0 | — | 12 |
| B1 | 认证与初始化：Setup、登录、注册、邮箱验证、忘记/重置密码、7 个 OAuth 回调、MCP 授权、404 | 15 | B0 | 10 |
| B2 | 公开页：首页（3 模式）、Key 用量自查、模型广场、法律文档 | 4 | B0 | 4 |
| B3 | 用户核心：仪表盘、API Key、用量、兑换、推广、可用渠道、MCP、资料 | 8 | B0/B1 | 10 |
| B4 | 支付与订阅：购买页、订单、二维码、结果页、Stripe、Stripe 弹窗、Airwallex、我的订阅、微信支付回调 | 9 | B0/B3 | 10 |
| B5 | 渠道状态与批量绘图：渠道状态 V1/V2、批量绘图（含 IndexedDB 预览缓存） | 4 | B0/B3 | 6 |
| B6 | 管理端核心：仪表盘、用户、分组、账号 | 4 | B0 | 20 |
| B7 | 管理端运营：渠道、代理、订阅、兑换、优惠码、公告、审计、备份、插件、用量、推广 | 12 | B0/B6 | 18 |
| B8 | 管理端设置：9 个分组的大表单 | 1（拆 9） | B0 | 12 |
| B9 | 管理端专项：风控、提示词审计、渠道监控、Ops、订单与支付配置 | 8 | B0 | 18 |
| B10 | 收尾：移除 Tailwind、测试补齐、性能、切换演练 | — | 全部 | 10 |

**B0 是唯一的串行瓶颈**，必须先完成 `AppDataTable` 并用 3 个代表页验证（Users / Accounts / Usage），否则后续 28 个表格页会反复返工。

---

## 10. 逐页功能核对表

> 用法：迁移某页时复制该页条目到 PR 描述，逐项勾选。`□` 未做 / `☑` 已完成 / `—` 不适用。

### 10.1 公开页与认证（B1 / B2）

#### `/setup` — 首次安装向导（`SetupWizardView.vue`，686 行）
- □ 四步流程：数据库 → Redis → 管理员 → 完成；顶部进度点
- □ 数据库表单：host / port / user / password / dbname / sslmode（4 选项）
- □ Redis 表单：host / port / username / password / db / TLS 开关
- □ 连接门控：测试连接成功才允许下一步（`dbConnected` / `redisConnected`）
- □ 管理员表单：邮箱 / 密码 / 确认密码（≥8 位、一致性内联校验）
- □ 安装后轮询 `/setup/status`（60 次 × 1s）直到 `!needs_setup` → 跳 `/login`
- □ `server.port` 由 `window.location` 推导回填
- □ 独立 axios 客户端（网关根路径，不走 `/api/v1` 拦截器）

#### `/home` — 站点首页（`HomeView.vue`，744 行）
- □ 三套渲染模式：管理员自定义内容 / 紧凑首页（`compact_home_enabled`）/ 默认营销页
- □ 自定义内容：`home_content` 为 http(s) URL → iframe；否则 `v-html`（管理员配置，XSS 风险已接受）
- □ hero 区：站点名、副标题、logo（`sanitizeUrl`）
- □ 终端打字动画、供应商网格（Claude / GPT / Gemini 支持状态）
- □ 功能卡三块：统一网关、账号池、计费与配额
- □ 页脚：GitHub、文档链接、语言切换
- □ 登录态跳转：admin → `/admin/dashboard`，用户 → `/dashboard`
- □ 模型广场入口受 `FeatureFlags.modelPlaza` 与 `model_plaza_require_auth` 双重控制
- □ 主题切换写 `localStorage.theme`，首次跟随系统偏好
- □ 挂载时 `checkAuth` + `fetchPublicSettings`

#### `/key-usage` — 匿名 Key 用量自查（`KeyUsageView.vue`，1019 行）
- □ 查询卡：密码遮罩输入 + 显隐切换 + Enter 提交
- □ 请求 `buildGatewayUrl('/v1/usage')` + `Authorization: Bearer`
- □ 日期快捷：今天 / 7 天 / 30 天 / 自定义（两个 date input）；参数含 `timezone`
- □ 4 组 SVG 渐变进度环（`requestAnimationFrame` 增长；>90 红、>70 琥珀）
- □ 16 个统计格、每日用量表（7 列）、模型统计表（4 列）
- □ 60s 定时刷新额度重置倒计时
- □ loading 骨架、fade-up 入场、无数据/错误态
- □ `FeatureFlags.subscription` 切换「订阅类型 / 计费方式」标签

#### `/model-plaza` — 模型广场（`ModelPlazaView.vue` + 5 个子组件）
- □ `?embedded=1` 且已登录 → 内嵌 AppLayout；否则独立页 + `PlazaNavBar`
- □ 筛选：平台 / 分组 / 倍率三行互斥联动；模型名搜索 + 清除
- □ 定价表 8 列：模型、付费（输入/输出/缓存）、官方（输入/输出/缓存）、倍率
- □ 有效倍率 = `user_rate_multiplier ?? rate_multiplier`
- □ 计费模式：token / image / video；时段定价周期行 + 星期徽标；阶梯区间；缓存写 1h/12h；边际标记；推理强度倍率徽标
- □ 分组排序（倍率 → 名称）、专属/订阅徽标、高峰倍率说明、长上下文说明
- □ 空态两分：无结果 vs 无数据；匿名提示；导航骨架屏
- □ 匿名访问受 `model_plaza_require_auth`，跳转带 `?redirect=/model-plaza`

#### `/legal/:documentId` — 法律文档（`LegalDocumentView.vue`）
- □ 顶部 logo + 站名（加载骨架）+ 登录按钮
- □ 正文标题区：类型标签、标题、更新时间
- □ marked(breaks+gfm) + DOMPurify 渲染
- □ 标题关键字决定图标（政策/隐私 → shield；国家/地区 → globe；特定 → cog）
- □ 四种状态：加载中 / 加载失败 / 文档不存在 / 内容为空
- □ 内置中英合规文档 + `login_agreement_documents` 两来源

#### `/404` — `NotFoundView.vue`
- □ 错误码、说明、`router.back()`、回仪表盘链接

#### `/login` — 登录（`LoginView.vue`，747 行）
- □ 邮箱 / 密码（显隐、autofocus、autocomplete）
- □ 提交禁用条件：`!publicSettingsLoaded`、协议未勾选
- □ 验证码三选一：Cloudflare Turnstile / 腾讯天御 / 阿里云
- □ 通行密钥登录：`PublicKeyCredential` 能力检测 + `passkey_enabled` 双显隐
- □ OAuth 区：GitHub / Google / LinuxDO / 钉钉 / 微信 / OIDC；`backend_mode_enabled` 时整块隐藏
- □ 微信 OAuth 按钮按 UA/配置给出不可用原因（仅外部浏览器 / 仅微信内 / 仅原生 App / 未配置）
- □ 忘记密码入口：`backend_mode` 下隐藏
- □ TOTP 二步弹窗（`temp_token` + 掩码邮箱，6 位自动提交）
- □ 登录协议三态（checkbox / 提示条 / 全屏弹窗）+ revision 存 localStorage
- □ `sessionStorage.auth_expired` 过期提示（展示一次后清除）
- □ `?redirect=` 回跳（`sanitizeRedirectPath`）
- □ 登录前清除 affiliate 推荐码
- □ 错误走 `extractI18nErrorMessage(error, t, 'auth.errors')`

#### `/register` — 注册（`RegisterView.vue`，1133 行）
- □ 邮箱 / 密码 / 确认密码（眼睛切换 + `passwordHint`）/ 邀请码 / 推广码 / 验证码
- □ 邀请码 500ms 防抖校验 + 绿/红内联态 + spinner；`affiliate_enabled` 时改为可选推广码
- □ 促销码防抖校验 + 错误码映射（NOT_FOUND / EXPIRED / DISABLED / MAX_USED / ALREADY_USED）+ 赠金额提示
- □ 注册关闭横幅
- □ OAuth 按钮透传 `aff-code` / `promo-code`；URL 支持 `?aff` / `?aff_code` / `?promo`
- □ `email_verify_enabled`：`register_data` 存 sessionStorage → 跳 `/email-verify`
- □ 域名配额模式下跳过邮箱后缀白名单预检
- □ 错误码 `EMAIL_DOMAIN_REGISTRATION_LIMIT`、`EMAIL_BLOCKED`

#### `/email-verify` — 邮箱验证（`EmailVerifyView.vue`，807 行）
- □ 6 位验证码（`one-time-code`、`inputmode=numeric`、maxlength 6、字间距）
- □ 挂载即发送；重发倒计时用服务端 `countdown`
- □ 重发与「创建账号」两个独立验证码实例
- □ pending OAuth 分支：`sendPendingOAuthVerifyCode`；`auth_result === 'pending_session'` 跳对应 provider 回调
- □ 创建账号：`/auth/oauth/pending/create-account`，载荷含沿用决定（显示名/头像）+ 邀请码 + aff
- □ `clearStoredCaptchaProof`；返回注册清 sessionStorage

#### `/forgot-password`、`/reset-password`
- □ 忘记密码：邮箱 + 验证码 → 成功卡片 → 回登录
- □ 重置密码：`?email&token`；缺失显示「链接无效」；新密码 ≥6 位 + 一致性；`INVALID_RESET_TOKEN` 单独处理

#### OAuth 回调（7 条路由）
- □ `/auth/callback`（别名 `/auth/oauth/callback`）：hash fragment 取 token；pending provider 转发后端；`resumePendingEmailOAuth`；注册补全（只读邮箱 + 密码 + 邀请码）；code / state / fullUrl 三个复制按钮；`?error` 监听
- □ `/auth/linuxdo/callback`、`/auth/oidc/callback`、`/auth/wechat/callback`、`/auth/dingtalk/callback`：四套同构状态机
  - □ fragment 直接登录 / `invitation_required` + `pending_oauth_token` / `exchangePendingOAuthCompletion`
  - □ 沿用选择（显示名、头像复选框）
  - □ `choose_account_action` 账号选择器
  - □ `create_account`（`PendingOAuthCreateAccountForm`，5 页复用）
  - □ `bind_login`（邮箱 + 密码）
  - □ 2FA 6 位挑战
  - □ bind vs login 完成态（`getOAuthCompletionKind`）
- □ 微信额外：当前会话绑定（`prepareOAuthBindAccessTokenCookie` + `/auth/oauth/wechat/bind/start`）；`wechat_bind_existing=1` 经 `/login?redirect=…` 恢复
- □ 钉钉额外：`email_completion` 跳转、`?bind=1` 覆盖、`auth.dingtalk.error.*` 分类文案
- □ OIDC 额外：`oidc_oauth_provider_name` 展示名
- □ `/auth/dingtalk/email-completion`：单表单提交；`REGISTRATION_DISABLED` / `choose_account_action_required` / `existing_account_bindable` 三种跳转
- □ `/auth/wechat/payment/callback`：先 hash 后 query；错误键兼容 `error`/`err_msg`/`errmsg`；需 `wechat_resume_token` 或 `openid`；`/payment*` → `/purchase*` 规范化；转发 state/scope/payment_type/amount/order_type/plan_id

#### `/oauth/mcp/authorize` — MCP 授权（`MCPOAuthAuthorizeView.vue`）
- □ PKCE 查询参数强校验；`mcpAPI.previewAuthorization`
- □ 展示 client 名称 / client_id、redirect origin + 完整 URI、resource
- □ scope 下拉（`user`；admin 含 `admin`）、readOnly 下拉
- □ 管理员写权限走 `stepUp.run`（TOTP）
- □ generation 计数使过期请求失效
- □ 跳转前校验 origin / path
- □ ARIA role / id 完整

### 10.2 用户端（B3）

#### `/dashboard` — 用户概览
- □ `UserDashboardStats`：余额、Key 数、今日请求/费用、今日/总 token、性能、平台分布、平台配额
- □ `UserDashboardCharts`：日期范围、粒度 day/hour、刷新
- □ `UserDashboardRecentUsage`：近 7 天前 5 条
- □ `UserDashboardQuickActions`：创建 Key / 查看用量 / 批量绘图（动态显隐）/ 兑换码
- □ 数据源 5 个接口；进入时 `refreshUser()`

#### `/keys` — API Key 管理（`KeysView.vue`，2114 行）
- □ 筛选：搜索、分组、状态（全部/未激活/激活/额度耗尽/已过期）
- □ 表格 13 列：Key（遮罩 + 复制反馈）、IP 限制盾牌、分组（点击弹搜索下拉）、并发 pill、今日/总用量 + 配额条、5h/1d/7d 限速条（重置 + 倒计时）、过期时间（过期标红）、状态徽标、最近使用、最近 IP、创建时间、操作
- □ 批量：勾选条（已选数 / 清空）、`BulkEditKeysModal`（分组 / 状态 / 配额 / 三项限速 / 过期或永不过期 / IP 黑白名单逐项开关；部分失败保留失败项）
- □ 列设置：勾选隐藏列 + 持久化 + 版本号
- □ 服务端排序
- □ 弹窗：创建/编辑（平台单选卡仅创建时、自定义 Key ≥16 位 `^[a-zA-Z0-9_-]+$`、IP 黑白名单 textarea、配额 + 已用 + 重置、限速三项 + 用量条、过期开关 7/30/90/自定义）、删除确认、重置配额、重置限速、`UseKeyModal`、CC Switch 客户端选择
- □ `EndpointPopover` 端点复制（1.8s 恢复）+ 测速链接
- □ deeplink 失败检测（`document.hasFocus()` 100ms）
- □ 60s tick 刷新倒计时；click-outside 关闭下拉

#### `/usage` — 用量与错误（`UsageView.vue`，937 行）
- □ 统计卡、日期范围 + 粒度、4 图表（模型/分组/端点/趋势）含指标切换
- □ 筛选：API Key、模型、分组、request_type、compaction、billing_type（受订阅开关）、billing_mode
- □ 工具：刷新、重置、列设置、导出 CSV
- □ 错误 tab（受 `allow_user_view_error_requests`）：筛选 key/model（可创建）/category/status；`UserErrorRequestsTable` 行点击 → `UserErrorDetailModal`
- □ 两套列偏好 key：`user-usage-hidden-columns`、`user-usage-error-hidden-columns`
- □ 导出：1000 条/批、429 重试 3 次（≤60s）、带 BOM
- □ AbortController + 序号守卫

#### `/redeem` — 兑换码
- □ 余额/并发头部、兑换表单、结果卡（余额/并发/订阅含分组与有效期）、错误卡、规则卡、历史列表
- □ 历史：类型图标与配色、管理员调整备注、每页 20/50/100 + 上/下页
- □ 成功后 `refreshUser()` + 订阅强制刷新

#### `/affiliate` — 推广返利
- □ 4 张统计卡（返利比例、邀请人数、可用额度、累计 + 冻结）
- □ 邀请码与邀请链接复制
- □ 提示列表（第 4 条条件显示）
- □ 转移额度按钮（为 0 时禁用）
- □ 表格：被邀请人邮箱 / 用户名 / 返利 / 加入时间

#### `/available-channels` — 可用渠道
- □ 搜索 + 刷新 + `AvailableChannelsTable`
- □ 5 列：名称、描述、平台、分组、支持模型
- □ 分组区专属/公开徽标与倍率
- □ 用户分组倍率覆盖默认；接口失败降级

#### `/settings/mcp` — MCP 接入
- □ 端点展示 + 复制 + 重试
- □ 3 步接入说明
- □ 已授权应用列表（名称、scope、readOnly、状态）
- □ 撤销授权弹窗（撤销中禁用 Esc；乐观更新；`unmounted` 守卫）

#### `/profile` — 个人资料（11 个子组件）
- □ `ProfileInfoCard`：头像/用户名/角色/余额/并发/注册时间；基础资料编辑；OAuth 绑定来源说明
- □ `ProfileIdentityBindingsSection`：邮箱/linuxdo/钉钉/OIDC/微信绑定卡片；绑定/解绑；邮箱更换（验证码 + 密码）；绑定数量
- □ `ProfileAvatarCard`：上传（类型/大小校验、前端压缩）、保存、删除
- □ `ProfilePasswordForm`：当前密码 / 新密码 / 确认（≥6、一致）
- □ `ProfileBalanceNotifyCard`：开关、阈值（含系统默认提示）、额外邮箱增删 + 逐个发送/校验验证码（60s 倒计时）、上限提示
- □ `ProfileTotpCard` + `TotpSetupModal`（验证码 + 密码 → 二维码 + 手动密钥复制 → 6 位确认）+ `TotpDisableDialog`
- □ `ProfilePasskeyCard`：新增（名称 + 密码 → WebAuthn）、列表（同步徽标、创建/最近使用）、重命名、删除（密码确认）、不支持/关闭提示

#### `/subscriptions` — 我的订阅
- □ 卡片头：分组名、平台徽标与色点、描述、倍率、高峰倍率、状态徽标、立即续费（`/purchase?tab=subscription&group=`）
- □ 到期展示：日期 + 今日/明日/剩余天数；≤3 天红、≤7 天橙
- □ 额度进度：时 / 日 / 周 / 月四档（≥90 红、≥70 橙）+ 重置倒计时
- □ 一次性日配额 → 显示「额度剩余时长」；全档未配置 → ∞ 无限卡；空态卡

#### `/monitor` — 渠道状态（V1 / V2 分派）
- □ `ChannelStatusView` 按 `isChannelMonitorV1Mode()` 切换
- □ V1：`MonitorHero`（整体状态、窗口 7d/15d/30d、手动刷新、自动刷新开关 + 间隔 30/60/120s + 倒计时）、`MonitorCardGrid`、`MonitorDetailDialog`（模型表 7 列）
- □ V1 刷新：`useAutoRefresh`（`channel-status-auto-refresh` 持久化，默认开）；`document.hidden` / loading 暂停；`channel_monitor_enabled === false` 停止；AbortController；非 7d 窗口预取详情
- □ V2 顶栏：更新时间、更新中、部分覆盖徽标、bootstrap 进度条
- □ V2 筛选：时间 90m/24h/7d/30d；平台/分组/模型多选（级联软裁剪，空选项不误清）；矩阵分组维度 4 种；趋势 pulse/line；健康模式 4 种
- □ V2 KPI：成功率、TTFT P50（AVG·P90）、tokens/s、缓存命中率、RPM（后两者受隐藏开关）
- □ V2 图表：`RelayPulseMatrix` / `MonitorTrendChart`（含覆盖度）
- □ V2 tabs：模型表（6 列，行下钻）、错误分类（可展开，管理员见详情）、用户榜（排名徽标、本人高亮，受隐藏开关）
- □ V2 筛选全部写入 URL query
- □ V2 轮询：bootstrap 10s，否则 `refresh_interval_seconds`（下限 60s，默认 300s）；`sequence` + AbortController 丢弃过期响应

#### `/batch-image` — 批量绘图（`BatchImageGuideView.vue`，2693 行）
- □ 筛选：任务名、API Key、状态（8 种）、下载状态；重置 / 刷新 / 使用指南 / 新建
- □ 批量：全选可见（indeterminate）、批量下载 zip、批量删除
- □ 表格 9 列：选择、任务名（父子展开 + 子任务徽标/数量）、模型、API Key、状态、成功/失败/总数、费用、下载状态、操作
- □ 行操作：详情、下载 zip、更多菜单（重试失败项 / 删除记录；Teleport 定位；点击/resize/scroll 关闭）
- □ Prompt 悬浮预览（延时开/关、可复制）
- □ 详情弹窗：4 格统计 + 条目表 5 列 + 刷新 + 取消 / 重试失败 / 下载 zip
- □ 条目预览：IndexedDB 缓存（`sub2api-batch-image-preview-cache`，3 天 / 120 条 / 48MB，WebP 360px，每小时清理）+ 放大预览
- □ 新建弹窗：任务名 ≤255、API Key（仅 gemini + `allow_batch_image_generation`）、模型（按 Key 动态）、尺寸 1K、格式 PNG/JPEG/WebP、Prompt 编辑（custom_id、每 prompt 1–4、参考图上传含上限、预计输出数）、限制与取消提示
- □ 使用指南弹窗（只读 + 一键复制）
- □ 轮询 8s，终态停止
- □ 分页 20/50/100；`has_more` 上下页
- □ 错误码映射 8 种

#### `/custom/:id` — 自定义页（`CustomPageView.vue`）
- □ 数据源 `custom_menu_items`；管理员回退 `adminSettingsStore`
- □ Markdown 模式（`page_slug` 或 `url` 以 `md:` 开头）：TOC 侧栏（h1–h4、rAF 节流高亮、≤640 收起、平滑滚动）、相对图片改写 `/pages/{slug}/images/…`、DOMPurify 允许 iframe、代码块复制按钮（2s）
- □ iframe 模式：`buildEmbeddedUrl` 注入 userId/token/theme/locale；可拖拽「新窗口打开」悬浮按钮（pointer capture、4px 阈值、边界限制、resize 重算）；`hide_open_button`
- □ 状态：未找到菜单 / URL 未配置 / 加载中
- □ MutationObserver 同步主题

### 10.3 支付与订阅（B4）

#### `/purchase` — 充值 / 购买（`PaymentView.vue`，1226 行）
- □ 一次 `checkout info` 拉账户与配置；tab 充值/订阅（`balance_disabled` + 订阅开关；watcher 收敛非法 tab）
- □ 充值：账户卡、markdown 赠送说明、`AmountInput`（预设 10…5000、min/max 过滤、赠金阶梯 + 第二行、倍率、币种）、`PaymentMethodSelector`、金额明细（优惠/赠送、手续费率、实付、到账、倍率预览）
- □ 订阅：套餐网格、内联确认（倍率/限额/高峰）、费用明细、当前订阅列表、`?group=` 续费弹窗、帮助 markdown + 图片预览遮罩
- □ 支付阶段：`PaymentStatusPanel`；`createOrder` + `decidePaymentLaunch` 六分支 —— wechat_oauth / stripe_popup / stripe_route / airwallex_route / wechat_jsapi（`WeixinJSBridge` 4s 等待，取消/失败回退）/ redirect_waiting
- □ 兜底：`attemptMobileQrFallback`（`isMobile:false` 重试 + 警告 toast）
- □ 专属错误：`TOO_MANY_PENDING`、`CANCEL_RATE_LIMITED`
- □ 恢复快照写入/读取；`resumeWechatPaymentFromQuery`；`isMobileDevice()` + MicroMessenger UA

#### `PaymentStatusPanel`（支付进行中面板）
- □ 终态卡三态：success（订单号/金额/赠送/实付）、cancelled、expired
- □ 二维码：canvas 220px + 品牌角标、扫码提示、30 分钟倒计时（无 `expires_at` 回退）、取消订单
- □ 移动支付宝 deep link 三态（launching / backgrounded / fallback）；2200ms（内嵌浏览器 300ms）回退二维码；重新唤起、保存二维码 PNG
- □ 轮询 3s + `pollInFlight` 防重入；PENDING 时 `verifyOrder` 二次确认（15s 间隔、最多 6 次）；终态忽略迟到响应
- □ 弹窗模式：等待提示 + 「打开支付窗口」（失败整页跳转）

#### `/payment/qrcode` — 独立扫码页
- □ `QRCode.toCanvas` + 居中 logo（纠错级 M）
- □ 轮询 3s + `pollInFlight` + 清理守卫；倒计时由 `expires_at`（回退 30 分钟）
- □ 分支：COMPLETED/PAID → 结果页；EXPIRED/CANCELLED/FAILED → 过期 UI；取消订单 + 打开支付窗口

#### `/payment/result` — 支付结果
- □ 解析链：`resume_token` → `order_id` → 公开 `out_trade_no`
- □ 状态集：成功 {COMPLETED, PAID, RECHARGING}；等待 {PENDING, CREATED, WAITING, PROCESSING}
- □ 轮询 2s × 最多 15 次；完成后余额类订单仅刷新一次余额
- □ 金额明细（基础/手续费/实付/赠送/到账）、支付方式 i18n、状态徽标
- □ EasyPay `returnInfo` 旧版回退
- □ 终态清除恢复快照；返回充值 / 查看订单

#### `/payment/stripe`、`/payment/stripe-popup`、`/payment/airwallex`
- □ Stripe：`?method` 弹窗模式；`client_secret` 只从匹配快照取；`loadStripe` 懒加载
- □ Stripe 分支：alipay（`confirmAlipayPayment`）、wechat_pay（mobile_web/web + `image_data_url` 二维码 + 3s 轮询）、其他（Payment Element tabs + `confirmPayment({redirect:'if_required'})`）
- □ Stripe 收尾：`scheduleClose`（有 opener 关窗，否则 2s 后跳结果页）；卸载清理
- □ Stripe 弹窗：postMessage 握手（`STRIPE_POPUP_READY` / `STRIPE_POPUP_INIT`）+ origin 校验 + 15s 超时；3s 原始 fetch 轮询（读 `localStorage.auth_token`）+ in-flight 守卫；成功后自动关窗
- □ Airwallex：快照校验；动态 import SDK；`init({env, enabledElements:['payments'], locale})`；`redirectToCheckout` 后 `location.assign`

#### `/orders` — 我的订单
- □ 状态筛选、刷新、返回充值
- □ `OrderTable`：订单号、外部单号、实付（含手续费率/到账/赠送）、支付方式、状态、创建时间、操作
- □ 取消订单（仅 PENDING）、申请退款（仅 COMPLETED 且 provider 在 `getRefundEligibleProviders`，需填原因）
- □ 分页

### 10.4 管理端核心（B6）

#### `/admin/dashboard` — 管理概览（`DashboardView.vue`，777 行）
- □ 8 张统计卡（用户 / 账号 / 请求 / Token / 成本 / 渠道健康等）
- □ 快捷操作入口
- □ 日期范围 + 粒度（hour / day / week）
- □ 3 张图表：请求量折线、成本/收入折线、模型或渠道分布
- □ 空态 `EmptyState`

#### `/admin/users` — 用户管理（`UsersView.vue`，1912 行）
- □ 21+ 列，含动态 `attr_*` 自定义属性列
- □ 筛选：role / status / group（可创建）/ apiKeyGroup / 属性
- □ 列显示开关（版本化 + 强制可见列 + 已移除列迁移）
- □ 服务端排序 + `sort-storage-key`
- □ 批量：修改额度/并发限额、批量删除（6 条/批分片，进度与失败计数回显）
- □ 10 个弹窗：编辑用户、余额调整、余额流水、API Key、可用分组、平台配额、分组替换、重置密码等
- □ 危险操作二次确认

#### `/admin/groups` — 分组管理（`GroupsView.vue`，7057 行）
- □ 表格列：name / id / platform / account_count / status / actions；可开关 billing_type / rate_multiplier / is_exclusive / capacity / usage
- □ 筛选：关键词、platform、status、is_exclusive；分页 + pageSize 持久化；列显示开关
- □ 新建/编辑超大表单：
  - □ 模型白名单
  - □ 定价五套：图片 / 视频 / 语音 / 联网搜索 / 模型 / 显式计费
  - □ 峰时倍率（启用/起止/倍率）
  - □ 利润控制
  - □ OpenAI Fast / Live / Messages 映射
  - □ 模型路由与复合路由（`Composite Routes Modal`）
  - □ 账号筛选条件
  - □ claudeCode 回退
  - □ mcpXml（仅 antigravity）
  - □ Claude Code 客户端限制（仅 anthropic）
  - □ Codex 网页搜索按次计费（仅 openai）
  - □ Grok Voice 显式定价（仅 grok）
  - □ supportedScopes
  - □ 订阅配置与限额（仅选订阅类型时显示）
- □ 弹窗：排序（`VueDraggable` 拖拽）、复制分组、删除确认、`GroupRateMultipliersModal`（用户搜索 + 批量倍率 + 清空）、`GroupRPMOverridesModal`
- □ 平台条件显隐：antigravity / anthropic / openai / grok / composite 各自专属区块
- □ `HelpTooltip` 富文本气泡（多处）

#### `/admin/accounts` — 上游账号（`AccountsView.vue`，2635 行）
- □ 19 列（用量/配额/优先级/容量/重置次数/推荐/余额等专用单元格）
- □ 筛选：platform / type / status / privacy_mode / group
- □ 服务端排序 + `sort-storage-key`；列显示开关
- □ 自动刷新 5/10/15/30s + 倒计时（`useAutoRefresh`）
- □ 批量栏：选中本页 / 选中全部结果（`fetchAllAccountIds`）、删除、重置状态、刷新 token、探测上游计费、启用/停用调度、按选中或按筛选结果批量编辑
- □ 13 个弹窗：新建（`CreateAccountModal` 7.8k 行）、编辑（6.4k）、批量编辑（2.5k）、统计、导入、定时测试、TLS 指纹、错误透传规则、合规提示等
- □ 账号表单多页签：基础 / 凭证 / OAuth / 限额 / 高级 / 调度；大量条件字段与联动校验
- □ `OAuthAuthorizationFlow`：生成授权链接 → 打开授权 → 粘贴回调，正则提取 code/state 并校验一致性
- □ 行内探针（每行独立「查询」按钮 + 独立 loading）：
  - □ `AccountUsageCell`、`AccountTodayStatsCell`
  - □ `OpenAIQuotaResetCell`（查询/重置/积分）
  - □ `ClaudeResetCreditsCell`（查询/重置/到期）
  - □ `CNProviderQuotaCell` / `CNProviderBalanceCell`（5h/周/月 + 余额）
  - □ `GrokQuotaProbeCell`、`OpenAIReferralCell`（可用数/邀请弹窗）
  - □ `OllamaCloudUsageCell`、`OpenCodeGoUsageCell`
  - □ `AccountPriorityCell`（快速上调/编辑）、`AccountCapacityCell`（窗口成本/会话数/RPM 分级着色）
  - □ `AccountGroupsCell`、`UpstreamBillingRateCell`
- □ 更多工具下拉：导出 JSON（TOTP step-up）、导入 JSON（拖拽多文件）、探活
- □ `ScheduledTestsPanel` 批量测试后按行回填结果
- □ 行操作菜单（`AccountActionMenu`，Teleport 定位 + 视口夹取）

### 10.5 管理端运营（B7）

#### `/admin/channels/pricing` — 渠道与定价（`ChannelsView.vue`，1743 行）
- □ 基础页签 + 按平台分页签
- □ 模型映射行编辑（源模型 → 目标模型），`ModelTagInput` 回车创建 / 批量粘贴
- □ `PricingEntryCard` / `IntervalRow` / `TimePricingSection`：按次 / 按 token / 分时段 / 区间阶梯定价，编辑态实时联动总价
- □ 账号维度统计定价规则表

#### `/admin/proxies` — 代理池（`ProxiesView.vue`，2089 行）
- □ 12 列（地址/协议/认证/地区/延迟/质量分/绑定账号数/状态等）
- □ 筛选 + 分页
- □ 批量测试（并发 3）、批量质量检测（并发 5）、批量删除
- □ 新建弹窗：单条表单 或 批量 textarea 粘贴解析
- □ 复制 URL 多格式菜单；密码显隐
- □ 质量报告弹窗、绑定账号弹窗
- □ JSON 导入 / 导出

#### `/admin/subscriptions` — 订阅管理（`SubscriptionsView.vue`，1699 行）
- □ 表格含日/周/月用量窗口进度条；可切换「用户维度」列模式
- □ 操作：分配（批量选用户）、调整、撤销、恢复、重置额度
- □ `BulkSubscriptionActionDialog`（分片提交 + 逐条结果汇总 + 失败项可重试）
- □ 订阅说明引导弹窗

#### `/admin/redeem` — 兑换码（`RedeemView.vue`，1197 行）
- □ 表格：码/面值/类型/状态/使用者/过期时间/操作；筛选 + 分页 + 多选
- □ 生成弹窗（数量/面值/前缀/有效期）
- □ 批量更新弹窗
- □ 删除未使用码（二次确认）
- □ 生成结果弹窗（全选复制）
- □ CSV 导出；单码复制

#### `/admin/promo-codes` — 推广码（`PromoCodesView.vue`，747 行）
- □ 表格 + 分页
- □ 新建/编辑/使用记录弹窗
- □ 复制码、复制注册链接（带 ref 参数）
- □ 删除确认

#### `/admin/announcements` — 公告（`AnnouncementsView.vue`，625 行）
- □ 表格：标题/类型/目标范围/发布时间/已读/操作
- □ 预览弹窗、已读状态弹窗（`AnnouncementReadStatusDialog`）
- □ 新建/编辑：标题、内容 textarea、目标（全部/指定分组/指定用户，`AnnouncementTargetingEditor`）、定时发布、置顶/弹窗开关

#### `/admin/audit-logs` — 审计日志（`AuditLogView.vue`，686 行）
- □ 表格：时间/操作者/动作/结果状态码/耗时/客户端 IP
- □ 时间范围筛选 + 自定义时间弹窗 + 分页
- □ 详情弹窗：请求/响应 JSON 只读展示 + 变更前后 diff 区块
- □ 清空日志：`ConfirmDialog` → 输入 6 位 TOTP 的弹窗

#### `/admin/backup` — 备份恢复（`BackupView.vue`，952 行）
- □ 备份列表（时间/大小/类型/状态）+ 手动备份、下载（多分片）、恢复、删除
- □ S3 配置 + 图床存储 + 定时策略（`BackupArchiveSettings`）
- □ 两个轮询器：备份进度 / 恢复进度
- □ `TotpStepUpDialog` 二次验证

#### `/admin/plugins` — 插件管理（`PluginsView.vue`，734 行）
- □ 插件列表（启用开关、版本、状态）
- □ 配置弹窗为沙箱 iframe（插件自带 UI）；宿主提供保存 / 测试 / 灰度百分比滑块
- □ 配置测试轮询
- □ `TotpStepUpDialog` 保护敏感操作

#### `/admin/usage` — 用量分析（`UsageView.vue`，887 行）
- □ 三页签 usage / errors / ranking（懒加载）
- □ `UsageFilters` 14 个筛选条件
- □ 两套列下拉（明细表 + 错误表），列显示持久化
- □ 4 张图表
- □ `UserTokenRanking`（可切换排序字段与条数）
- □ Excel 导出（file-saver + 进度 + 中断）
- □ `UsageCleanupDialog` 清理历史数据

#### `/admin/affiliates/{invites,rebates,transfers}` — 推广记录
- □ `AdminAffiliateRecordsTable`：邀请人/被邀请人/邀请码/返佣金额/邀请时间/订单列（全部可排序）；起止日期筛选；概览弹窗
- □ `AffiliateOfflineWithdrawDialog`：线下提现（用户选择/切换、金额、备注）
- □ 三页共用一张表，仅筛选条件与列不同

### 10.6 系统设置（B8）

#### `/admin/settings` — 9 个页签（`SettingsView.vue`，13,415 行）
字段类型统计：number 53 / url 17 / password 14 / checkbox 5 / textarea 5 / select 4 / email 3 / radio 2 / date 2。

**general**
- □ `adminApiKey`（password + 显示/复制/重置）
- □ `site`：site_name / subtitle / api_base_url / contact_info / doc_url、site_logo（`ImageUpload`）、home_content（textarea）、compact_home_enabled / hide_ccs_import_button / backend_mode_enabled 开关、表格偏好（默认页大小 + 可选页大小）
- □ `customMenu` 自定义菜单行编辑器（标题/图标/链接/排序/可见性，增删行）
- □ `defaults` + `authSourceDefaults`：新用户默认余额/额度/分组/并发，按登录来源分别设默认
- □ `scheduling`：调度策略、粘性会话、失败切换、优先级/权重
- □ `usageRecords`：保留天数、清理开关、统计粒度
- □ 登录方式：`linuxdo`（client id/secret/回调/开关）、`oidc`（issuer / client_id / secret / scopes / claim 映射 / 自动建号 / 默认分组，20+ 字段）、`captcha`（turnstile / tencent / aliyun 三套 key + secret + 开关）
- □ `registration`：注册开关、邮箱后缀白名单、域名配额、邀请码、推广码、密码重置、frontend_url
- □ `apiKeyAcl`（IP/网段白名单）、`panelRateLimit`、`overloadCooldown`、`rateLimit429Cooldown`、`streamTimeout`、`rectifier`、`betaPolicy`、`openaiFastPolicy`（含 `OpenAIFastPolicyUserSelector`）

**gateway**
- □ `claudeCode`：Claude Code 客户端兼容开关 / 伪装参数 / 版本号
- □ `gatewayForwarding`：Codex 加固行表格 + 指纹信号行表格，逐行开关
- □ `upstreamBillingProbe`：探测间隔 / 超时 / 并发
- □ `ollamaCloudUsage`、`opencodeGoUsage`：额度轮询配置
- □ `webSearchEmulation`：provider / endpoint / api key / 最大结果数

**agreement**
- □ 登录协议 Markdown textarea + 预览

**features**
- □ 功能开关列表：渠道监控、可用渠道、模型广场、插件管理、风控、分销、支付、邮箱验证、Cyber 会话拦截、后端模式

**security**
- □ `totp_enabled`、`passkey_enabled`、`step_up_enabled`、`session_binding_enabled`、`audit_log_retention_days`、`email_verify_enabled`
- □ 注册邮箱黑名单编辑器（`RegistrationEmailBlacklistEditor`，行内增删）
- □ 分销设置卡：返佣比例 / 冻结小时 / 有效期天数 / 单人上限 + 自定义用户表格（批量勾选 + 增删改弹窗）
- □ `AdminAccessKeysSettings`

**users**
- □ 默认用户额度 / 并发 / 平台配额默认值 / 用户属性默认值

**payment**
- □ `PaymentProviderList`（`VueDraggable` 拖拽排序）+ `PaymentProviderDialog`（凭据表单，按 provider 动态字段）
- □ 充值赠送阶梯编辑器（`RechargeBonusTierEditor`）
- □ 订阅计划入口
- □ USD→CNY 汇率与手续费

**email**
- □ SMTP（host/port/username/password/加密方式/发件人）
- □ 邮件模板（`EmailTemplateEditor`，724 行）
- □ 邮箱 OAuth（github / google 两组 client id/secret/回调）
- □ `wechatConnect`：open / mp / mobile 三套 appid+secret
- □ `dingtalk`：corp id/secret/agent id + 同步显示名/企业邮箱/部门开关

**backup**
- □ 备份调度（cron/周期、保留份数、启用）+ S3 + 图床存储（`BackupArchiveSettings`）

**横切**
- □ 主表单一次性保存 + 约 10 个卡片级独立保存函数（webSearch / upstreamBillingProbe / ollamaCloudUsage / openCodeGoUsage / overloadCooldown / panelRateLimit / rateLimit429Cooldown / streamTimeout / rectifier / betaPolicy），互不影响
- □ `loadFailed` 时禁用保存
- □ 底部固定保存条（`settings-save-dock`，backup 页签隐藏）
- □ 大量 `required` + number 范围校验
- □ `GroupBadge` / `ProxySelector` / `Toggle` / `ImageUpload` 在设置中的复用

### 10.7 管理端专项（B9）

#### `/admin/risk-control` — 风控（`RiskControlView.vue`，2472 行）
- □ 概览指标卡、预拦截同步、API Key 载入、Worker 池网格
- □ 日志表 10 列 + 筛选，`setInterval` 轮询
- □ 命中详情弹窗
- □ 设置弹窗 10 个页签共享同一草稿，保存整体提交：基础 / 引擎 / API Key / 审计测试图片 / 分组范围 / 模型过滤 / Worker 数 / 保留期 / 阈值 / 关键词
- □ 联动校验

#### `/admin/prompt-audit` — 提示词审计（`features/prompt-audit/`）
- □ events / config 双页签；config 底部固定保存条 + 脏态显示，离开页签保留草稿
- □ `RuntimeOverview`、`PolicyPanel`、`EndpointPool`、`EventWorkspace`（列表与筛选）、`EventDetailDialog`、`FilterDeleteDialog`（按条件批量删除）
- □ 拦截策略变更需 `ConfirmDialog` 确认；事件删除二次确认
- □ `viewModel.ts` + `api.ts` 状态与请求分离

#### `/admin/channels/monitor` — 渠道监控（`ChannelMonitorView.vue` + `features/channel-monitor-v2`）
- □ v2 / legacy 双模式（`isChannelMonitorV1Mode`）
- □ `MonitorFiltersBar` 筛选 + 分页
- □ `MonitorFormDialog`：名称、检测模式、provider、绑定账号、apiMode、endpoint、apiKey、主模型、附加模型、分组名
- □ 模板管理弹窗（`MonitorTemplateManagerDialog` / `ApplyPickerDialog`）
- □ 运行结果弹窗（`MonitorRunResultDialog`）、删除确认
- □ 单元格组件 `MonitorPrimaryModelCell` / `MonitorActionsCell`
- □ `RelayPulseMatrix`：渠道 × 时间桶热力矩阵，滚轮横向缩放、悬浮多指标 tooltip、图例、健康状态点
- □ `MonitorSettingsPanel`、`MonitorTrendChart`、`MonitorRankBadge`、`MetricCell`、`FilterMultiSelect`

#### `/admin/ops` — 运维大盘（`OpsDashboard.vue` 865 行 + 17 个卡片组件）
- □ `OpsDashboardHeader`（1649 行）：时间范围、刷新间隔、SLA 指标条
- □ 卡片：`OpsConcurrencyCard`、`OpsAlertRulesCard`（指标阈值规则编辑）、`OpsAlertEventsCard`、`OpsSystemLogTable`、6 张图表卡（延迟 / 吞吐 / 切换率 / 错误趋势 / 错误分布 / OpenAI Token 统计）
- □ 弹窗：`OpsSettingsDialog`（采集间隔、告警收件人、最低级别、报告配置、范围校验）、`OpsErrorDetailModal`、`OpsRequestDetailsModal`、`OpsErrorDetailsModal`
- □ 定时刷新（`useIntervalFn`）
- □ 骨架屏（`OpsDashboardSkeleton`）
- □ `OPS_DISABLED` 错误 → 缓存关闭 + window 事件 + 重定向

#### `/admin/orders`、`/admin/orders/dashboard`、`/admin/orders/plans`
- □ `AdminOrdersView`：`OrderTable`（show-user）、订单详情弹窗、`AdminRefundDialog`（强制确认时显示 warning / require-force）
- □ `AdminPaymentDashboardView`：收入/订单/渠道统计卡 + 图表
- □ `AdminPaymentPlansView`：计划表（id/名称/分组/价格/有效期/在售/排序/操作）+ `PlanEditDialog`（名称、分组带平台色 + 限额预览、描述、价格含 CNY 换算与手续费预览、原价、有效期数值+单位、排序、币种、卖点、在售开关）+ 删除确认

### 10.8 全站组件（随批次迁移）

- □ `AppSidebar` 三套菜单：admin / 「我的账户」/ 普通用户；`expandOnly` 分组；功能开关过滤；简单模式过滤；自定义菜单（`icon_svg`）；引导锚点（`#sidebar-*`、`data-tour`）；主题切换；折叠 72/256；移动端抽屉 + 遮罩
- □ `AppHeader`：标题解析链（自定义页 → `resolveRouteMetaKeys` → meta）、公告铃铛、文档链接、模型广场入口、语言切换、订阅进度、余额胶囊（hover 明细，`sm` 以下入用户菜单）、用户菜单（资料 / 密钥 / GitHub / 客服 / 重播引导 / 退出）
- □ `TablePageLayout` → `PageLayout`：操作 / 筛选 / 表格 / 分页四段，<1024px 恢复文档流
- □ 引导：admin 27 步（25 锚点）/ 用户 6 步（5 锚点）；35 处 `data-tour`；进度键 `onboarding_tour_<userId>_<role>_v4_interactive`
- □ 公告：`AnnouncementBell`（未读红点 ping、列表 + 详情双层弹窗、markdown、全部已读）+ `AnnouncementPopup`（队列串播 300ms 间隔、会话内去重）
- □ 合规：`AdminComplianceDialog`（423 门禁、确认语 zh/en、文档链接）
- □ 版本：`VersionBadge`（当前/最新版本、更新、重启倒计时、回滚版本选择、GitHub/Docker 链接、手动命令复制、非 release 构建提示）
- □ 支付组件：`PaymentMethodSelector`、`PaymentQRDialog`、`SubscriptionPlanCard`、`ProviderCard`、`PaymentProviderList`（拖拽）、`PaymentProviderDialog`
- □ 图表：`TokenUsageTrend`、`ModelDistributionChart`（含排行 + 行内钻取 `UserBreakdownSubTable`）、`EndpointDistributionChart`、`GroupDistributionChart`、`UserDashboardCharts`、`DailyRevenueChart`、`AccountStatsModal`、`MonitorTrendChart`、Ops 六图

---

## 11. 横切专项

### 11.1 认证与 OAuth

- 登录方式：账号密码、邮箱验证码、TOTP 二次验证、Passkey（WebAuthn）、OAuth（GitHub / LinuxDO / 微信 / 钉钉 / OIDC）、MCP OAuth 授权页。
- 回调页：`/auth/callback`（含别名）、`/auth/linuxdo/callback`、`/auth/wechat/callback`、`/auth/wechat/payment/callback`、`/auth/dingtalk/callback`、`/auth/dingtalk/email-completion`、`/auth/oidc/callback`。
- 每个回调页的状态机（code 交换、state 校验、pending 注册、邮箱补全、错误码文案）**逐行保留**，只替换布局与控件。
- `PendingOAuthCreateAccountForm` 被 5 个回调页复用，必须优先迁移。
- 验证码支持三种实现，按公开设置三选一：Cloudflare Turnstile、腾讯天御、阿里云验证码（`CaptchaChallenge.vue` 分派）。三种 SDK 的加载、token 回传、过期/错误事件原样保留，只重写外层样式与布局。

### 11.2 支付与订阅

- 渠道：EasyPay（支付宝/微信）、支付宝直连、微信直连、Stripe、Airwallex。
- 模式：二维码、弹窗、跳转；Stripe 还有独立 `stripe-popup` 页。
- 支付流程：创建订单 → 拉起 SDK → 轮询结果 → 结果页；`paymentFlow.ts` 的 `decidePaymentLaunch` 分支、`payment.recovery.current` 恢复快照原样保留。
- 微信支付授权回跳后的恢复逻辑（`paymentWechatResume.ts`）必须保留。
- 支付相关页面 `requiresPayment` 开关、管理端支付配置（`PaymentProviderList` 拖拽排序 + 凭据表单）一并迁移。
- CSP 已放行 Stripe/Airwallex 域名；新前端不得引入额外脚本域名。

### 11.3 实时能力

| 场景 | 机制 | 迁移要求 |
|---|---|---|
| 管理端 Ops | WebSocket `/api/v1/admin/.../ws` | 连接生命周期/重连/降级保持 |
| 渠道监控 | 轮询 + 自动刷新 | `useAutoRefresh` 原样复用 |
| 账户测试 | 轮询/流式结果 | 逐条回填与取消逻辑保持 |
| 订阅进度 | 轮询 | `subscriptionStore.startPolling` 保持 |
| 公告 | 路由切换 + 可见性触发 | 节流策略保持 |
| 支付结果 | 轮询 + 恢复快照 | 停止条件保持 |

### 11.4 导入 / 导出

- 账户批量导入、代理导入、数据导入弹窗；导出 CSV/JSON/Excel（按页面实际实现）。
- 导出进度使用 `a-modal` + `a-progress`；取消语义保留。
- 文件下载使用 `file-saver`，文件名与内容格式不变。

### 11.5 安全相关交互

- WebAuthn Passkey：注册/登录/删除；依赖 origin，同源部署不变。
- TOTP：绑定（二维码 + 密钥）、登录二次验证、敏感操作 step-up。
- `useStepUp` 的 `STEP_UP_REQUIRED / STEP_UP_TOTP_NOT_ENABLED / STEP_UP_ADMIN_API_KEY_FORBIDDEN` 错误处理与「静默取消」语义。
- 管理端合规弹窗（423 `ADMIN_COMPLIANCE_ACK_REQUIRED`）与全局事件 `admin-compliance-required`。
- 危险操作二次确认（删除、回滚、重置密钥等）文案与门槛不降低。

### 11.6 内容渲染

- 公告 Markdown：marked + DOMPurify；沿用 `announcement-markdown.css` 排版。
- 自定义首页：`home_content` 支持 HTML / iframe / 紧凑模式；`compact_home_enabled`。
- 自定义页面 `/custom/:id` 与法律文档 `/legal/:documentId`：内容渲染与 404 行为保持。
- 模型广场 `/model-plaza`：开关 + 强制登录 + 嵌入式参数。

### 11.7 国际化与时区

- `vue-i18n` 保留；新增 antd locale 映射：`zh → zhCN`、`en → enUS`，同时 `dayjs.locale()`。
- 请求头 `Accept-Language` 与 GET `timezone` 参数由 `api/client.ts` 注入，原样保留。
- 日期范围选择、表格时间列、图表时间轴的显示时区必须一致（`server_utc_offset`）。
- 语言切换后整页 reload（后端按语言返回数据）。

### 11.8 CSP 与配置注入

- 新前端 HTML 仍由 Go 提供（同源嵌入），CSP 不变；antd CSS-in-JS 依赖 `style-src 'unsafe-inline'`，现状已允许。
- `window.__APP_CONFIG__` 注入可选；缺失时 `appStore.fetchPublicSettings()` 回退。
- 不引入 Google Fonts、外部图标字体或 CDN 资源。

### 11.9 错误处理

- 统一错误 envelope 解析（`utils/apiError.ts`）。
- 业务错误码文案（`errorCategory`、`errorBadges`）保留。
- 路由 chunk 加载失败自动重载（10 秒防抖）。
- 全局未捕获错误提示与降级页保持不变。

---

## 12. 测试与验收

### 12.1 单元测试（按 384 个现有 spec 分类处置）

| 类别 | 数量 | 处置 |
|---|---:|---|
| 纯逻辑可平移（`api/` 29、`utils/` 36、`stores/` 9、`composables/` 16、`constants/` 1、`router/` 5、`i18n/` 9、integration 4） | ~109 | 复制后改 import 路径；`client.spec`、`tokenRefresh.spec` 是最高价值资产，必须保留 |
| 依赖 store/API 形状、少量 DOM（可改造） | ~130 | 保留主体断言，替换选择器与 mock 形状 |
| 依赖自研组件 DOM / Tailwind 类名 | ~85 | 重写；优先覆盖 DataTable、Select、BaseDialog、DateRangePicker、Pagination |
| 读源码文本做契约断言（`readFileSync` 读 `.vue`/`style.css`） | 17 | 全部重写为新目录的契约测试（或改为行为测试） |

具体重灾区：`components/common/__tests__`（29）、`components/account/__tests__`（33）、`components/payment/__tests__`（12）、`views/admin/__tests__`（44）、`views/user/__tests__`（20）、`views/auth/__tests__`（12）、`views/admin/ops/components/__tests__`（12）。

**CI 硬门槛**：`Makefile` 的 `test-frontend-critical` 硬编码 30 个 spec（i18n、client、tokenRefresh、keys.bulkUpdate、channelMonitorV2、useModelWhitelist、platforms、账号凭据构建、KeysView、PaymentView、SettingsView、channel-monitor-v2 等）。新目录必须逐一对齐这 30 个文件，否则 CI 直接红。

**新增测试**：`AppDataTable`、`AppDateRangePicker`、`useConfirm`、toast 适配器、i18n key 完整性、功能开关语义、路由守卫矩阵。

### 12.2 E2E 冒烟（新增 Playwright）

必须覆盖的 12 条主链路：

1. 初始化向导 → 创建管理员 → 登录。
2. 账号密码登录 / TOTP / Passkey。
3. OAuth 回调（至少 GitHub 与 LinuxDO 的 mock）。
4. 用户创建 Key → 复制 → 查看用量。
5. 购买订阅 → 二维码支付 → 结果页（mock 支付回调）。
6. Stripe 支付弹窗打开与关闭。
7. 管理端用户 CRUD + 余额调整。
8. 管理端分组创建/编辑（含订阅与倍率）。
9. 管理端账号创建/测试/批量操作。
10. 管理端设置保存与脏检查。
11. 渠道监控与 Ops 页面加载、WebSocket 连接。
12. 语言切换、主题切换、移动端导航。

### 12.3 功能核对机制

- 每页迁移完成时，在本文件 §10 对应行勾选，并附「截图 + 关键交互录屏」。
- i18n key 校验：`check:i18n` 保留；新增「每个 key 至少被引用一次」的静态检查（允许白名单）。
- 组件 API 对照：附录 A 逐项确认 props/emits/slots 未丢失。

### 12.4 视觉与可用性走查

- 亮/暗两套主题逐页截图对比。
- 1280 / 1024 / 768 / 375 四档宽度。
- 键盘导航与焦点顺序；弹窗 ESC 层级；表单错误提示可读性。

### 12.5 性能预算

- 首屏 JS gzip 不超过旧版 +15%。
- 表格 100 行渲染 < 300ms；页面切换 < 500ms。
- 无 Tailwind 后 CSS 体积应显著下降（antd CSS-in-JS 按需注入）。

---

## 13. 构建、部署、切换、回滚

### 13.1 并行期

- `frontend-antd/vite.config.ts` 输出 `../backend/internal/web/dist-antd`，不覆盖现有 `dist`。
- 本地联调：`pnpm dev` 代理 `/api`、`/v1`、`/setup` 到 `http://localhost:8080`，与旧前端一致。
- 开发期注入公开设置：沿用 `injectPublicSettings` 插件（2s 超时，失败回退 API）。
- 合法文档：`LegalDocumentView` 通过 `?raw` 构建期导入 `docs/legal/*.md`，新目录需保持相同相对路径与 Docker 拷贝。
- CI 新增 `frontend-antd` job（lint + typecheck + test + build），旧 job 保留。

### 13.2 切换（一次性，可逆）

1. 把 `outDir` 改为 `../backend/internal/web/dist`。
2. `Dockerfile` 的前端构建阶段 `COPY frontend-antd/`（建议 `ARG FRONTEND_DIR=frontend` 便于切换），并保留 `COPY docs/legal/ /app/docs/legal/`。
3. `.github/workflows/backend-ci.yml`、`release.yml`、`security-scan.yml` 的 `working-directory` 与 `cache-dependency-path` 改为新目录；`release.yml` 的 `frontend-dist` artifact 路径不变。
4. `Makefile` 的 `build-frontend` / `test-frontend` 指向新目录；`test-frontend-critical` 的 30 个 spec 路径同步。
5. 删除旧 `frontend/`（切换后至少保留一个 release 周期）。

### 13.3 回滚

- **代码级**：保留旧 `frontend/` 与切换前 commit；回滚即还原构建目录。
- **产物级**：切换前的 release 二进制已内嵌旧前端，可直接回退版本。
- **灰度建议**：先在测试环境用新前端跑完整 E2E，再发版；不做同域双前端分流（会引入路由与静态资源冲突）。

---

## 14. 风险与对策

| # | 风险 | 影响 | 对策 |
|---|---|---|---|
| 1 | 功能遗漏 | 高 | §10 逐页核对表 + 66 路由清单 + i18n key 校验 + E2E |
| 2 | `DataTable` 行为差异（虚拟滚动/移动卡片/粘性列/操作列折叠） | 高 | 先做 `AppDataTable` 并在 3 个代表页（Users/Accounts/Usage）验证，再批量推广 |
| 3 | 大表单拆分引入回归（Settings 13k 行、Groups 7k 行） | 高 | 先产出字段清单与保存作用域，再拆组件；逐字段对照 |
| 4 | 支付 SDK / CSP / 回调 | 高 | 保留原加载与轮询逻辑；沙箱环境回归；不改 CSP |
| 5 | WebAuthn / OAuth origin | 中 | 同源部署；不换域名与端口 |
| 6 | 移动端适配退化 | 中 | 四档宽度走查；表格卡片模式优先实现 |
| 7 | 暗色主题不一致 | 中 | 统一 `useTheme`；禁止组件内直接读 `document.classList` |
| 8 | 包体增大 | 中 | 按需引入 + 手动分包 + 预算门禁 |
| 9 | 测试大量失效 | 中 | 纯逻辑测试复制；DOM 测试按页重写；E2E 补位 |
| 10 | 双目录漂移（API 变更） | 中 | P0 建立 `api/` diff 守卫；后端变更需同步两处或只改新目录并回灌 |
| 11 | 引导锚点失效 | 低 | 统一补 `data-tour`；逐步骤验收 |
| 12 | 图表暗色联动 | 低 | 图表配置改为响应式变量 |

---

## 15. 排期与里程碑

> 估算前提：1 名熟悉该项目的前端；AI 辅助；不含后端配合等待。人日按「页面 + 组件 + 自测 + 走查」计，与 §9 批次一一对应。

| 批次 | 内容 | 人日 | 里程碑 |
|---|---|---:|---|
| B0 | 基建：主题、布局、`AppDataTable`、`AppSelect`、`AppDateRangePicker`、`AppModal`、toast、confirm、图标映射、HTTP/store/router/i18n 复制 | 12 | 登录后空壳可跑通 |
| B1 | 认证 / 公开页 / Setup | 10 | 全新实例可初始化并登录 |
| B2 | 首页 / Key 用量 / 模型广场 / 法律文档 | 4 | 公开站可用 |
| B3 | 用户核心：仪表盘 / Key / 用量 / 兑换 / 推广 / 可用渠道 / MCP / 资料 | 10 | 用户主链路可用 |
| B4 | 支付 / 订阅 / 订单 / 结果页 / Stripe / Airwallex | 10 | 支付全链路可用 |
| B5 | 渠道状态 V1/V2 / 批量绘图 | 6 | 用户端全量 |
| B6 | 管理端：用户 / 分组 / 账号 / 概览 | 20 | 账号池可运营 |
| B7 | 管理端：渠道 / 代理 / 订阅 / 兑换 / 优惠 / 公告 / 审计 / 备份 / 插件 / 用量 / 推广 | 18 | 常规运营闭环 |
| B8 | 管理端：系统设置（9 个页签） | 12 | 设置可保存 |
| B9 | 管理端：风控 / 提示词审计 / 渠道监控 / Ops / 订单支付 | 18 | 管理端全量 |
| B10 | 收尾：去 Tailwind、测试补齐、性能、切换演练 | 10 | 可发布 |
| **合计** | | **130 人日** | 单人约 26 周（约 6 个月）；双人并行约 13–15 周 |

> 口径说明：130 人日已按「AI 辅助 + 复用非 UI 层」压缩，折算约 1,150 行 UI 代码/人日。若要求逐页像素级走查与录屏验收，实际会落在 150–170 人日。不建议把工期压到 3 个月以内——支付、OAuth、大表单三块的返工成本远高于赶工收益。

**并行建议**：B6 与 B7 可由两人并行（文件不重叠）；B8 与 B9 同理。B0–B5 建议单人串行，保证主链路一致性与设计系统收敛。

**关键路径**：B0 的 `AppDataTable` 是所有表格页的前置，必须最先稳定；B8（SettingsView 13k 行）与 B6 的账号表单（`CreateAccountModal` 7.8k 行 + `EditAccountModal` 6.4k 行）是单点最大风险，建议预留缓冲。

---

## 16. 立即执行的前置任务（本周）

1. 建立 `frontend-antd/` 脚手架与 CI job，产出空壳登录页。
2. 复制 `api/`、`types/`、`utils/`、`constants/`、`i18n/`、`stores/`、`router/`，建立 `api` diff 守卫。
3. 完成主题 token、`ConfigProvider`、暗色切换、`AppToast`、`AppModal`、`AppDataTable` 原型。
4. 迁移 3 个代表页作为样板：`LoginView`、`UsersView`（表格）、`SettingsView` 的一个页签（大表单）。
5. 产出 `docs/FRONTEND_ANTD_PARITY_CHECKLIST.md`（从本文 §10 拆出可勾选版本）。

---

## 附录 A：路由对照表（66 条）

| 路径 | name | 组件 | 需登录 | 需管理员 |
|---|---|---|---|---|
| `/setup` | Setup | setup/SetupWizardView | 否 | — |
| `/home` | Home | HomeView | 否 | — |
| `/login` | Login | auth/LoginView | 否 | — |
| `/register` | Register | auth/RegisterView | 否 | — |
| `/email-verify` | EmailVerify | auth/EmailVerifyView | 否 | — |
| `/auth/callback`（别名 `/auth/oauth/callback`） | OAuthCallback | auth/OAuthCallbackView | 否 | — |
| `/auth/linuxdo/callback` | LinuxDoOAuthCallback | auth/LinuxDoCallbackView | 否 | — |
| `/auth/wechat/callback` | WeChatOAuthCallback | auth/WechatCallbackView | 否 | — |
| `/auth/wechat/payment/callback` | WeChatPaymentOAuthCallback | auth/WechatPaymentCallbackView | 否 | — |
| `/auth/dingtalk/callback` | DingTalkOAuthCallback | auth/DingTalkCallbackView | 否 | — |
| `/auth/dingtalk/email-completion` | dingtalk-email-completion | auth/DingTalkEmailCompletionView | 否 | — |
| `/auth/oidc/callback` | OIDCOAuthCallback | auth/OidcCallbackView | 否 | — |
| `/forgot-password` | ForgotPassword | auth/ForgotPasswordView | 否 | — |
| `/reset-password` | ResetPassword | auth/ResetPasswordView | 否 | — |
| `/key-usage` | KeyUsage | KeyUsageView | 否 | — |
| `/legal/:documentId` | LegalDocument | public/LegalDocumentView | 否 | — |
| `/model-plaza` | ModelPlaza | ModelPlazaView | 否（受开关） | — |
| `/` | — | 重定向 `/home` | — | — |
| `/dashboard` | Dashboard | user/DashboardView | 是 | 否 |
| `/keys` | Keys | user/KeysView | 是 | 否 |
| `/batch-image`（别名 `/docs/batch-image`） | BatchImageGuide | user/BatchImageGuideView | 是 | 否 |
| `/usage` | Usage | user/UsageView | 是 | 否 |
| `/redeem` | Redeem | user/RedeemView | 是 | 否 |
| `/affiliate` | Affiliate | user/AffiliateView | 是 | 否 |
| `/available-channels` | UserAvailableChannels | user/AvailableChannelsView | 是 | 否 |
| `/settings/mcp` | MCP | user/MCPView | 是 | 否 |
| `/oauth/mcp/authorize` | MCPOAuthAuthorize | auth/MCPOAuthAuthorizeView | 是 | 否 |
| `/profile` | Profile | user/ProfileView | 是 | 否 |
| `/subscriptions` | Subscriptions | user/SubscriptionsView | 是（+订阅开关） | 否 |
| `/purchase` | PurchaseSubscription | user/PaymentView | 是（+支付开关） | 否 |
| `/orders` | OrderList | user/UserOrdersView | 是（+支付开关） | 否 |
| `/payment/qrcode` | PaymentQRCode | user/PaymentQRCodeView | 是 | 否 |
| `/payment/result` | PaymentResult | user/PaymentResultView | 否 | 否 |
| `/payment/stripe` | StripePayment | user/StripePaymentView | 否 | 否 |
| `/payment/airwallex` | AirwallexPayment | user/AirwallexPaymentView | 否 | 否 |
| `/payment/stripe-popup` | StripePopup | user/StripePopupView | 否 | 否 |
| `/custom/:id` | CustomPage | user/CustomPageView | 是 | 否 |
| `/admin` | — | 重定向 `/admin/dashboard` | — | — |
| `/admin/dashboard` | AdminDashboard | admin/DashboardView | 是 | 是 |
| `/admin/ops` | AdminOps | admin/ops/OpsDashboard | 是 | 是 |
| `/admin/audit-logs` | AdminAuditLogs | admin/AuditLogView | 是 | 是 |
| `/admin/users` | AdminUsers | admin/UsersView | 是 | 是 |
| `/admin/groups` | AdminGroups | admin/GroupsView | 是 | 是 |
| `/admin/channels` | — | 重定向 `/admin/channels/pricing` | — | — |
| `/admin/channels/pricing` | AdminChannels | admin/ChannelsView | 是 | 是 |
| `/admin/channels/monitor` | AdminChannelMonitor | admin/ChannelMonitorView | 是 | 是 |
| `/monitor` | ChannelStatus | user/ChannelStatusView | 是 | 否 |
| `/admin/subscriptions` | AdminSubscriptions | admin/SubscriptionsView | 是 | 是 |
| `/admin/accounts` | AdminAccounts | admin/AccountsView | 是 | 是 |
| `/admin/plugins` | AdminPlugins | admin/PluginsView | 是 | 是 |
| `/admin/announcements` | AdminAnnouncements | admin/AnnouncementsView | 是 | 是 |
| `/admin/proxies` | AdminProxies | admin/ProxiesView | 是 | 是 |
| `/admin/redeem` | AdminRedeem | admin/RedeemView | 是 | 是 |
| `/admin/promo-codes` | AdminPromoCodes | admin/PromoCodesView | 是 | 是 |
| `/admin/settings` | AdminSettings | admin/SettingsView | 是 | 是 |
| `/admin/risk-control` | AdminRiskControl | admin/RiskControlView | 是 | 是（+风控开关） |
| `/admin/prompt-audit` | AdminPromptAudit | features/prompt-audit/PromptAuditView | 是 | 是（+风控开关） |
| `/admin/usage` | AdminUsage | admin/UsageView | 是 | 是 |
| `/admin/affiliates` | — | 重定向 `/admin/affiliates/invites` | — | — |
| `/admin/affiliates/invites` | AdminAffiliateInvites | admin/affiliates/AdminAffiliateInvitesView | 是 | 是 |
| `/admin/affiliates/rebates` | AdminAffiliateRebates | admin/affiliates/AdminAffiliateRebatesView | 是 | 是 |
| `/admin/affiliates/transfers` | AdminAffiliateTransfers | admin/affiliates/AdminAffiliateTransfersView | 是 | 是 |
| `/admin/orders/dashboard` | AdminPaymentDashboard | admin/orders/AdminPaymentDashboardView | 是 | 是（+支付开关） |
| `/admin/orders` | AdminOrders | admin/orders/AdminOrdersView | 是 | 是（+支付开关） |
| `/admin/orders/plans` | AdminPaymentPlans | admin/orders/AdminPaymentPlansView | 是 | 是（+支付开关） |
| `/:pathMatch(.*)*` | NotFound | NotFoundView | — | — |

---

## 附录 B：本地存储键清单（键名与语义不得变更）

| 键 | 存储 | 用途 |
|---|---|---|
| `auth_token` | localStorage | 访问令牌 |
| `refresh_token` | localStorage | 刷新令牌 |
| `auth_user` | localStorage | 用户快照（防串号） |
| `token_expires_at` | localStorage | 令牌过期时间 |
| `pending_auth_session` | localStorage | OAuth 待认领会话摘要 |
| `auth_expired` | sessionStorage | 过期提示（一次性） |
| `theme` | localStorage | `light` / `dark` |
| i18n 语言键 | localStorage | `en` / `zh` |
| `table-page-size` | localStorage | 表格页大小 |
| `*-hidden-columns` | localStorage | 各页列显示偏好（版本化） |
| `sort-storage-key` 系列 | localStorage | 表格排序偏好 |
| `onboarding_tour_<userId>_<role>_v4_interactive` | localStorage | 引导进度 |
| `channel-status-auto-refresh` | localStorage | 渠道状态自动刷新 |
| `payment.recovery.current` | localStorage | 支付恢复快照 |
| `ops_monitoring_enabled_cached` | localStorage | Ops 开关镜像 |
| `ops_realtime_monitoring_enabled_cached` | localStorage | Ops 实时开关镜像 |
| `payment_enabled_cached` | localStorage | 支付开关镜像 |
| `ops_query_mode_default_cached` | localStorage | Ops 查询模式镜像 |
| 协议 revision 键 | localStorage | 登录协议版本 |
| `register_data` | sessionStorage | 注册待验证数据 |
| `chunk_reload_attempted` | sessionStorage | chunk 重载防抖 |
| `sub2api-batch-image-preview-cache` | IndexedDB | 批量绘图预览缓存 |

---

## 附录 C：i18n 命名空间

| 命名空间 | 约 key 数 | 覆盖 |
|---|---:|---|
| `admin.accounts` | 1639 | 账号管理 |
| `admin.settings` | 1361 | 系统设置 |
| `admin.overview` | 1225 | 管理概览 |
| `dashboard` | 943 | 用户仪表盘、分组、密钥、用量、监控、资料、空态、分页、错误、日期 |
| `admin.channels` | 790 | 渠道与定价 |
| `admin.ops` | 778 | 运维大盘 |
| `misc` | 561 | 购买、自定义页、公告、我的订阅、引导、支付、版本、订阅进度 |
| `admin.resources` | 557 | 资源类设置 |
| `common` | 425 | 导航、认证、法律、step-up、合规 |
| `adminAccessKeys` | 260 | 管理端访问密钥 |
| `landing` | 210 | 首页、Key 用量、setup、批量绘图指南 |
| `batchImage` | 195 | 批量绘图 |
| `channelMonitorV2` | 124 | 渠道监控 v2 |
| `admin.promptAudit` | 88 | 提示词审计 |
| `mcp` | 59 | MCP |
| `admin.plugins` | 47 | 插件 |
| `admin.audit` | 45 | 审计 |

**质量闸门（4 类测试必须重建）**：
1. en / zh 叶子键集合完全一致。
2. 叶子文案非空。
3. 静态引用键全覆盖（扫全仓 `t()` / `$t()` / `keypath` / `titleKey` / `descriptionKey` / `<i18n-t>`）。
4. 全量消息可编译（捕获未转义花括号）+ 顶层键冲突检测。

---

## 附录 D：迁移完成定义（DoD）

单个页面「完成」需同时满足：

1. 本文件 §10 对应条目全部勾选。
2. 亮/暗主题、四档宽度截图存档。
3. 该页的纯逻辑测试已迁移；DOM 测试已重写或由 E2E 覆盖。
4. 该页用到的 i18n key 在 en / zh 均存在且非空。
5. 该页用到的 `localStorage` 键行为与旧版一致（手工验证或测试）。
6. 该页涉及的接口请求（含查询参数、请求头）与旧版逐字段一致（DevTools 抓包比对）。
7. 该页的危险操作、二次确认、TOTP step-up 行为一致。
8. PR 描述中附「功能核对表」勾选结果与截图链接。