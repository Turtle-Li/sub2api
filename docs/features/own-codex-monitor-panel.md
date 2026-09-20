# 自有 Sub2API 续期面板与 IP 池管理

> 当前状态：2026-09-21 续期提前量、成功耗时和静默刷新已上线，源码 `de57ca2da7c19f6d4d5a6f90049cb1d047b616ac`。当前活动池为10静态＋1动态。

## 范围与参照

自有主线基线 `d9e6f1e21ba8f9dc6d15d894ed59430de7594604`。复用现有探针、账号覆盖层、模型写入和统计，不将第三方 host 适配器或共享静态池调度套入自有项目。

管理面板参照 `https://github.com/Turtle-Li/sub2api-zvonimirsun` revision `8a007412733ed39938a7244b8a79035168c2dd96`（本地 `sub2api-cts-isolated-test-worktree`）：
- `frontend/src/views/admin/CodexTurnStateView.vue`、`frontend/src/assets/codex-turn-state-panel.html`、对应 bridge tests：复用管理员认证、opaque iframe、nonce、主题同步、账号选择和手动刷新。
- `backend/internal/handler/admin/codex_turn_state_panel_handler.go` 与 tests：复用限定路径代理，不新建数据库服务。
- `tools/codex-turn-state-manager/integrated.py` 与 tests：采用受保护代理源覆盖层、地址校验、提取 API SSRF 防护；有意保留自有 per-account/model static-first 调度、optional-device 与 active-container discovery。
- 两个固定版本根目录 LICENSE 均为 GNU LGPL v3；保留许可证与来源记录，分发修改版本时遵循对应源码与许可义务。

## 诊断：动态来源零命中（2026-09-20）

Bug: CTS-SOURCE-OBSERVABILITY-20260920
现象: 用户看到命中来自10个静态代理，动态为零。
复现条件: 对现有在线服务进行只读 `/api/stats`、`/api/state` 与日志聚合，不触发额外探测。
调用链: `_refresh_proxies` → `_harvest_rotating` → `probe_turn_state` → `_stats_attempt` → SQLite 聚合 → 面板。
影响范围: 运维判断来源是否生效、连接是否正常、状态是否达标。
疑似根因: 已确认动态被调用且上游返回不达标长度；供应商出口/账号/上游判定的具体因果仍未确认。静态优先的非随机选择会影响比较。
推荐修改方案: 展示完整来源清单及加载状态；区分未尝试、HTTP错误与HTTP200未命中；保留正常调度，不以零命中推断未调用。
风险: 网关地址不是实际出口；292长度不是模型质量证明。不能从日志推导实际出口数量或供应商因果优劣。
状态: ANALYSIS_READY（可观测性改进）；上游零命中的因果仍未闭环。

取样时累计（UTC日期范围2026-09-17至2026-09-20）：

|来源|尝试|HTTP200|目标命中|验证写入|
|---|---:|---:|---:|---:|
|static|1041|1033|238|238|
|rainproxy|464|451|0|0|
|proxora|462|445|0|0|
|webshare_resi|32|32|0|0|

当天日志中动态的928次HTTP200均为312字节；未观察到292。线上状态为10个静态入口、3个动态网关；两个已配置账号共6个模型状态有效。数字是取样时快照，后续探测会变化。没有读取或导出凭据/状态原文，没有修改用户新增账号和IP。

## 任务与验收

|任务|Owner/文件范围|依赖|验收|
|---|---|---|---|
|T1 只读诊断|主代理、线上聚合|无|可区分未调用与实际未命中|
|T2 Python来源管理|独立worker，tools/codex-turn-state-manager|T1|基线来源可见、凭据不回显、覆盖层启停、调度保留|
|T3 Go认证代理|独立worker，backend|参照契约|路径限定、原admin权限、无任意URL或重定向|
|T4 前端适配|主代理，frontend|T1与T2/T3契约|管理清单、统计解释、只读与异常状态、无定时重绘|
|T5 集成验证|主代理及独立QA|T2/T3/T4|Python/Go/前端测试、build、桌面/移动视觉验证|

当前工作区沿用原任务已创建的分支，无新建重复任务分支。preflight 因仓库未配置 `.agent-worktree.toml` trunk 返回 CANONICAL_NOT_READY；以已核对的 fork/main 和干净 HEAD 为基线，不修改仓库工作流配置。

## 上线边界

当前线上仍是独立 systemd 只读 Tailnet 面板。自有管理后台新增路由需要主应用按 `deploy/README.md` 的构建和蓝绿流程发布；不能仅复制前端文件称已上线。

探针代码需要整体安装管理适配层后运行 `integrated.py`，保持现有 protected config/state_dir/account overlay/statistics/pins。只读 Tailnet 绑定不得直接切换为无认证管理模式。容器访问方式、私有管理绑定及维护锁应在发布前验证；不得重写 Caddy 或覆盖用户账号/IP配置。

