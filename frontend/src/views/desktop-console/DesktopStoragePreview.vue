<template>
  <div class="min-h-screen bg-slate-950 px-6 py-8 text-slate-100">
    <div class="mx-auto max-w-5xl">
      <DesktopStorageEditor :preview-settings="previewSettings" />
    </div>
  </div>
</template>

<script setup lang="ts">
import DesktopStorageEditor from "./DesktopStorageEditor.vue";
import type { DesktopStorageSettings } from "@/api/desktopConsole";

const previewSettings: DesktopStorageSettings = {
  config: {
    schema_version: 1,
    enabled: true,
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
  release_manifest_url: "https://download.example.com/releases/latest.json",
  theme_catalog_url: "https://download.example.com/themes/catalog.json",
};
</script>
