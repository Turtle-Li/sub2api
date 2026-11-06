<template>
  <ChannelStatusV1View v-if="isV1" />
  <ChannelStatusV3View v-else-if="isV3" />
  <ChannelStatusV2View v-else />
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { getChannelMonitorMode, isChannelMonitorV1Mode } from '@/utils/featureFlags'
import ChannelStatusV1View from './ChannelStatusV1View.vue'
import ChannelStatusV2View from './ChannelStatusV2View.vue'

import ChannelStatusV3View from './ChannelStatusV3View.vue'
const isV3 = computed(() => getChannelMonitorMode() === 'v3')
const isV1 = computed(() => isChannelMonitorV1Mode())
</script>
