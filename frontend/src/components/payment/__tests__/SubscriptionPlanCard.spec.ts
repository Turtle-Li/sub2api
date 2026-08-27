import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";
import { createPinia } from "pinia";
import { createI18n } from "vue-i18n";
import type { SubscriptionPlan } from "@/types/payment";
import SubscriptionPlanCard from "../SubscriptionPlanCard.vue";

const i18n = createI18n({
  legacy: false,
  locale: "en",
  fallbackWarn: false,
  missingWarn: false,
  messages: {
    en: {
      payment: {
        days: "days",
        weeks: "weeks",
        months: "months",
        perMonth: "month",
        models: "Models",
        planCard: {
          quota: "Quota",
          rate: "Rate",
          unlimited: "Unlimited",
        },
        subscribeNow: "Subscribe now",
      },
    },
  },
});

type PricingProps = {
  displayCurrency?: string;
  usdToCnyRate?: number;
  selected?: boolean;
  featured?: boolean;
};

const mountPlanCard = (
  groupPlatform: string,
  overrides: Partial<SubscriptionPlan> = {},
  pricing: PricingProps = {},
) =>
  mount(SubscriptionPlanCard, {
    props: {
      ...pricing,
      plan: {
        id: 1,
        group_id: 10,
        group_platform: groupPlatform,
        name: "Pro",
        price: 10,
        amount: 1000,
        features: [],
        rate_multiplier: 1,
        validity_days: 30,
        validity_unit: "day",
        supported_model_scopes: ["claude", "gemini_text", "gemini_image"],
        is_active: true,
        ...overrides,
      },
    },
    global: { plugins: [i18n, createPinia()] },
  });

