/**
 * 公开站点文案（首页以外的一级页面）。
 *
 * 首页文案仍在 landing.ts 的 home.landing.* 下。
 *
 * 约定：凡是无法从仓库代码验证的商业信息（价格、可用率、时效、
 * 流程），一律写成 〔待确认：…〕，由 .placeholder 样式显式标出，
 * 交付时另附替换清单。不要把这类占位改成看起来已定稿的措辞。
 */

export default {
  site: {
    nav: {
      models: '模型与价格',
      platform: '能力与架构',
      docs: '接入文档',
      why: '我们的优势',
      changelog: '更新日志',
      openMenu: '打开导航',
      closeMenu: '关闭导航',
      moreLabel: '更多',
      desc: {
        models: '全部可调用模型与实付倍率',
        platform: '协议面、调度、计费与限流',
        docs: '从零到第一个请求',
        why: '服务保障与商业条款',
        changelog: '版本与公告归档',
        console: '控制台'
      }
    },

    footer: {
      groups: {
        product: '产品',
        developer: '开发者',
        service: '服务',
        legal: '条款'
      },
      links: {
        status: '渠道状态',
        console: '控制台',
        plaza: '模型广场',
        keyUsage: '用量查询',
        externalDocs: '外部文档'
      }
    },

    common: {
      backToHome: '返回首页',
      viewAll: '查看全部',
      copy: '复制',
      copied: '已复制'
    },

    // ── /models ────────────────────────────────────────────────
    models: {
      title: '模型与价格',
      lede: '所有模型共用同一个端点、同一套密钥和同一份配额策略。下表按分组列出可调用的模型与实付倍率。',
      live: '数据取自本站模型广场，与计费同源',
      loading: '正在读取模型目录',
      fallbackTitle: '当前展示的是代表性模型',
      fallbackBody: '模型广场未开放或暂时不可用，下表为代表性模型，不含实时价格。完整目录与实时价格请登录后在控制台查看。',
      tableLabel: '模型目录',
      count: '{shown} / {total} 个模型',
      groupLabel: '分组',
      allGroups: '全部',
      empty: '该分组下暂无可用模型',
      cols: {
        model: '模型 ID',
        group: '分组',
        capability: '能力',
        region: '区域',
        multiplier: '倍率',
        input: '输入',
        output: '输出'
      },
      unit: {
        perMillion: '每百万 tokens',
        multiplier: '×'
      },
      exclusive: '专属分组',
      subscription: '订阅制',
      viewPlaza: '完整价目表',
      pricingUnavailable: '价格以控制台为准',

      multiplier: {
        index: '01',
        title: '倍率是怎么算的',
        lede: '实付金额不是一个拍脑袋的折扣数字，而是由下面几层规则叠出来的。它们全部在后台可配置，也全部会体现在你的用量明细里。',
        items: {
          group: {
            term: '分组倍率',
            desc: '每个分组有一个基础倍率，实付 = 官方目录价 × 该倍率。你所在的分组决定了默认价位。'
          },
          user: {
            term: '专属倍率',
            desc: '管理员可以给单个账号配置专属倍率。配置后生效倍率取专属值，不再取分组倍率——登录后你在价目表里看到的就是自己的实际价格。'
          },
          peak: {
            term: '分时倍率',
            desc: '可以按时段给整单实付再乘一个系数，用于错峰定价。时段带时区，也可以设为仅工作日生效，周末整天按标准价。'
          },
          longContext: {
            term: '长上下文阶梯',
            desc: '多档模型按请求实际落在的档位计价。分组可以关闭阶梯计费，关闭后实付只按最低档，官方阶梯仅作参考。'
          },
          image: {
            term: '图片独立倍率',
            desc: '图片计费模型可以走一条独立倍率，不受分组倍率和专属倍率影响，避免文本折扣误伤图片成本。'
          }
        },
        note: '所有倍率都作用在官方目录价上，目录价与计费服务同源，不存在两套价格。'
      }
    },

    // ── /platform ──────────────────────────────────────────────
    platform: {
      title: '一个网关，四套协议面',
      lede: '大多数网关只做「OpenAI 兼容」。TurtleRoute 同时提供 Anthropic、OpenAI、Gemini 原生和 Codex 直连四套协议面，客户端不需要为了换后端而改写请求格式。',

      protocols: {
        index: '01',
        title: '协议面',
        lede: '四个前缀，各自保持上游原生的请求与响应格式。你用哪个 SDK，就走哪个前缀，不需要中间层翻译。',
        cols: { surface: '协议面', base: '前缀', desc: '说明' },
        items: {
          anthropic: {
            name: 'Anthropic Messages',
            desc: '/v1/messages 与 /v1/messages/count_tokens，Anthropic 官方 SDK 直接可用'
          },
          openai: {
            name: 'OpenAI 兼容',
            desc: '/v1/chat/completions、/v1/responses、/v1/embeddings、/v1/models，覆盖主流 OpenAI 客户端'
          },
          gemini: {
            name: 'Gemini 原生',
            desc: '/v1beta 下的 models 列表与调用，保留 Google 原生格式而非转译成 OpenAI 形状'
          },
          codex: {
            name: 'Codex 直连',
            desc: '/backend-api/codex 专用通道，供 Codex 系客户端直接接入'
          },
          antigravity: {
            name: 'Antigravity',
            desc: '/antigravity/v1 与 /antigravity/v1beta 专用端点'
          }
        }
      },

      routing: {
        index: '02',
        title: '调度与故障绕行',
        lede: '一个模型背后可以挂多条上游路由。健康检查不通过的路由会被移出轮询，调用方拿到的仍然是同一个端点、同一个密钥。',
        diagram: {
          gateway: '统一端点',
          pool: '账号池',
          note: '示意图。每条上游后面挂一串账号，实心表示可用，虚线表示已用尽或受限；实际号池状态请看渠道状态页。'
        },
        points: {
          multiAccount: {
            term: '多账号池',
            desc: '同一上游可以挂多个账号，OAuth 与 API Key 两种类型混用，用尽或受限时自动跳过'
          },
          sticky: {
            term: '粘性会话',
            desc: '同一会话尽量落在同一条路由上，避免多轮对话中途换后端导致上下文缓存失效'
          },
          failover: {
            term: '失败绕行',
            desc: '失败率升高的路由暂时移出轮询，恢复健康后自动回到池中，调用方无感'
          },
          local: {
            term: '本地后端',
            desc: 'Ollama、vLLM 这类自建后端与云端服务商同级注册，参与同一份轮询'
          }
        }
      },

      billing: {
        index: '03',
        title: '计费口径',
        lede: '按 token 计费，不是按请求数拍平均。输入、输出、缓存写入、缓存读取分别计量，用量明细可以逐日逐模型下钻。',
        points: {
          token: {
            term: 'Token 级计量',
            desc: '输入 / 输出 / 缓存创建 / 缓存读取四类分别统计，缓存写入还区分 5 分钟与 1 小时两档'
          },
          catalog: {
            term: '目录同源',
            desc: '官方参考价与计费目录同源，价目表上看到的和实际扣费用的是同一份数据'
          },
          quota: {
            term: '额度与钱包',
            desc: '支持 Key 限额模式与钱包余额两种口径，可设 5 小时 / 日 / 7 天 / 周 / 月多种限额窗口'
          },
          query: {
            term: '免登录查用量',
            desc: '拿着 API Key 就能在用量查询页看到消费与限额，Key 只在浏览器本地处理'
          }
        }
      },

      limits: {
        index: '04',
        title: '并发与限流',
        lede: '限流不只在用户这一层。用户级和账号级分别有并发上限，配合请求与 token 速率限制，避免单个调用方打穿整个号池。',
        points: {
          userConcurrency: { term: '用户级并发', desc: '限制单个账号同时在途的请求数' },
          accountConcurrency: { term: '账号级并发', desc: '限制单个上游账号的在途请求数，保护号池' },
          rate: { term: '速率限制', desc: '请求数与 token 数两个维度可分别配置' },
          publicIp: { term: '匿名接口限流', desc: '公开接口按来源 IP 限流，反代内网地址自动跳过' }
        }
      },

      capabilities: {
        index: '05',
        title: '不只是聊天',
        lede: '文本之外，同一套密钥还能调用图像、视频、语音和检索能力，不需要另外开账号。',
        groups: {
          images: {
            term: '图像',
            desc: '同步生成与编辑，异步任务，以及批量任务（提交、查询、逐条取结果、打包下载、取消）'
          },
          video: {
            term: '视频',
            desc: '生成、编辑、续写三类任务，各自可查状态与取内容'
          },
          voice: {
            term: '语音',
            desc: 'TTS、STT 与自定义音色，音色可上传、查询与试听'
          },
          realtime: {
            term: '实时与检索',
            desc: 'realtime 通道，以及 web_search 与 x_search 检索接口'
          }
        }
      },

      ops: {
        index: '06',
        title: '管理与运维',
        lede: '平台自带完整后台，不需要另外拼一套管理系统。',
        points: {
          admin: { term: '管理后台', desc: '用户、账号、分组、渠道、用量、审计日志都在 Web 界面里' },
          monitor: { term: '渠道监控', desc: '各条路由的实时健康状况与失败率，登录后可查' },
          payment: { term: '内置支付', desc: '易支付、支付宝官方、微信官方、Stripe 内置，用户可自助充值' },
          embed: { term: '外部系统嵌入', desc: '支持以 iframe 嵌入工单等外部系统，扩展后台功能' }
        }
      },

      cta: {
        title: '看看实际怎么接',
        lede: '协议面和端点的完整清单在接入文档里，从创建密钥到第一个请求大概五分钟。'
      }
    },

    // ── /docs ──────────────────────────────────────────────────
    docs: {
      title: '接入文档',
      lede: '把 base URL 指向本站，其余照旧。',
      sectionsLabel: '目录',
      sections: {
        quickstart: '快速开始',
        protocols: '协议与端点',
        clients: '客户端配置',
        media: '图像 · 视频 · 语音',
        errors: '错误与限流'
      },
      externalDocs: '还有一份外部文档',
      externalDocsDesc: '本站文档覆盖接入路径；更详细的运维与部署说明在外部文档站。',

      quickstart: {
        title: '五分钟发出第一个请求',
        lede: '你只需要一个 API Key 和一行 base URL 改动。已有的 Anthropic 或 OpenAI SDK 调用不用重写。',
        steps: {
          account: {
            title: '注册并登录',
            body: '在控制台创建账号。若站点关闭了自助注册，请联系管理员开通。'
          },
          key: {
            title: '创建 API Key',
            body: '控制台的密钥页可以创建 Key、设置限额窗口与到期时间。Key 创建后只完整显示一次，请立即保存。'
          },
          baseUrl: {
            title: '改 base URL',
            body: '把 SDK 的 base URL 指向本站，把 API Key 换成刚创建的那个。不需要改模型名以外的任何请求字段。'
          },
          send: {
            title: '发请求',
            body: '下面的示例可以直接复制运行，base URL 已经填成当前站点。'
          }
        },
        verify: {
          title: '怎么确认接通了',
          body: '拿 Key 到用量查询页（无需登录）查一次，能看到刚才那笔请求的 token 与费用，就说明计费链路也通了。'
        }
      },

      protocols: {
        title: '协议与端点',
        lede: '四套协议面共存，各自保留上游原生格式。选哪个取决于你手上的 SDK，而不是取决于最终要调哪个模型。',
        groups: {
          anthropic: 'Anthropic Messages',
          openai: 'OpenAI 兼容',
          gemini: 'Gemini 原生',
          images: '图像',
          video: '视频',
          voice: '语音',
          realtime: '实时与检索',
          account: '账务与用量'
        },
        pickTitle: '该选哪一个',
        pickBody: '用 Anthropic SDK 就走 /v1/messages，用 OpenAI SDK 就走 /v1/chat/completions 或 /v1/responses，用 Google SDK 就走 /v1beta。模型的实际归属由模型 ID 决定，与你选的协议面无关。'
      },

      clients: {
        title: '客户端配置',
        lede: '常见客户端只需要改两个环境变量。',
        items: {
          claudeCode: {
            title: 'Claude Code',
            body: '设置 base URL 与 auth token 两个环境变量后直接使用，模型名填本站支持的 Anthropic 系模型 ID。'
          },
          codex: {
            title: 'Codex',
            body: '走 /backend-api/codex 专用通道，或使用 OpenAI 兼容面的 /v1/responses。'
          },
          sdk: {
            title: '官方 SDK',
            body: 'Anthropic 与 OpenAI 官方 SDK 都只需要覆盖 base URL 与 api key 两个构造参数。'
          },
          gemini: {
            title: 'Google SDK',
            body: '把 API 端点指向本站的 /v1beta，保留 Google 原生请求格式。'
          }
        }
      },

      media: {
        title: '图像 · 视频 · 语音',
        lede: '这些能力和文本共用同一套密钥与配额，不需要单独开通。',
        asyncTitle: '同步、异步与批量',
        asyncBody: '图像生成有三种形态：同步接口直接返回结果；异步接口先拿任务 ID 再轮询；批量接口一次提交多条提示词，完成后可以逐条取内容或打包下载，中途也可以取消。长任务建议走异步或批量，避免同步请求超时。'
      },

      errors: {
        title: '错误与限流',
        lede: '错误响应保持上游原生格式，网关不额外包一层，方便你复用已有的错误处理。',
        limitsTitle: '你可能撞上的几种限制',
        limits: {
          concurrency: { term: '并发上限', desc: '账号同时在途的请求数超限，稍后重试即可' },
          rate: { term: '速率限制', desc: '请求数或 token 数超过窗口配额' },
          quota: { term: '额度耗尽', desc: 'Key 限额或钱包余额不足，需要充值或调整限额' },
          upstream: { term: '上游不可用', desc: '所有候选路由都不健康时才会透传失败，正常情况下会先自动绕行' }
        },
        retryTitle: '重试建议',
        retryBody: '限流类错误建议指数退避重试。网关本身已经在多条路由之间做过绕行，所以客户端不需要为了「换一家上游」而重试——那一层已经做了。'
      }
    },

    // ── /changelog ─────────────────────────────────────────────
    changelog: {
      title: '更新日志',
      lede: '平台能力与站点的变更记录，新的在上面。',
      note: '本页目前由站点维护。公告系统接入公开接口后，这里会自动同步后台发布的公告。',
      empty: '暂无记录',
      placeholderMark: '示例',
      tags: {
        feature: '新功能',
        improvement: '改进',
        fix: '修复',
        notice: '公告'
      }
    },

    // ── /why ───────────────────────────────────────────────────
    why: {
      title: '为什么选 TurtleRoute',
      lede: '技术之外，你更需要知道出问题时找谁、钱怎么退、发票怎么开。这几件事写在这里，不写在客服话术里。',
      items: {
        pool: {
          term: '官方一手号池',
          desc: '上游账号自建自管，不经过二级分销转手。号池状态在渠道状态页可查，不健康的路由会被自动移出轮询而不是让你的请求去撞。',
          fact: '〔待确认：号池来源与规模的具体表述〕'
        },
        refund: {
          term: '退款政策',
          desc: '未消耗的余额可申请退款。退款按未消耗部分原路退回，已产生的 token 消费不在退款范围内。',
          fact: '〔待确认：退款窗口期、手续费比例、申请入口与到账时效〕'
        },
        invoice: {
          term: '开具发票',
          desc: '支持为企业用户开具发票。提交抬头与税号后由财务处理。',
          fact: '〔待确认：发票类型（增值税普票/专票）、起开金额、开票周期与寄送方式〕'
        },
        support: {
          term: '客服在线',
          desc: '技术问题与账务问题都有人工响应，不是只有工单机器人。',
          fact: '〔待确认：在线时段、响应时效承诺、联系方式（工单/群/邮箱）〕'
        },
        billing: {
          term: '计费透明',
          desc: '按 token 计量，输入、输出、缓存写入、缓存读取分开统计。用量可以逐日逐模型下钻，拿着 Key 不登录也能查。倍率规则全部公开写在模型与价格页。',
          fact: ''
        },
        stability: {
          term: '可用性',
          desc: '同一模型挂多条上游路由，失败自动绕行；用户级与账号级双层并发控制，避免单个调用方打穿号池。',
          fact: '〔待确认：对外承诺的可用率数字与 SLA 条款，未确认前不要写具体百分比〕'
        }
      },
      contact: {
        title: '还有问题',
        body: '账务、发票、批量采购和企业接入可以直接联系我们。',
        placeholder: '〔待确认：对外联系方式〕'
      }
    }
  }
}