回滚只恢复本次代码和入口设置；保留账号覆盖层、代理覆盖层、统计库与已写入状态，主应用按现有兼容回滚流程处理，不做数据回滚。

## 验证证据

- 前端 bridge + wrapper + 真实内嵌脚本共22项定向测试通过；额外locale完整性3项通过。
- 前端构建（含vue-tsc）与eslint通过。
- Playwright桌面深色/手机浅色/只读模式：无页面横向溢出、无控制台错误，账号模型添加/移出、来源暂停/恢复、16秒不自动请求通过。浏览器夹具使用合成数据，无真实请求。
- 独立QA发现的只读degraded“加入清单”和刷新失败保留旧健康状态均已修复，新增内嵌脚本回归覆盖；独立QA复验通过，包括Chromium两个opaque frame的隔离验证。
- Go admin handler/routes/cmd/server及完整middleware tests通过；audit canary证明导入body不记录。
- Python适配层11项测试通过，覆盖基线10入口、安全元数据、无GET DNS/probe、启停待应用、损坏overlay保留、per-model static优先、force不跳退避、提取缓存到期失败清除、SSRF/CSRF/只读及进程锁。
- Python最终全套74项通过（原有63项含新增中文账号名称测试，适配层11项）。

## 明确限制

- 现有配置来源在面板中只读，新增来源才可从面板启停/移除；不会重新导入或覆盖用户已有IP。
- 统计按来源、UTC日期汇总；尚无逐个真实动态出口IP的采样，因此不能指出哪个动态出口导致312。
- 原因没有通过随机对照或实际出口记录确认；不会以统计差异直接更换账号、供应商或代理。
- 已通过带令牌校验的同机私网入口连接主应用；既有Tailnet查看入口仍只读。
- knowledge_candidate: no，本次为项目特定适配，不晋升全局规范。

## 首次本地交付记录（上线前）

IMPLEMENTATION_READY，未提交/推送/部署。最终前端build、vue-tsc、22项定向测试和3项locale测试通过；Python全套74项通过；Go带embed整包构建通过，产物仅供本地评审。独立前端QA与Python源码复验均通过，本次未发现未解决的范围内阻塞问题。

Python适配层冻结SHA-256：`55563f81db1565dd4b374fb9ae4d41fe88c2bf11fc95632e14a43c8514044d45`。完整29文件源码manifest、包含新增文件的review patch与演示截图保存在部署工作区 `review-artifacts/own-monitor-20260920/`。

## 2026-09-20 用户授权发布

用户明确要求“面板先发布上线”，本次授权包含将已验证代码合入自有main、GitHub构建和现有蓝绿发布，以及探针管理入口的必要安装。

上线前已核对：主线仍为d9e6f1e21，线上green为f8c209764且healthy；已有Docker桥网关由实际网络检查获得。为保证管理接口不裸露，保留Tailnet只读8787，新增同机Docker桥上的令牌保护API端口8788。复用现有内部health-token受保护文件及应用已有只读挂载；不新增/导出秘密、不改公网/Caddy路由、不修改用户账号和IP。

Python的admin_panel仅接受字面量私网IPv4，所有API要求Bearer，写操作要求CSRF；既有只读入口仍拒绝写入。Go后台读取受保护令牌文件并注入服务端请求，不透传浏览器令牌，公网目标拒绝携带内部令牌。

发布器只增加两个非秘密连接参数的显式覆盖及候选一致性检查；线上发布脚本已有独立batch-image扩展，因此安装仅对核对过hash的现有脚本应用本次delta，禁止用本地主线整文件覆盖它。备份、CAS恢复与维护锁沿用现有探针安装规范，保留全部pins/账号覆盖层/代理覆盖层/统计。

### 同轮用户追加：清理代理池

保留原文件中的10个静态IP，移除其余动态配置/失效导入源，仅加入新Webshare新加坡SOCKS5轮换网关。新凭据先存Vault（item `23192d89-75e3-48e6-9c56-53ae4a497805`），通过hash-pinned consumer流式注入远端受保护暂存，Git只保存引用。

用户明确要求静态随机优先，本次设置 `static_proxy_order=random`：每个账号/模型每轮将10个静态入口洗牌、无放回尝试，全部未命中后才转入动态池，下一次续期重新洗牌；保留错误退避和长Retry-After，不额外触发强制探测。旧源统计继续保留，用于历史对比；它们不再参与新的探针。

### 发布连接验证

