/**
 * Public site copy (top-level pages other than the homepage).
 *
 * Homepage copy still lives under home.landing.* in landing.ts.
 *
 * Convention: any commercial claim that cannot be verified from the
 * repository (prices, uptime, turnaround, process) is written as
 * [TBC: …] and rendered with the .placeholder style so it cannot be
 * mistaken for finished copy. Do not soften these into confident
 * wording before the real values are supplied.
 */

export default {
  site: {
    nav: {
      models: 'Models & pricing',
      platform: 'Platform',
      docs: 'Docs',
      why: 'Why us',
      changelog: 'Changelog',
      openMenu: 'Open navigation',
      closeMenu: 'Close navigation',
      moreLabel: 'More',
      desc: {
        models: 'Every callable model and its rate',
        platform: 'Protocols, routing, billing, limits',
        docs: 'From nothing to a first request',
        why: 'Service guarantees and terms',
        changelog: 'Releases and notices',
        console: 'Console'
      }
    },

    footer: {
      groups: {
        product: 'Product',
        developer: 'Developers',
        service: 'Service',
        legal: 'Legal'
      },
      links: {
        status: 'Channel status',
        console: 'Console',
        plaza: 'Model plaza',
        keyUsage: 'Key usage',
        externalDocs: 'External docs'
      }
    },

    common: {
      backToHome: 'Back to home',
      viewAll: 'View all',
      copy: 'Copy',
      copied: 'Copied'
    },

    // ── /models ────────────────────────────────────────────────
    models: {
      title: 'Models & pricing',
      lede: 'Every model shares one endpoint, one key, and one quota policy. The table below lists the callable models per group along with the rate you actually pay.',
      live: 'Read from this site’s model plaza — the same source billing uses',
      loading: 'Loading the model catalog',
      fallbackTitle: 'Showing a representative selection',
      fallbackBody: 'The model plaza is disabled or temporarily unavailable, so the table below is a representative selection without live pricing. Sign in and open the console for the full catalog.',
      tableLabel: 'Model catalog',
      count: '{shown} / {total} models',
      groupLabel: 'Group',
      allGroups: 'All',
      empty: 'No models available in this group',
      cols: {
        model: 'Model ID',
        group: 'Group',
        capability: 'Capability',
        region: 'Region',
        multiplier: 'Rate',
        input: 'Input',
        output: 'Output'
      },
      unit: {
        perMillion: 'per 1M tokens',
        multiplier: '×'
      },
      exclusive: 'Exclusive',
      subscription: 'Subscription',
      viewPlaza: 'Full price list',
      pricingUnavailable: 'See console for pricing',

      multiplier: {
        index: '01',
        title: 'How the rate is computed',
        lede: 'What you pay is not an arbitrary discount. It is the product of the layers below, all configurable in the admin panel and all visible in your usage breakdown.',
        items: {
          group: {
            term: 'Group rate',
            desc: 'Each group carries a base multiplier: what you pay is the official catalog price times that multiplier. Your group sets your default tier.'
          },
          user: {
            term: 'Per-account rate',
            desc: 'An admin can assign a multiplier to a single account. When set, it replaces the group rate — so once signed in, the price list shows your actual price, not a generic one.'
          },
          peak: {
            term: 'Time-of-day rate',
            desc: 'A further multiplier can apply to whole requests during configured windows. Windows carry a timezone and can be limited to weekdays, leaving weekends at standard price.'
          },
          longContext: {
            term: 'Long-context tiers',
            desc: 'Tiered models are priced by the tier a request actually lands in. A group can switch tiering off, in which case you pay the lowest tier and the official tiers are reference only.'
          },
          image: {
            term: 'Independent image rate',
            desc: 'Image-billed models can run on their own multiplier, unaffected by group and per-account rates, so a text discount never silently distorts image cost.'
          }
        },
        note: 'Every multiplier applies to the official catalog price, and that catalog is the same one the billing service reads. There is no second set of prices.'
      }
    },

    // ── /platform ──────────────────────────────────────────────
    platform: {
      title: 'One gateway, four protocol surfaces',
      lede: 'Most gateways offer “OpenAI compatibility” and stop there. TurtleRoute exposes Anthropic, OpenAI, native Gemini, and a direct Codex surface side by side, so switching backends never means rewriting your request format.',

      protocols: {
        index: '01',
        title: 'Protocol surfaces',
        lede: 'Four prefixes, each preserving its upstream’s native request and response shape. Use the prefix that matches your SDK — nothing is translated in between.',
        cols: { surface: 'Surface', base: 'Prefix', desc: 'Notes' },
        items: {
          anthropic: {
            name: 'Anthropic Messages',
            desc: '/v1/messages and /v1/messages/count_tokens; works with the official Anthropic SDK unchanged'
          },
          openai: {
            name: 'OpenAI compatible',
            desc: '/v1/chat/completions, /v1/responses, /v1/embeddings, /v1/models — covers mainstream OpenAI clients'
          },
          gemini: {
            name: 'Gemini native',
            desc: 'Model listing and calls under /v1beta, keeping Google’s native shape rather than reshaping it into OpenAI’s'
          },
          codex: {
            name: 'Codex direct',
            desc: 'A dedicated /backend-api/codex channel for Codex-family clients'
          },
          antigravity: {
            name: 'Antigravity',
            desc: 'Dedicated /antigravity/v1 and /antigravity/v1beta endpoints'
          }
        }
      },

      routing: {
        index: '02',
        title: 'Scheduling and failover',
        lede: 'A model can sit behind several upstream routes. Routes failing health checks drop out of rotation; callers keep hitting the same endpoint with the same key.',
        diagram: {
          gateway: 'Unified endpoint',
          pool: 'Account pool',
          note: 'Illustrative. Each upstream carries a row of accounts — filled means available, dashed means exhausted or throttled. Real pool state is on the channel status page.'
        },
        points: {
          multiAccount: {
            term: 'Account pools',
            desc: 'One upstream can carry many accounts, mixing OAuth and API-key types; exhausted or throttled accounts are skipped automatically'
          },
          sticky: {
            term: 'Sticky sessions',
            desc: 'A conversation stays on one route where possible, so multi-turn context caching is not thrown away mid-thread'
          },
          failover: {
            term: 'Failover',
            desc: 'Routes with elevated failure rates leave rotation and rejoin once healthy again — invisible to callers'
          },
          local: {
            term: 'Local backends',
            desc: 'Ollama, vLLM, and anything else you host register as peers of cloud providers and join the same rotation'
          }
        }
      },

      billing: {
        index: '03',
        title: 'How billing is measured',
        lede: 'Billing is per token, not a flattened per-request average. Input, output, cache writes, and cache reads are metered separately, and usage drills down by day and by model.',
        points: {
          token: {
            term: 'Token-level metering',
            desc: 'Input, output, cache creation, and cache read counted separately; cache writes further split into 5-minute and 1-hour tiers'
          },
          catalog: {
            term: 'One catalog',
            desc: 'Official reference prices come from the same catalog billing reads, so the price list and the charge agree by construction'
          },
          quota: {
            term: 'Quota and wallet',
            desc: 'Both key-quota and wallet-balance modes are supported, with 5-hour, daily, 7-day, weekly, and monthly limit windows'
          },
          query: {
            term: 'Usage without signing in',
            desc: 'An API key alone is enough to check spend and limits on the usage page; the key is processed in your browser only'
          }
        }
      },

      limits: {
        index: '04',
        title: 'Concurrency and rate limits',
        lede: 'Limits apply at more than one layer. Per-user and per-account concurrency caps combine with request and token rate limits so a single caller cannot drain the pool.',
        points: {
          userConcurrency: { term: 'Per-user concurrency', desc: 'Caps how many requests one account can have in flight' },
          accountConcurrency: { term: 'Per-account concurrency', desc: 'Caps in-flight requests per upstream account, protecting the pool' },
          rate: { term: 'Rate limits', desc: 'Request count and token count are configurable independently' },
          publicIp: { term: 'Anonymous endpoint limits', desc: 'Public endpoints are limited by source IP; internal proxy addresses are skipped' }
        }
      },

      capabilities: {
        index: '05',
        title: 'Not just chat',
        lede: 'Beyond text, the same key reaches image, video, voice, and retrieval capabilities. Nothing to enable separately.',
        groups: {
          images: {
            term: 'Images',
            desc: 'Synchronous generation and edits, async jobs, and batch jobs (submit, poll, fetch per item, bulk download, cancel)'
          },
          video: {
            term: 'Video',
            desc: 'Generation, edits, and extensions, each with status and content retrieval'
          },
          voice: {
            term: 'Voice',
            desc: 'TTS, STT, and custom voices that can be uploaded, listed, and previewed'
          },
          realtime: {
            term: 'Realtime and search',
            desc: 'A realtime channel plus web_search and x_search retrieval endpoints'
          }
        }
      },

      ops: {
        index: '06',
        title: 'Operations',
        lede: 'The platform ships with a full admin panel — there is no second system to assemble.',
        points: {
          admin: { term: 'Admin panel', desc: 'Users, accounts, groups, channels, usage, and audit logs all in one web UI' },
          monitor: { term: 'Channel monitoring', desc: 'Live health and failure rates per route, available once signed in' },
          payment: { term: 'Built-in payments', desc: 'EasyPay, Alipay, WeChat Pay, and Stripe are built in for self-service top-ups' },
          embed: { term: 'External embeds', desc: 'Ticketing and other external systems can be embedded via iframe to extend the panel' }
        }
      },

      cta: {
        title: 'See how it actually connects',
        lede: 'The full list of surfaces and endpoints is in the docs. Creating a key to first request takes about five minutes.'
      }
    },

    // ── /docs ──────────────────────────────────────────────────
    docs: {
      title: 'Documentation',
      lede: 'Point your base URL here. Everything else stays as it is.',
      sectionsLabel: 'Contents',
      sections: {
        quickstart: 'Quickstart',
        protocols: 'Protocols & endpoints',
        clients: 'Client setup',
        media: 'Image · video · voice',
        errors: 'Errors & limits'
      },
      externalDocs: 'There is also an external doc site',
      externalDocsDesc: 'These pages cover integration. Deeper deployment and operations material lives on the external doc site.',

      quickstart: {
        title: 'First request in five minutes',
        lede: 'All you need is an API key and one changed base URL. Existing Anthropic or OpenAI SDK calls do not get rewritten.',
        steps: {
          account: {
            title: 'Create an account',
            body: 'Register in the console. If self-service registration is disabled on this site, ask an administrator to provision access.'
          },
          key: {
            title: 'Mint an API key',
            body: 'The keys page lets you create a key and set its limit window and expiry. A key is shown in full exactly once — save it immediately.'
          },
          baseUrl: {
            title: 'Change the base URL',
            body: 'Point your SDK’s base URL at this site and swap in the key you just created. No request field other than the model name needs to change.'
          },
          send: {
            title: 'Send it',
            body: 'The example below is ready to run — the base URL is already filled in with this site’s origin.'
          }
        },
        verify: {
          title: 'Confirming it worked',
          body: 'Paste the key into the usage page (no sign-in required). If you can see that request’s tokens and cost, the billing path is connected too.'
        }
      },

      protocols: {
        title: 'Protocols & endpoints',
        lede: 'Four surfaces coexist, each keeping its upstream’s native shape. Which one you pick follows from the SDK in your hand, not from which model you ultimately want.',
        groups: {
          anthropic: 'Anthropic Messages',
          openai: 'OpenAI compatible',
          gemini: 'Gemini native',
          images: 'Images',
          video: 'Video',
          voice: 'Voice',
          realtime: 'Realtime & search',
          account: 'Account & usage'
        },
        pickTitle: 'Which one to use',
        pickBody: 'Anthropic SDK → /v1/messages. OpenAI SDK → /v1/chat/completions or /v1/responses. Google SDK → /v1beta. Which upstream ultimately serves the call is decided by the model ID, not by the surface you chose.'
      },

      clients: {
        title: 'Client setup',
        lede: 'Common clients need two environment variables changed.',
        items: {
          claudeCode: {
            title: 'Claude Code',
            body: 'Set the base URL and auth token environment variables, then use it as normal with any Anthropic-family model ID this site supports.'
          },
          codex: {
            title: 'Codex',
            body: 'Use the dedicated /backend-api/codex channel, or the OpenAI-compatible /v1/responses surface.'
          },
          sdk: {
            title: 'Official SDKs',
            body: 'The Anthropic and OpenAI SDKs both need only their base URL and api key constructor arguments overridden.'
          },
          gemini: {
            title: 'Google SDK',
            body: 'Point the API endpoint at this site’s /v1beta and keep Google’s native request format.'
          }
        }
      },

      media: {
        title: 'Image · video · voice',
        lede: 'These share the same key and quota as text. Nothing separate to enable.',
        asyncTitle: 'Sync, async, and batch',
        asyncBody: 'Image generation comes in three shapes: the synchronous endpoint returns directly; the async endpoint hands back a task ID to poll; the batch endpoint takes many prompts at once and lets you fetch items individually, download them together, or cancel mid-run. Prefer async or batch for long jobs so a synchronous request cannot time out.'
      },

      errors: {
        title: 'Errors & limits',
        lede: 'Error responses keep their upstream shape — the gateway does not wrap them in another envelope — so your existing error handling still applies.',
        limitsTitle: 'Limits you may hit',
        limits: {
          concurrency: { term: 'Concurrency cap', desc: 'Too many in-flight requests for the account; retry shortly' },
          rate: { term: 'Rate limit', desc: 'Request count or token count exceeded the window’s allowance' },
          quota: { term: 'Quota exhausted', desc: 'Key quota or wallet balance is spent; top up or adjust the limit' },
          upstream: { term: 'Upstream unavailable', desc: 'A failure only reaches you once every candidate route is unhealthy; otherwise it is routed around first' }
        },
        retryTitle: 'Retry guidance',
        retryBody: 'Back off exponentially on rate-limit errors. The gateway already routes around unhealthy upstreams, so there is no need to retry merely to “try another provider” — that layer is already handled.'
      }
    },

    // ── /changelog ─────────────────────────────────────────────
    changelog: {
      title: 'Changelog',
      lede: 'Platform and site changes, newest first.',
      note: 'This page is currently maintained by hand. Once the announcement system exposes a public endpoint, published notices will sync here automatically.',
      empty: 'Nothing recorded yet',
      placeholderMark: 'Sample',
      tags: {
        feature: 'Feature',
        improvement: 'Improvement',
        fix: 'Fix',
        notice: 'Notice'
      }
    },

    // ── /why ───────────────────────────────────────────────────
    why: {
      title: 'Why TurtleRoute',
      lede: 'Past the engineering, what you actually need to know is who answers when something breaks, how refunds work, and how to get an invoice. Those answers belong on a page, not in a support script.',
      items: {
        pool: {
          term: 'First-party account pool',
          desc: 'Upstream accounts are provisioned and operated by us, not resold through an intermediary. Pool health is visible on the channel status page, and unhealthy routes leave rotation rather than letting your request hit them.',
          fact: '[TBC: exact wording for pool provenance and scale]'
        },
        refund: {
          term: 'Refunds',
          desc: 'Unspent balance can be refunded. Refunds return the unconsumed portion by the original payment path; tokens already consumed are not refundable.',
          fact: '[TBC: refund window, any processing fee, where to file, time to settle]'
        },
        invoice: {
          term: 'Invoicing',
          desc: 'Invoices are available for business customers. Submit your entity name and tax ID and finance handles the rest.',
          fact: '[TBC: invoice types, minimum amount, issuing cycle, delivery method]'
        },
        support: {
          term: 'Human support',
          desc: 'Both technical and billing questions reach a person, not only a ticket bot.',
          fact: '[TBC: hours of availability, response-time commitment, contact channels]'
        },
        billing: {
          term: 'Transparent billing',
          desc: 'Metered per token, with input, output, cache writes, and cache reads counted separately. Usage drills down by day and by model, and an API key alone is enough to check it without signing in. Every multiplier rule is published on the models page.',
          fact: ''
        },
        stability: {
          term: 'Availability',
          desc: 'Each model sits behind multiple upstream routes with automatic failover, and two layers of concurrency control keep one caller from draining the pool.',
          fact: '[TBC: the uptime figure and SLA terms you commit to publicly — do not publish a percentage until confirmed]'
        }
      },
      contact: {
        title: 'Still have questions',
        body: 'Billing, invoicing, volume purchasing, and enterprise onboarding can reach us directly.',
        placeholder: '[TBC: public contact details]'
      }
    }
  }
}
