<template>
  <div class="dark min-h-screen bg-slate-950 text-slate-100">
    <div
      class="pointer-events-none fixed inset-0 opacity-70"
      style="background:
        radial-gradient(circle at 18% 0%, rgba(79, 122, 255, 0.2), transparent 34rem),
        radial-gradient(circle at 88% 16%, rgba(93, 211, 184, 0.12), transparent 28rem);"
    ></div>

    <header
      class="sticky top-0 z-20 border-b border-white/10 bg-slate-950/85 backdrop-blur-xl"
    >
      <div class="mx-auto flex h-16 max-w-7xl items-center justify-between px-4 sm:px-6 lg:px-8">
        <RouterLink to="/tt-switch-console" class="flex items-center gap-3">
          <span
            class="grid h-9 w-9 place-items-center rounded-xl border border-blue-300/25 bg-blue-400/10 text-xs font-bold tracking-tight text-blue-100 shadow-lg shadow-blue-950/40"
          >
            TT
          </span>
          <span>
            <span class="block text-sm font-semibold tracking-wide text-white">TT Switch</span>
            <span class="block text-[11px] uppercase tracking-[0.2em] text-slate-500">Console</span>
          </span>
        </RouterLink>

        <div class="flex items-center gap-2 text-sm">
          <span class="hidden text-slate-400 sm:inline">
            {{ authStore.user?.username || "Administrator" }}
          </span>
          <RouterLink
            to="/admin/dashboard"
            class="rounded-lg px-3 py-2 text-slate-300 transition hover:bg-white/5 hover:text-white"
          >
            服务管理台
          </RouterLink>
          <button
            type="button"
            class="rounded-lg px-3 py-2 text-slate-400 transition hover:bg-white/5 hover:text-white"
            @click="logout"
          >
            退出
          </button>
        </div>
      </div>
    </header>

    <main class="relative mx-auto max-w-7xl px-4 py-8 sm:px-6 lg:px-8">
      <slot />
    </main>
  </div>
</template>

<script setup lang="ts">
import { RouterLink, useRouter } from "vue-router";
import { useAuthStore } from "@/stores/auth";

const authStore = useAuthStore();
const router = useRouter();

async function logout(): Promise<void> {
  await authStore.logout();
  await router.push({
    path: "/login",
    query: { redirect: "/tt-switch-console" },
  });
}
</script>
