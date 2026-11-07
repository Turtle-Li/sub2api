<template>
  <div v-if="publicView" class="min-h-screen bg-gray-50 dark:bg-dark-950">
    <PlazaNavBar login-redirect="/monitor" />
    <main class="mx-auto max-w-[1680px] px-4 py-7 sm:px-6 lg:px-8"><GroupStatusDashboard /></main>
  </div>
  <AppLayout v-else><GroupStatusDashboard /></AppLayout>
</template>

<script setup lang="ts">
import AppLayout from '@/components/layout/AppLayout.vue'
import PlazaNavBar from '@/components/modelPlaza/PlazaNavBar.vue'
import GroupStatusDashboard from '@/features/channel-monitor-v3/GroupStatusDashboard.vue'
defineProps<{ publicView?: boolean }>()
</script>
