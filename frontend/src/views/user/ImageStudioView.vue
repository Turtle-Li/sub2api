<template><AppLayout><div ref="host" /></AppLayout></template>
<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { createElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import AppLayout from '@/components/layout/AppLayout.vue'
import { useAuthStore } from '@/stores/auth'
import { Studio } from '@/features/image-studio/Studio'
const host = ref<HTMLElement>()
const auth = useAuthStore()
let root: Root | undefined
function render() { root?.render(auth.user ? createElement(Studio, { key: auth.user.id, userId: auth.user.id }) : null) }
onMounted(() => { if (host.value) { root = createRoot(host.value); render() } })
watch(() => auth.user?.id, render)
onBeforeUnmount(() => root?.unmount())
</script>
