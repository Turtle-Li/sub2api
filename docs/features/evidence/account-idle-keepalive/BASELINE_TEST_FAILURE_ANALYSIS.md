# 五条基线测试失败的原因分析

状态：IMPLEMENTED_SELF_CHECKED（2026-11-09）。已修复三个测试文件，原有五条失败
全部消除；前端全量 4037/4037 通过。此次修复没有修改业务代码，未进行独立 QA
或生产验收。

结论：已处理，修复范围是三个测试文件。这五条失败由两次已有界面改动后
测试未同步造成：三条首页测试选错链接，另两条仍采用统一商品管理改版前的
路径或支付开关预期。修正后专项、全量回归、ESLint、类型检查和构建均通过。

## Bug Analysis

Bug: BUG-20261109-baseline-navigation-assertions

现象:

修复前完整前端回归为 4023 passed / 5 failed。五条失败分别位于
`HomeView.compact.spec.ts`（3 条）、`feature-access.spec.ts`（1 条）、
`AppSidebar.spec.ts`（1 条）。这是自动化断言失败的计数。

复现条件:

- 源码基线为 `f03597410f48c44da86352e80e52d8c8ae2b7933`。
- 已在从该提交导出的干净源码中运行上述三个文件，结果为 35 passed / 5 failed；
  与当前工作树逐条对比，失败的五个测试名称完全一致。
- 修复前确认三个业务文件及三个测试文件的内容均与基线相同。
- 环境沿用项目安装的 Vue、Vue Router 和 Vitest。无需后端、账号凭据或真实付款。

调用链:

| 断言 | 实际检查过程 | 首次偏离预期的位置 |
| --- | --- | --- |
| 首页：未登录、普通用户、管理员各一条 | `compactDestination()` → compact-home 下第一个 `RouterLinkStub` → 取 `to` | helper 取到新增的状态入口 `/status`，没有定位登录/进入控制台按钮 |
| 路由：关闭公开购买后管理员仍可管理订单 | 模拟 `createRouter()` → 原始 routes 数组 → `routeFor()` 只比较 `candidate.path` | 新的规范路径为 `/admin/products`，旧 `/admin/orders/plans` 位于 `alias`；原始数组查找不会展开别名 |
| 侧栏：支付开关控制商品入口 | 读取组件源码 → 从 `paymentEnabled` 文本开始截取 → 匹配旧路径字符串 | 新商品入口为 `/admin/products`，并按新契约独立于充值支付开关 |

影响范围:

直接影响前端回归可信度，以及这三处功能的有效测试覆盖。当前证据未显示
这五条失败对应五个用户可见故障。分析没有访问生产环境，也没有作生产端到端
可用性的结论。账号每日次数限额和自动保号的业务文件不在上述调用链中。

疑似根因:

**首页三条：直接证据，置信度高。**

- `frontend/src/views/__tests__/HomeView.compact.spec.ts:58` 的
  `compactDestination()` 对整个 compact-home 使用 `findComponent`，返回第一个链接。
- `frontend/src/views/HomeView.vue:52` 的状态入口排在登录按钮之前。
- 同文件的导航登录按钮（62 行）及主内容按钮（81 行）仍按登录状态选择
  `/login` 或 `dashboardPath`；542 行仍区分普通用户 `/dashboard` 与管理员
  `/admin/dashboard`。
- 提交 `0c699b5dd` 加入状态入口后改变了链接顺序。三个失败共享同一个 helper，
  均实际比较了 `/status`，无需假设三个登录分支同时出错。

**路由一条：直接证据及实际解析验证，置信度高。**

- `frontend/src/router/index.ts:827` 定义 `/admin/products`，下一行保留
  `/admin/orders/plans` 别名，元信息为需要登录、需要管理员、`requiresPayment: false`。
- 旧测试在 `feature-access.spec.ts:116` 只查原始 `path`，因此先抛出
  `route not found`。随后还保留了该商品页 `requiresPayment: true` 的旧断言；
  只让 helper 识别别名仍不足以完成修复。
- 本次从当前路由源码用 TypeScript AST 提取真实配置，交给实际安装的 Vue Router
  创建内存路由解析。新旧地址都能匹配，旧地址的 `aliasOf` 为 `/admin/products`；
  两者保留相同登录与管理员限制，支付开关均为 false。
- 因而该报错的具体含义是测试的原始配置查找漏掉别名，并不证明旧链接失效。

**侧栏一条：直接证据及产品契约，置信度高。**

- `frontend/src/components/layout/__tests__/AppSidebar.spec.ts:65` 仍匹配旧的
  `/admin/orders/plans`，测试名称仍要求商品管理受支付开关控制。
- 当前组件 871 行只按支付开关控制支付统计入口；875 行使用独立的
  `/admin/products` 商品入口。
