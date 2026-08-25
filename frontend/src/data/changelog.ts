/**
 * 更新日志条目。
 *
 * 目前由站点手工维护。TODO：公告系统接入公开接口后改为从后端拉取。
 * 现状是 GET /api/v1/announcements 挂在需要登录的路由组下
 * （backend/internal/server/routes/user.go），且 targeting 只支持
 * subscription / balance 两类用户属性条件，对匿名访客没有语义。
 * 接入公开公告需要后端补一个「对访客可见」字段和一个公开只读端点。
 *
 * 内容是数据不是界面文案，所以中英文直接内联在这里，不进 i18n 包。
 *
 * ⚠️ 下面每条都带 placeholder: true——条目标题描述的能力在仓库里
 * 真实存在，但版本号和日期是我编的。替换成真实发布记录后请去掉该
 * 标记，页面上的「示例」角标会随之消失。
 */

export type ChangelogTag = 'feature' | 'improvement' | 'fix' | 'notice'

export interface ChangelogEntry {
  id: string
  /** ISO 日期，用于排序与显示 */
  date: string
  tag: ChangelogTag
  title: { zh: string; en: string }
  body: { zh: string; en: string }
  /** true 表示日期/版本尚未核实，页面会标注 */
  placeholder?: boolean
}

export const CHANGELOG: ChangelogEntry[] = [
  {
    id: 'batch-image',
    date: '2026-08-14',
    tag: 'feature',
    placeholder: true,
    title: { zh: '图片批量生成', en: 'Batch image generation' },
    body: {
      zh: '一次提交多条提示词，任务完成后可以逐条取内容或打包下载，中途可取消。长任务不再受同步请求超时限制。',
      en: 'Submit many prompts in one job, then fetch items individually or download them together; jobs can be cancelled mid-run. Long jobs no longer bound by synchronous request timeouts.',
    },
  },
  {
    id: 'antigravity',
    date: '2026-07-02',
    tag: 'feature',
    placeholder: true,
    title: { zh: 'Antigravity 专用端点', en: 'Antigravity endpoints' },
    body: {
      zh: '新增 /antigravity/v1 与 /antigravity/v1beta 两个专用前缀，配合混合调度模式使用。',
      en: 'Added the dedicated /antigravity/v1 and /antigravity/v1beta prefixes, for use with hybrid scheduling.',
    },
  },
  {
    id: 'time-pricing',
    date: '2026-06-18',
    tag: 'feature',
    placeholder: true,
    title: { zh: '分时倍率', en: 'Time-of-day rates' },
    body: {
      zh: '可按时段给整单实付再乘一个系数。时段带时区，并可设为仅工作日生效，周末整天按标准价计费。',
      en: 'A configurable multiplier can now apply to whole requests during set windows. Windows carry a timezone and can be limited to weekdays, leaving weekends at standard price.',
    },
  },
  {
    id: 'long-context',
    date: '2026-05-27',
    tag: 'improvement',
    placeholder: true,
    title: { zh: '长上下文阶梯计费', en: 'Long-context tiered billing' },
    body: {
      zh: '多档模型改为按请求实际落在的档位计价，分组可以单独关闭阶梯。价目表同步展示各档绝对单价。',
      en: 'Tiered models are now priced by the tier a request actually lands in, and tiering can be switched off per group. The price list shows the absolute price of each tier.',
    },
  },
  {
    id: 'channel-status',
    date: '2026-04-09',
    tag: 'feature',
    placeholder: true,
    title: { zh: '渠道状态页', en: 'Channel status page' },
    body: {
      zh: '登录后可以查看各条上游路由的实时健康状况与失败率，不用再靠猜。',
      en: 'Signed-in users can now see live health and failure rates for every upstream route instead of guessing.',
    },
  },
  {
    id: 'builtin-payment',
    date: '2026-03-12',
    tag: 'feature',
    placeholder: true,
    title: { zh: '内置支付', en: 'Built-in payments' },
    body: {
      zh: '易支付、支付宝官方、微信官方与 Stripe 内置，用户可以自助充值，不需要另外部署支付服务。',
      en: 'EasyPay, Alipay, WeChat Pay, and Stripe are built in, so users can top up themselves without a separately deployed payment service.',
    },
  },
]
