import { apiClient } from "./client";

export type DesktopPromotionIcon =
  | "agent"
  | "chat"
  | "globe"
  | "link"
  | "sparkles"
  | "tool";

export type DesktopPromotionSurface = "discover" | "overview";

export interface DesktopPromotion {
  id: string;
  title: string;
  summary: string;
  target_url: string;
  cta_label: string;
  badge?: string;
  icon: DesktopPromotionIcon;
  surfaces: DesktopPromotionSurface[];
  enabled: boolean;
  sort_order: number;
  starts_at?: string;
  ends_at?: string;
}

export interface DesktopUpdatePolicy {
  schema_version: number;
  latest_version: string;
  minimum_supported_version: string;
  enforcement_enabled: boolean;
  enforce_after?: string;
  reason?: string;
  manual_download_url?: string;
}

export interface DesktopConsoleSettings {
  schema_version: number;
  control_plane_url: string;
  promotions: DesktopPromotion[];
  update_policy: DesktopUpdatePolicy;
}

export interface UpdateDesktopConsoleSettings {
  control_plane_url: string;
  promotions: DesktopPromotion[];
  update_policy: DesktopUpdatePolicy;
}

export interface DesktopStorageConfig {
  schema_version: number;
  enabled: boolean;
  provider: "tencent_cos";
  region: string;
  bucket: string;
  secret_id: string;
  secret_key?: string;
  public_base_url: string;
  release_prefix: string;
  theme_prefix: string;
  quarantine_prefix: string;
}

export interface DesktopStorageSettings {
  config: DesktopStorageConfig;
  secret_configured: boolean;
  endpoint: string;
  release_manifest_url: string;
  theme_catalog_url: string;
}

export interface DesktopStorageTestResult {
  ok: boolean;
  message: string;
  endpoint?: string;
  object_key?: string;
}

export async function getDesktopConsoleSettings(): Promise<DesktopConsoleSettings> {
  const { data } = await apiClient.get<DesktopConsoleSettings>(
    "/desktop-console/settings",
  );
  return data;
}

export async function updateDesktopConsoleSettings(
  settings: UpdateDesktopConsoleSettings,
): Promise<DesktopConsoleSettings> {
  const { data } = await apiClient.put<DesktopConsoleSettings>(
    "/desktop-console/settings",
    settings,
  );
  return data;
}

export async function getDesktopStorageSettings(): Promise<DesktopStorageSettings> {
  const { data } = await apiClient.get<DesktopStorageSettings>(
    "/desktop-console/storage",
  );
  return data;
}

export async function updateDesktopStorageSettings(
  config: DesktopStorageConfig,
): Promise<DesktopStorageSettings> {
  const { data } = await apiClient.put<DesktopStorageSettings>(
    "/desktop-console/storage",
    config,
  );
  return data;
}

export async function testDesktopStorageConnection(
  config: DesktopStorageConfig,
): Promise<DesktopStorageTestResult> {
  const { data } = await apiClient.post<DesktopStorageTestResult>(
    "/desktop-console/storage/test",
    config,
  );
  return data;
}