describe("SubscriptionPlanCard", () => {
  it("does not show Antigravity model scopes for OpenAI plans", () => {
    const wrapper = mountPlanCard("openai")
    const text = wrapper.text();

    expect(wrapper.classes()).toContain("payment-product-card")
    expect(wrapper.find(".payment-product-card__body").exists()).toBe(true)
    expect(wrapper.find(".payment-product-card__list").exists()).toBe(true)
    expect(wrapper.find("button").classes()).toContain("payment-product-card__action")

    expect(text).not.toContain("Claude");
    expect(text).not.toContain("Gemini");
    expect(text).not.toContain("Imagen");
  });

  it("shows model scopes for Antigravity plans", () => {
    const text = mountPlanCard("antigravity").text();

    expect(text).toContain("Claude");
    expect(text).toContain("Gemini");
    expect(text).toContain("Imagen");
  });

  // #4607：管理端保存的单位是复数（months/weeks），此前用户侧只匹配单数
  // 'month'，「1 个月」的套餐卡片被显示成「1天」。测试环境的 vue-i18n 为
  // runtime-only 构建，t() 原样返回 key，故按 key 断言单位分支。
  it("renders plural admin-form validity units instead of mislabeled days (#4607)", () => {
    expect(mountPlanCard("openai", { validity_days: 1, validity_unit: "months" }).text()).toContain("/ payment.perMonth");
    expect(mountPlanCard("openai", { validity_days: 3, validity_unit: "months" }).text()).toContain("/ 3payment.months");
    expect(mountPlanCard("openai", { validity_days: 2, validity_unit: "weeks" }).text()).toContain("/ 2payment.weeks");
    expect(mountPlanCard("openai", { validity_days: 30, validity_unit: "day" }).text()).toContain("/ 30payment.days");
  });

  // The card used to print plan.price behind a symbol derived from plan.currency
  // (defaulting to USD), while the confirm step converted the same plan into the
  // gateway currency — one plan, two prices. Both now follow the server rule.
  it("prices the plan in the gateway currency, not the plan's nominal currency", () => {
    const text = mountPlanCard("openai", { currency: "USD", original_price: 20 }, {
      displayCurrency: "CNY",
    }).text();

    expect(text).toContain("¥10.00");
    expect(text).toContain("¥20.00");
    expect(text).not.toContain("$10");
  });

  // Mirrors calculateSubscriptionGatewayBaseAmount: the rate is applied only for
  // the default gateway currency. A USD gateway charges the plan price as-is.
  it("applies the subscription rate only for the default gateway currency", () => {
    expect(mountPlanCard("openai", {}, { displayCurrency: "CNY", usdToCnyRate: 6.75 }).text())
      .toContain("¥67.50");
    expect(mountPlanCard("openai", {}, { displayCurrency: "USD", usdToCnyRate: 6.75 }).text())
      .toContain("$10.00");
  });

  // Long names wrap and stay fully readable. The card no longer clamps them to
  // a fixed two-line box — heights come from content, and the grid supplies the
  // shared baseline — so the title must not be truncated or line-clamped.
  it.each([
    ["long Chinese", "企业全球加速专业订阅套餐（含高级模型与优先支持）"],
    ["long English", "Enterprise Global Acceleration Subscription with Priority Support"],
    ["unbroken token", "EnterpriseGlobalAccelerationSubscriptionWithPrioritySupport1234567890"],
  ])("keeps the full %s plan title readable", (_label, name) => {
    const title = mountPlanCard("openai", { name }).get("h3");

    expect(title.text()).toBe(name);
    expect(title.attributes("title")).toBe(name);
    expect(title.classes()).toContain("payment-product-card__title");
    expect(title.classes()).not.toContain("truncate");
    expect(title.classes()).not.toContain("line-clamp-2");
  });

  // Reset cards are a headline entitlement, so they state both how many and for
  // how long. The period is a count plus a unit on the server; days is only the
  // default for plans saved before units existed.
  it("spells out reset card count and validity period", () => {
    const monthly = mountPlanCard("openai", {
      entitlements: {
        balance_bonus: 0,
        reset_card_count: 3,
        reset_card_expiry_days: 2,
        reset_card_expiry_unit: "month",
        concurrency: 0,
      },
    }).text();
    expect(monthly).toContain("3 payment.entitlements.resetCards");

    const legacy = mountPlanCard("openai", {
      entitlements: {
        balance_bonus: 0,
        reset_card_count: 1,
        reset_card_expiry_days: 14,
        concurrency: 0,
      },
    });
    const resetItem = legacy.findAll("li").find((node) => node.text().includes("resetCards"));
    expect(resetItem?.classes()).toContain("payment-product-card__list-item--benefit");
  });

  it("marks paid entitlements apart from plain quota facts", () => {
    const wrapper = mountPlanCard("openai", {
      entitlements: { balance_bonus: 20, reset_card_count: 0, reset_card_expiry_days: 0, concurrency: 8 },
    });
    const benefits = wrapper.findAll(".payment-product-card__list-item--benefit");

    expect(benefits).toHaveLength(2);
    expect(wrapper.text()).toContain("payment.entitlements.balanceBonus +20 payment.creditUnit");
    expect(wrapper.text()).toContain("payment.entitlements.concurrency");
  });

  // The whole card is the control. A card that looks selectable but only reacts
  // on a small button at its bottom edge reads as broken.
  it("emits select from the card body, not just the action button", async () => {
    const wrapper = mountPlanCard("openai");

    await wrapper.trigger("click");
    await wrapper.get("button").trigger("click");

    expect(wrapper.emitted("select")).toHaveLength(2);
    expect(wrapper.attributes("aria-pressed")).toBe("false");
  });

  it("shows the selected and recommended states without conflating them", () => {
    const selected = mountPlanCard("openai", {}, { selected: true });
    expect(selected.classes()).toContain("payment-product-card--selected");
    expect(selected.attributes("aria-pressed")).toBe("true");
    expect(selected.find(".payment-product-card__ribbon").exists()).toBe(false);

    const featured = mountPlanCard("openai", {}, { featured: true });
    expect(featured.classes()).toContain("payment-product-card--featured");
    expect(featured.classes()).not.toContain("payment-product-card--selected");
    expect(featured.find(".payment-product-card__ribbon").exists()).toBe(true);
  });
});
