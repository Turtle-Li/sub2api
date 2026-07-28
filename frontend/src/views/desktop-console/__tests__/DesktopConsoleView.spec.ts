import { beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { defineComponent, h } from "vue";

import DesktopConsoleView from "../DesktopConsoleView.vue";
import type { DesktopPromotion } from "@/api/desktopConsole";

const {
  getDesktopConsoleSettings,
  updateDesktopConsoleSettings,
  getDesktopStorageSettings,
  updateDesktopStorageSettings,
  testDesktopStorageConnection,
  showError,
  showSuccess,
} = vi.hoisted(() => ({
  getDesktopConsoleSettings: vi.fn(),
  updateDesktopConsoleSettings: vi.fn(),
  getDesktopStorageSettings: vi.fn(),
  updateDesktopStorageSettings: vi.fn(),
  testDesktopStorageConnection: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock("@/api/desktopConsole", () => ({
  getDesktopConsoleSettings,
  updateDesktopConsoleSettings,
  getDesktopStorageSettings,
  updateDesktopStorageSettings,
  testDesktopStorageConnection,
}));

vi.mock("@/stores", () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
  }),
}));

vi.mock("@/components/desktop-console/DesktopConsoleLayout.vue", () => ({
  default: defineComponent({
    setup(_, { slots }) {
      return () => h("div", { class: "desktop-console-layout-stub" }, slots.default?.());
    },
  }),
}));

vi.mock("@/components/auth/TotpStepUpDialog.vue", () => ({
  default: defineComponent({
    setup: () => () => h("div"),
  }),
}));

vi.mock("@/composables/useStepUp", () => ({
  useStepUp: () => ({
    run: (operation: () => Promise<unknown>) => operation(),
  }),
  isStepUpBlocked: () => false,
  isStepUpCancelled: () => false,
  stepUpBlockReason: () => "",
}));

const promotion: DesktopPromotion = {
  id: "turtle-chat",
  title: "Turtle Chat",
  summary: "网页聊天",
  target_url: "https://chat.example.com",
  cta_label: "打开",
  badge: "推荐",
  icon: "chat",
  surfaces: ["discover"],
  enabled: true,
  sort_order: 10,
  starts_at: "",
  ends_at: "",
};

describe("TT Switch desktop console", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    getDesktopConsoleSettings.mockResolvedValue({
      schema_version: 1,
      control_plane_url: "https://accounts.example.com",
      promotions: [promotion],
      update_policy: {
        schema_version: 1,
        latest_version: "1.4.0",
        minimum_supported_version: "1.2.0",
        enforcement_enabled: false,
        enforce_after: "",
        reason: "",
        manual_download_url: "https://download.example.com/tt-switch",
      },
    });
    updateDesktopConsoleSettings.mockImplementation(async (payload) => ({
      schema_version: 1,
      ...payload,
    }));
    getDesktopStorageSettings.mockResolvedValue({
      config: {
        schema_version: 1,
        enabled: false,
        provider: "tencent_cos",
        region: "ap-guangzhou",
        bucket: "tt-switch-1250000000",
        secret_id: "AKIDEXAMPLE",
        secret_key: "",
        public_base_url: "https://download.example.com",
        release_prefix: "releases/",
        theme_prefix: "themes/",
        quarantine_prefix: "theme-quarantine/",
      },
      secret_configured: true,
      endpoint: "https://cos.ap-guangzhou.myqcloud.com",
      release_manifest_url:
        "https://download.example.com/releases/latest.json",
      theme_catalog_url: "https://download.example.com/themes/catalog.json",
    });
    updateDesktopStorageSettings.mockImplementation(async (config) => ({
      config: { ...config, secret_key: "" },
      secret_configured: true,
      endpoint: "https://cos.ap-guangzhou.myqcloud.com",
      release_manifest_url:
        "https://download.example.com/releases/latest.json",
      theme_catalog_url: "https://download.example.com/themes/catalog.json",
    }));
    testDesktopStorageConnection.mockResolvedValue({
      ok: true,
      message: "ok",
      endpoint: "https://cos.ap-guangzhou.myqcloud.com",
      object_key: "releases/_probes/test.txt",
    });
  });

  it("loads and saves only the desktop settings contract", async () => {
    const wrapper = mount(DesktopConsoleView);
    await flushPromises();

    expect(wrapper.text()).toContain("桌面端设置");
    expect(
      (
        wrapper.get('input[placeholder="Turtle Chat"]')
          .element as HTMLInputElement
      ).value,
    ).toBe("Turtle Chat");
    const controlPlane = wrapper.get(
      '[data-testid="desktop-console-control-plane"]',
    );
    expect((controlPlane.element as HTMLInputElement).value).toBe(
      "https://accounts.example.com",
    );

    await controlPlane.setValue("https://new-accounts.example.com/");
    await wrapper.get('[data-testid="desktop-console-save"]').trigger("click");
    await flushPromises();

    expect(updateDesktopConsoleSettings).toHaveBeenCalledWith({
      control_plane_url: "https://new-accounts.example.com",
      promotions: [promotion],
      update_policy: {
        schema_version: 1,
        latest_version: "1.4.0",
        minimum_supported_version: "1.2.0",
        enforcement_enabled: false,
        enforce_after: "",
        reason: "",
        manual_download_url: "https://download.example.com/tt-switch",
      },
    });
    expect(showSuccess).toHaveBeenCalledWith("TT Switch 设置已保存");
  });

  it("rejects an insecure public control-plane URL before the request", async () => {
    const wrapper = mount(DesktopConsoleView);
    await flushPromises();

    await wrapper
      .get('[data-testid="desktop-console-control-plane"]')
      .setValue("http://accounts.example.com");
    await wrapper.get('[data-testid="desktop-console-save"]').trigger("click");
    await flushPromises();

    expect(updateDesktopConsoleSettings).not.toHaveBeenCalled();
    expect(showError).toHaveBeenCalledWith(
      "账号控制面必须是 HTTPS Origin，不能包含路径、参数或登录凭据",
    );
  });

  it("saves and probes the dedicated Tencent COS configuration", async () => {
    const wrapper = mount(DesktopConsoleView);
    await flushPromises();

    expect(wrapper.text()).toContain("腾讯云 COS");
    expect(
      (
        wrapper.get('[data-testid="desktop-storage-bucket"]')
          .element as HTMLInputElement
      ).value,
    ).toBe("tt-switch-1250000000");

    await wrapper.get('[data-testid="desktop-storage-enabled"]').setValue(true);
    await wrapper.get('[data-testid="desktop-storage-save"]').trigger("click");
    await flushPromises();

    expect(updateDesktopStorageSettings).toHaveBeenCalledWith(
      expect.objectContaining({
        enabled: true,
        provider: "tencent_cos",
        region: "ap-guangzhou",
        bucket: "tt-switch-1250000000",
        secret_key: "",
        release_prefix: "releases/",
        theme_prefix: "themes/",
        quarantine_prefix: "theme-quarantine/",
      }),
    );
    expect(showSuccess).toHaveBeenCalledWith("TT Switch COS 配置已保存");

    await wrapper.get('[data-testid="desktop-storage-test"]').trigger("click");
    await flushPromises();
    expect(testDesktopStorageConnection).toHaveBeenCalledWith(
      expect.objectContaining({ enabled: true }),
    );
    expect(showSuccess).toHaveBeenCalledWith(
      "COS 写入、公开读取和删除测试通过",
    );
  });
});