- 商品统一提交 `f6c60f671f0937fade3843a4ae18a76a535e2c6d` 同时改了路由、
  别名、侧栏地址和支付开关要求，没有同步这些旧断言。
- 当前权威契约 `docs/features/SHOP_UNIFIED_SKU_20261108.md:50` 明确要求三个商品
  Tab 共用新入口、旧地址保留别名，并使商城管理独立于充值功能开关。
- 现有“截取一段源码再匹配字符串”的测试也无法可靠证明入口位于某个条件分支内。

推荐修改方案:

1. 首页测试明确定位主内容中的登录/进入控制台按钮，再分别校验三种身份的目标。
   单独覆盖状态页入口，避免依赖整页链接顺序。
2. 路由测试覆盖规范路径与旧地址别名，优先使用真实路由解析验证；更新商品管理
   的支付开关预期，同时保留登录/管理员限制及客户购买页面的支付门禁检查。
3. 侧栏测试验证支付开关关闭时商品管理仍可见、支付统计隐藏；开启时两者可见。
   将断言与当前商品管理契约对齐，减少对源码字符串和位置的依赖。
4. 先回放这三个文件，再执行全量前端回归、类型检查及构建。

风险:

直接删除失败用例，或者把它们一律改为断言 `/status`，会损失原本需要验证的
身份跳转和权限边界。修复应保留这些业务意图，避免把“管理员能配置商品”误改成
“客户能在支付关闭时购买”。无需更改生产路由或页面来迎合旧断言。

## 证据与交接

- 原始记录：`/tmp/sub2api-keepalive-frontend-full.log`。
- 干净基线记录：`/tmp/sub2api-keepalive-frontend-baseline.log`。
- 上述日志中的五个失败名称已经逐条比对一致；临时基线源码副本已清理。
- 相关既有 Bug 检索：前端目录的支付方式可用性、Studio 加载/价格案例均与本次
  导航断言机制不同，未复用其修复方案；项目内未发现匹配模板或相同根因记录。
- 收到所有者修复指令后，Developer 按上述范围完成修改并回放验证，结果见下。
- `knowledge_candidate: false`：当前证据是执行者自检，尚未经过独立 QA，不作独立
  验证或发布就绪结论。

## 修复与验证

修复仅涉及以下测试文件；原有首页、路由和侧栏业务文件仍与基线相同。

| 文件 | 修改及覆盖 |
| --- | --- |
| `frontend/src/views/__tests__/HomeView.compact.spec.ts` | 将登录/控制台目标定位限定到主内容按钮；保留未登录、普通用户和管理员三个分支；单独检查页头状态入口，并确认模型广场入口可见时主按钮仍指向登录 |
| `frontend/src/router/__tests__/feature-access.spec.ts` | 用实际 Vue Router 的内存路由解析配置；验证 `/admin/products` 与旧地址的真实别名关系；两种地址均验证管理员在支付关闭时可访问、匿名需登录、普通用户被拦截；购买入口继续读取真实元信息验证支付门禁 |
| `frontend/src/components/layout/__tests__/AppSidebar.spec.ts` | 挂载实际组件，分别设置支付开启/关闭；两种情况下均显示商品、订单、优惠券及发票管理入口，支付统计仅在支付开启时显示；导航采用新商品路径 |

新增 9 项用例，专项由原来 40 项增至 49 项，没有跳过失败用例。

| 检查 | 结果 | 原始日志 |
| --- | --- | --- |
| 修改前回放三个文件 | 35 passed / 5 failed，1.02 s | `/tmp/sub2api-navigation-tests-red.log` |
| 修改后回放三个文件 | 49/49 passed，903 ms | `/tmp/sub2api-navigation-tests-green.log` |
| `pnpm exec vitest run` | 465 个文件、4037 项测试全部通过，69.57 s | `/tmp/sub2api-navigation-frontend-full.log` |
| 三个修改文件的 `pnpm exec eslint` | 通过，退出码 0 | 命令直接执行，无诊断输出 |
| `pnpm build` | 通过：3 项语言键检查、`vue-tsc -b`、Vite；Vite 用时 22.48 s | `/tmp/sub2api-navigation-frontend-build.log` |
| `git diff --check` | 通过 | 命令直接执行，无诊断输出 |

构建仍有既有的 Browserslist/分块体积提示，测试存在既有的本地存储或翻译告警；
没有测试失败或类型错误。此次为测试修复，因此没有重复执行未改动的后端测试，
也没有访问真实上游、生产环境或执行提交、推送、部署。

沿用当前相关功能的隔离工作树。分支预检报告 `CANONICAL_NOT_READY`，原因是
缺少可解析的 `.agent-worktree.toml` trunk；未据此重建工作树或改动已有功能。