- 新增私网Bearer/CSRF/readonly隔离、令牌缺失/轮换/权限/链接/格式拒绝及私网绑定测试；加静态10入口无放回随机序列测试，Python78项通过。
- Go handler完整包及race测试通过，覆盖令牌文件逐请求读取、错误不发上游、公网目标拒绝及重定向不跟随。
- 线上manager与候选使用跨Python版本标准化AST比较，原有函数除账号名称相关3处外一致；随后按用户追加指令显式增加静态随机序列。
- 新网关已存Vault并通过固定hash消费者注入远端0600暂存；CLI操作完成后回到locked。

## 正式上线结果（2026-09-20）

GitHub生产发布成功：`https://github.com/Turtle-Li/sub2api/actions/runs/35511273437`。运行源码 `260b915e092705659f5454467870fdf217aab52c`，线上活动容器 `sub2api-blue`，镜像 `sub2api:auto-20260920-204854-260b915e`，healthy、restart_count=0，traffic accepting/background active。未登录访问管理API返回401，公共health为ok。

实际Chrome管理员登录页面 `/admin/codex-turn-state` 已验证：10静态、1动态、2账号6模型均有效，`static`与`webshare_sg`均已加载，刷新操作正常。旧供应商只留历史统计，新池不再使用它们。新SOCKS5网关经一次IP-echo请求验证HTTP200和公网出口；没有触发强制模型探测，故尚不声称新来源已有目标状态命中。

探针服务active/running，NRestarts=0。私有API未认证401、错误写入400、Tailnet只读写入403。原静态文件SHA256保持不变。新增精确UFW规则只允许本项目桥接口/网段访问管理端口，没有开放公网监听。

回滚备份（仅远端root可读）：`/opt/sub2api/codex-turn-state-manager/backups/own-admin-20260920-01/deployment.json`，记录原/新目标文件hash、权限与对应备份索引。安装事务已完成，暂存私密代理文件已删除；不得对完成的安装调用 `--recover`。回滚须持项目维护锁、比较当前hash、恢复对应代码及配置，保留pins/账号/统计，主应用走既有蓝绿回滚。私有Registry记录 `projects/sub2api-own-admin-panel-20260920.md` 保存防火墙和完整运维细节。

## 2026-09-21 续期提前量、完整成功耗时与静默刷新

用户反馈15分钟提前量过长，并要求可调整、显示平均成功耗时、倒计时和不闪烁的数据刷新。

- 提前量可从管理面板设为1–30整数分钟，持久保存，所有账号使用统一设置；未保存新设置时沿用既有配置，不自动把生产值调小。受既有管理员/Bearer/CSRF保护，Tailnet只读查看不可修改。
- 成功耗时定义为本轮第一次实际harvest开始到数据库验证写入成功，包含跨轮重试及退避；未完成、写入失败、进程重启中断的轮次不作为成功样本。旧来源TTFB/长度计数不能充当该指标。
- 前端每秒倒数，每10秒只读静默同步，后台标签暂停，重新可见立即同步。复用DOM节点，更新文本及必要属性；未改动列表不重新挂载，不覆盖未保存输入，不重置滚动。自动刷新不调用探针。
- 原15秒整表innerHTML替换与后来完全手动刷新均被本次明确需求取代。

旧日志只读分析：最近24小时可配对130轮，首响应日志到验证写入平均17.21秒、中位4.65秒、最长51秒；不含首请求耗时，为偏短估计，仅用于本次说明，不回填精确统计。

影响范围：Python调度读取统一提前量和成功计时；安全设置API及Go allowlist；内嵌管理面板的DOM更新和计时。保持10个静态随机无放回优先、单动态兜底、已有错误退避和Retry-After、账号清单及凭据不变。

### 2026-09-21 发布验证

GitHub run `35526492106` 成功，镜像 `sub2api:auto-20260921-014704-de57ca2d` 在green活动，healthy、restart_count=0、traffic accepting/background active。探针源代码安装事务完成，备份位于 `/opt/sub2api/codex-turn-state-manager/backups/timing-20260921-01/deployment.json`；仅升级运行源码，原config/静态IP/账号/代理覆盖层hash守卫通过。

83项Python测试在本地与服务器暂存均通过；27项前端定向测试、3项locale及production build通过。Go面板handler测试通过。独立QA复验通过，实际本地浏览器覆盖桌面/手机/只读、秒倒计时、10秒同步、DOM节点和输入/焦点保留、错误提示。

线上初始完整样本3个，平均3.4997秒、最近一次2.806秒；6模型有效，0过期；提前量仍15分钟（用户可自行保存新值）。该小样本仅是新计时口径的实测，不是未来时长保证。公共设置API无登录返回401，私有API返回有效设置和统计。

本轮macOS屏幕访问失败，未声称完成正式登录页面视觉验收；用正式服务状态、实际下发前端资源及已完成的本地同源构建浏览器验证闭环。旧任务的实际登录验收记录不替代本轮限制。
