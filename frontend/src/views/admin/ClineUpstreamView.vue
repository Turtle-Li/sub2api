<template>
  <AppLayout>
    <div class="space-y-6">
      <!-- Header -->
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 class="text-xl font-semibold text-gray-900 dark:text-white">
            {{ t('admin.clineUpstream.title') }}
          </h1>
          <p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.clineUpstream.description') }}
          </p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <button
            type="button"
            class="btn btn-secondary"
            :disabled="loadingState || !selectedAccountId"
            data-testid="cline-upstream-refresh"
            @click="loadState"
          >
            <Icon name="refresh" size="sm" :class="{ 'animate-spin': loadingState }" />
            <span>{{ t('admin.clineUpstream.refresh') }}</span>
          </button>
          <button
            type="button"
            class="btn btn-primary"
            :disabled="probing || !state?.supported"
            data-testid="cline-upstream-probe-all"
            @click="probeAll"
          >
            <Icon name="beaker" size="sm" />
            <span>{{ probing ? t('admin.clineUpstream.probing') : t('admin.clineUpstream.probeAll') }}</span>
          </button>
          <button
            type="button"
            class="btn btn-secondary"
            :disabled="probing || !state?.supported || !state.models.length"
            data-testid="cline-upstream-one-click"
            @click="openOneClick"
          >
            <Icon name="bolt" size="sm" />
            <span>{{ t('admin.clineUpstream.oneClick.button') }}</span>
          </button>
          <div class="flex items-center gap-1.5">
            <input
              v-model="manualModel"
              class="input w-64"
              :placeholder="t('admin.clineUpstream.manualProbePlaceholder')"
              data-testid="cline-upstream-manual-model"
              @keyup.enter="probeManual"
            />
            <button
              type="button"
              class="btn btn-secondary"
              :disabled="probing || !state?.supported || !manualModel.trim()"
              :title="t('admin.clineUpstream.manualProbeHint')"
              data-testid="cline-upstream-manual-probe"
              @click="probeManual"
            >
              <Icon name="search" size="sm" />
            </button>
          </div>
        </div>
      </div>

      <div class="rounded-xl border border-blue-100 bg-blue-50/60 p-3 text-xs leading-relaxed text-blue-800 dark:border-blue-900/40 dark:bg-blue-950/30 dark:text-blue-200">
        <p>{{ t('admin.clineUpstream.pipelineHint') }}</p>
        <p class="mt-1.5 flex flex-wrap items-center gap-x-1.5 gap-y-1 font-medium">
          <span>{{ t('admin.clineUpstream.recommendationLabel') }}</span>
          <span class="rounded bg-white/70 px-1.5 py-0.5 dark:bg-blue-900/40">{{ t('admin.clineUpstream.recommendationStep1') }}</span>
          <Icon name="arrowRight" size="xs" class="opacity-60" />
          <span class="rounded bg-white/70 px-1.5 py-0.5 dark:bg-blue-900/40">{{ t('admin.clineUpstream.recommendationStep2') }}</span>
          <Icon name="arrowRight" size="xs" class="opacity-60" />
          <span class="rounded bg-white/70 px-1.5 py-0.5 dark:bg-blue-900/40">{{ t('admin.clineUpstream.recommendationStep3') }}</span>
        </p>
      </div>
      <p class="text-xs text-gray-500 dark:text-gray-400">
        {{ t('admin.clineUpstream.probeCostHint') }}
      </p>

      <!-- Account picker -->
      <div class="card flex flex-wrap items-center gap-3 p-4 dark:bg-dark-800">
        <label class="text-sm font-medium text-gray-700 dark:text-gray-300" for="cline-upstream-account">
          {{ t('admin.clineUpstream.accountLabel') }}
        </label>
        <select
          id="cline-upstream-account"
          v-model="selectedAccountId"
          class="input min-w-56"
          data-testid="cline-upstream-account"
          :disabled="loadingAccounts || accounts.length === 0"
        >
          <option :value="null" disabled>{{ t('admin.clineUpstream.selectAccount') }}</option>
          <option v-for="account in accounts" :key="account.id" :value="account.id">
            {{ account.name }}
          </option>
        </select>
        <span v-if="state" class="text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.clineUpstream.configuredCount', { count: configuredCount }) }}
          · {{ t('admin.clineUpstream.lastFetched', { time: fetchedAtLabel }) }}
        </span>
      </div>

      <!-- States -->
      <div v-if="loadingAccounts" class="flex min-h-32 items-center justify-center text-sm text-gray-500">
        <Icon name="refresh" size="md" class="mr-2 animate-spin text-primary-500" />
        {{ t('admin.clineUpstream.loadingAccounts') }}
      </div>

      <div
        v-else-if="accounts.length === 0"
        class="card flex min-h-40 flex-col items-center justify-center border-dashed border-gray-300 p-8 text-center dark:border-dark-600 dark:bg-dark-800"
        data-testid="cline-upstream-no-accounts"
      >
        <Icon name="server" size="xl" class="text-gray-400" />
        <p class="mt-3 font-medium text-gray-800 dark:text-gray-200">
          {{ t('admin.clineUpstream.noAccounts') }}
        </p>
        <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">
          {{ t('admin.clineUpstream.noAccountsHint') }}
        </p>
      </div>

      <template v-else>
        <div
          v-if="loadError"
          role="alert"
          class="rounded-xl border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900/40 dark:bg-red-950/30 dark:text-red-300"
          data-testid="cline-upstream-error"
        >
          {{ loadError }}
        </div>

        <div
          v-if="state && !state.supported"
          class="rounded-xl border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-900/40 dark:bg-amber-950/30 dark:text-amber-200"
          data-testid="cline-upstream-unsupported"
        >
          <p class="font-medium">{{ t('admin.clineUpstream.unsupported') }}</p>
          <p class="mt-1 text-xs">{{ t('admin.clineUpstream.unsupportedHint') }}</p>
        </div>

        <div
          v-if="state?.models_error"
          class="rounded-xl border border-amber-200 bg-amber-50 p-3 text-xs text-amber-800 dark:border-amber-900/40 dark:bg-amber-950/30 dark:text-amber-200"
        >
          {{ t('admin.clineUpstream.modelsError', { error: state.models_error }) }}
        </div>

        <div
          v-if="state && state.models.length === 0"
          class="card flex min-h-40 flex-col items-center justify-center border-dashed border-gray-300 p-8 text-center dark:border-dark-600 dark:bg-dark-800"
          data-testid="cline-upstream-empty"
        >
          <Icon name="cpu" size="xl" class="text-gray-400" />
          <p class="mt-3 font-medium text-gray-800 dark:text-gray-200">
            {{ t('admin.clineUpstream.emptyModels') }}
          </p>
          <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.clineUpstream.emptyModelsHint') }}
          </p>
        </div>

        <div v-else-if="state" class="card overflow-hidden dark:bg-dark-800">
          <div class="overflow-x-auto">
            <table class="w-full min-w-[1080px] divide-y divide-gray-200 dark:divide-dark-700" data-testid="cline-upstream-table">
              <thead class="bg-gray-50 dark:bg-dark-900/40">
                <tr>
                  <th class="px-4 py-3 text-left text-[11px] font-semibold uppercase tracking-wide text-gray-500">
                    {{ t('admin.clineUpstream.columns.model') }}
                  </th>
                  <th class="w-28 px-4 py-3 text-left text-[11px] font-semibold uppercase tracking-wide text-gray-500">
                    {{ t('admin.clineUpstream.columns.pipeline') }}
                  </th>
                  <th class="px-4 py-3 text-left text-[11px] font-semibold uppercase tracking-wide text-gray-500">
                    {{ t('admin.clineUpstream.columns.upstreams') }}
                  </th>
                  <th class="w-32 px-4 py-3 text-left text-[11px] font-semibold uppercase tracking-wide text-gray-500">
                    {{ t('admin.clineUpstream.columns.lastProvider') }}
                  </th>
                  <th class="w-40 px-4 py-3 text-left text-[11px] font-semibold uppercase tracking-wide text-gray-500">
                    {{ t('admin.clineUpstream.columns.mode') }}
                  </th>
                  <th class="w-56 px-4 py-3 text-right text-[11px] font-semibold uppercase tracking-wide text-gray-500">
                    {{ t('admin.clineUpstream.columns.actions') }}
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
                <template v-for="item in state.models" :key="item.model">
                <tr
                  class="align-middle transition-colors hover:bg-gray-50/70 dark:hover:bg-dark-700/30"
                  :data-testid="`cline-upstream-row-${item.model}`"
                >
                  <!-- 模型：模型名 + 真实 slug + 来源 -->
                  <td class="px-4 py-3.5 align-middle">
                    <div class="flex items-center gap-1.5">
                      <button
                        type="button"
                        class="shrink-0 rounded p-0.5 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-dark-700 dark:hover:text-gray-200"
                        :title="t('admin.clineUpstream.detail.toggle')"
                        :data-testid="`cline-upstream-detail-toggle-${item.model}`"
                        @click="toggleDetail(item.model)"
                      >
                        <Icon :name="expandedModels.has(item.model) ? 'chevronDown' : 'chevronRight'" size="xs" />
                      </button>
                      <span class="font-mono text-xs font-medium text-gray-900 dark:text-white">{{ item.model }}</span>
                    </div>
                    <div class="mt-1 flex min-w-0 items-center gap-2">
                      <span
                        class="badge badge-gray whitespace-nowrap"
                        :title="sourceHint(item.source)"
                      >
                        {{ sourceLabel(item.source) }}
                      </span>
                      <span
                        v-if="item.meta.canonical_slug && item.meta.canonical_slug !== item.model"
                        class="min-w-0 truncate font-mono text-[11px] text-gray-400"
                        :title="item.meta.canonical_slug"
                      >
                        {{ item.meta.canonical_slug }}
                      </span>
                    </div>
                  </td>

                  <!-- 管道 -->
                  <td class="px-4 py-3.5 align-middle">
                    <div class="flex flex-col items-start gap-1">
                      <span
                        class="badge whitespace-nowrap"
                        :class="pipelineClass(item.meta.pipeline)"
                        :title="pipelineTitle(item.meta.pipeline)"
                      >
                        {{ pipelineLabel(item.meta.pipeline) }}
                      </span>
                      <span
                        v-if="item.meta.probed_at && !item.meta.pinnable"
                        class="badge badge-warning whitespace-nowrap"
                        :title="t('admin.clineUpstream.singleUpstreamHint')"
                        :data-testid="`cline-upstream-not-pinnable-${item.model}`"
                      >
                        {{ t('admin.clineUpstream.notPinnable') }}
                      </span>
                    </div>
                  </td>

                  <!-- 渠道清单（校验后按状态着色） -->
                  <td class="px-4 py-3.5 align-middle">
                    <div v-if="item.meta.upstreams.length" class="flex flex-wrap gap-1.5">
                      <span
                        v-for="upstream in item.meta.upstreams"
                        :key="upstream"
                        class="badge whitespace-nowrap"
                        :class="statusClass(item.meta.statuses?.[upstream]?.status)"
                        :title="statusTitle(upstream, item)"
                      >
                        {{ upstream }}
                      </span>
                    </div>
                    <span v-else class="text-xs text-gray-400">{{ t('admin.clineUpstream.noUpstreams') }}</span>
                  </td>

                  <!-- 最近实际渠道 -->
                  <td class="px-4 py-3.5 align-middle">
                    <span
                      v-if="item.meta.last_provider"
                      class="inline-flex items-center gap-1.5 font-mono text-xs text-gray-700 dark:text-gray-200"
                    >
                      <span class="h-1.5 w-1.5 shrink-0 rounded-full bg-emerald-500"></span>
                      <span class="min-w-0 truncate">{{ item.meta.last_provider }}</span>
                    </span>
                    <span v-else class="text-xs text-gray-400">—</span>
                  </td>

                  <!-- 钉住模式 + 候选顺序 -->
                  <td class="px-4 py-3.5 align-middle">
                    <span class="badge whitespace-nowrap" :class="modeClass(item.pin.mode)">
                      {{ modeLabel(item.pin.mode) }}
                    </span>
                    <div
                      v-if="item.candidates.length"
                      class="mt-1 truncate font-mono text-[11px] text-gray-400"
                      :title="item.candidates.join(' > ')"
                    >
                      {{ item.candidates.join(' > ') }}
                    </div>
                  </td>

                  <!-- 操作 -->
                  <td class="px-4 py-3.5 align-middle">
                    <div class="flex items-center justify-end gap-1.5 whitespace-nowrap">
                      <button
                        type="button"
                        class="btn btn-secondary btn-sm"
                        :disabled="!state.supported || busyModel === item.model"
                        :title="t('admin.clineUpstream.actions.probeModel')"
                        @click="probeModel(item.model)"
                      >
                        <Icon name="beaker" size="xs" />
                        <span>{{ t('admin.clineUpstream.actions.probe') }}</span>
                      </button>
                      <button
                        type="button"
                        class="btn btn-secondary btn-sm"
                        :disabled="!state.supported || busyModel === item.model || !item.meta.pinnable"
                        :title="t('admin.clineUpstream.actions.validateModel')"
                        @click="validateModel(item.model)"
                      >
                        <Icon name="check" size="xs" />
                        <span>{{ t('admin.clineUpstream.actions.validate') }}</span>
                      </button>
                      <button
                        type="button"
                        class="btn btn-primary btn-sm"
                        @click="openDialog(item)"
                      >
                        <Icon name="edit" size="xs" />
                        <span>{{ t('admin.clineUpstream.actions.configure') }}</span>
                      </button>
                    </div>
                  </td>
                </tr>

                <!-- 探测详情：解释「为什么渠道清单只有这几个」 -->
                <tr v-if="expandedModels.has(item.model)" :data-testid="`cline-upstream-detail-${item.model}`">
                  <td colspan="6" class="bg-gray-50/70 px-4 py-3 dark:bg-dark-900/30">
                    <div class="grid gap-3 text-xs md:grid-cols-2">
                      <div class="space-y-1.5">
                        <div class="flex flex-wrap gap-x-4 gap-y-1 text-gray-500 dark:text-gray-400">
                          <span>
                            {{ t('admin.clineUpstream.detail.pipeline') }}:
                            <span class="text-gray-800 dark:text-gray-200">{{ pipelineTitle(item.meta.pipeline) }}</span>
                          </span>
                          <span>
                            {{ t('admin.clineUpstream.detail.canonicalSlug') }}:
                            <span class="font-mono text-gray-800 dark:text-gray-200">{{ item.meta.canonical_slug || '—' }}</span>
                          </span>
                          <span>
                            {{ t('admin.clineUpstream.detail.probedAt') }}:
                            <span class="text-gray-800 dark:text-gray-200">{{ formatTs(item.meta.probed_at) }}</span>
                          </span>
                          <span>
                            {{ t('admin.clineUpstream.detail.pinnable') }}:
                            <span class="text-gray-800 dark:text-gray-200">
                              {{ item.meta.pinnable ? t('admin.clineUpstream.detail.pinnableYes') : t('admin.clineUpstream.detail.pinnableNo') }}
                            </span>
                          </span>
                          <span>
                            {{ t('admin.clineUpstream.detail.validatedAt') }}:
                            <span class="text-gray-800 dark:text-gray-200">{{ formatTs(item.meta.validated_at) }}</span>
                          </span>
                        </div>
                        <div
                          v-if="item.meta.harvest_note"
                          class="rounded-lg border border-amber-200 bg-amber-50 p-2 text-[11px] leading-relaxed text-amber-800 dark:border-amber-900/40 dark:bg-amber-950/30 dark:text-amber-200"
                        >
                          <span class="font-medium">{{ t('admin.clineUpstream.detail.harvestNote') }}:</span>
                          <span class="ml-1 break-all font-mono">{{ item.meta.harvest_note }}</span>
                        </div>
                        <div
                          v-else
                          class="rounded-lg border border-gray-200 p-2 text-[11px] text-gray-500 dark:border-dark-700 dark:text-gray-400"
                        >
                          {{ t('admin.clineUpstream.detail.harvestOk') }}
                        </div>
                        <div v-if="item.meta.response_model" class="text-gray-500 dark:text-gray-400">
                          {{ t('admin.clineUpstream.detail.responseModel') }}:
                          <span class="font-mono text-gray-800 dark:text-gray-200">{{ item.meta.response_model }}</span>
                        </div>
                        <div
                          v-if="item.meta.routing_raw"
                          class="rounded-lg border border-gray-200 p-2 text-[11px] leading-relaxed text-gray-600 dark:border-dark-700 dark:text-gray-300"
                        >
                          <div class="mb-0.5 font-medium">{{ t('admin.clineUpstream.detail.routingRaw') }}</div>
                          <div class="break-all font-mono text-[10px]">{{ item.meta.routing_raw }}</div>
                        </div>
                        <div
                          v-if="item.meta.last_error"
                          class="rounded-lg border border-red-200 bg-red-50 p-2 text-[11px] leading-relaxed text-red-700 dark:border-red-900/40 dark:bg-red-950/30 dark:text-red-300"
                        >
                          <span class="font-medium">{{ t('admin.clineUpstream.detail.lastError') }}:</span>
                          <span class="ml-1 break-all font-mono">{{ item.meta.last_error }}</span>
                        </div>
                      </div>

                      <div>
                        <div class="mb-1.5 font-medium text-gray-600 dark:text-gray-300">
                          {{ t('admin.clineUpstream.detail.statuses') }}
                        </div>
                        <div v-if="item.meta.upstreams.length" class="space-y-1">
                          <div
                            v-for="upstream in item.meta.upstreams"
                            :key="upstream"
                            class="flex items-start gap-2"
                          >
                            <span class="badge shrink-0 whitespace-nowrap" :class="statusClass(item.meta.statuses?.[upstream]?.status)">
                              {{ statusLabel(item.meta.statuses?.[upstream]?.status) }}
                            </span>
                            <span class="shrink-0 font-mono text-gray-800 dark:text-gray-200">{{ upstream }}</span>
                            <span class="min-w-0 break-all text-[11px] text-gray-500 dark:text-gray-400">
                              {{ item.meta.statuses?.[upstream]?.note || '' }}
                            </span>
                          </div>
                        </div>
                        <div v-else class="text-[11px] text-gray-400">
                          {{ t('admin.clineUpstream.noUpstreams') }}
                        </div>
                      </div>
                    </div>
                  </td>
                </tr>
                </template>
              </tbody>
            </table>
          </div>
        </div>
      </template>
    </div>

    <!-- One-click vendor pinning preview -->
    <BaseDialog
      :show="oneClickOpen"
      :title="t('admin.clineUpstream.oneClick.title')"
      width="extra-wide"
      mobile-sheet
      @close="oneClickOpen = false"
    >
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.clineUpstream.oneClick.description') }}</p>
      <p class="mt-2 text-sm font-medium text-gray-800 dark:text-gray-200">
        {{ t('admin.clineUpstream.oneClick.summary', { pinned: oneClickPlan.pinnable.length, skipped: oneClickPlan.skipped.length }) }}
      </p>
      <div class="mt-3 max-h-[50vh] overflow-y-auto rounded-xl border border-gray-200 dark:border-dark-700">
        <table class="w-full text-xs">
          <thead class="bg-gray-50 text-[11px] uppercase tracking-wide text-gray-500 dark:bg-dark-900/40">
            <tr>
              <th class="px-3 py-2 text-left">{{ t('admin.clineUpstream.oneClick.columns.model') }}</th>
              <th class="px-3 py-2 text-left">{{ t('admin.clineUpstream.oneClick.columns.channel') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="row in oneClickPlan.pinnable" :key="row.model">
              <td class="px-3 py-2 font-mono">{{ row.model }}</td>
              <td class="px-3 py-2">
                <span class="badge badge-success whitespace-nowrap">{{ row.channel }}</span>
              </td>
            </tr>
            <tr v-for="row in oneClickPlan.skipped" :key="row.model" class="text-gray-500 dark:text-gray-400">
              <td class="px-3 py-2 font-mono">{{ row.model }}</td>
              <td class="px-3 py-2">{{ t(`admin.clineUpstream.oneClick.skip.${row.reason}`) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="!oneClickPlan.pinnable.length" class="mt-2 text-xs text-amber-600 dark:text-amber-400">
        {{ t('admin.clineUpstream.oneClick.nothingToDo') }}
      </p>

      <template #footer>
        <button type="button" class="btn btn-secondary" @click="oneClickOpen = false">
          {{ t('admin.clineUpstream.actions.cancel') }}
        </button>
        <button
          type="button"
          class="btn btn-primary"
          :disabled="saving || !oneClickPlan.pinnable.length"
          data-testid="cline-upstream-one-click-apply"
          @click="applyOneClick"
        >
          <Icon name="check" size="sm" />
          <span>{{ saving ? t('admin.clineUpstream.actions.saving') : t('admin.clineUpstream.oneClick.apply') }}</span>
        </button>
      </template>
    </BaseDialog>

    <!-- Per-model pin dialog -->
    <BaseDialog
      :show="editingModel !== null"
      :title="dialogTitle"
      width="extra-wide"
      mobile-sheet
      @close="closeDialog"
    >
      <div class="space-y-5">
        <!-- Mode -->
        <div>
          <div class="text-sm font-medium text-gray-800 dark:text-gray-200">
            {{ t('admin.clineUpstream.dialog.mode') }}
          </div>
          <div class="mt-2 flex flex-wrap gap-2">
            <button
              v-for="mode in pinModes"
              :key="mode"
              type="button"
              class="btn btn-sm"
              :class="draft.mode === mode ? 'btn-primary' : 'btn-secondary'"
              :data-testid="`cline-upstream-mode-${mode}`"
              @click="draft.mode = mode"
            >
              {{ modeLabel(mode) }}
            </button>
          </div>
          <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ modeHint(draft.mode) }}</p>
        </div>

        <!-- Upstreams -->
        <div>
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div class="text-sm font-medium text-gray-800 dark:text-gray-200">
              {{ t('admin.clineUpstream.dialog.upstreams') }}
            </div>
            <span class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.clineUpstream.dialog.selected', { count: draft.upstreams.length }) }}
            </span>
          </div>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.clineUpstream.dialog.upstreamsHint') }}
          </p>

          <div
            v-if="meta && meta.probed_at && !meta.pinnable"
            class="mt-3 rounded-xl border border-amber-200 bg-amber-50 p-3 text-xs leading-relaxed text-amber-800 dark:border-amber-900/40 dark:bg-amber-950/30 dark:text-amber-200"
            data-testid="cline-upstream-single-upstream-warning"
          >
            {{ t('admin.clineUpstream.singleUpstreamHint') }}
            <span v-if="meta.upstreams.length" class="ml-1 font-mono">{{ meta.upstreams.join(', ') }}</span>
          </div>

          <div v-if="knownUpstreams.length === 0" class="mt-3 rounded-xl border border-dashed border-gray-300 p-4 text-center text-xs text-gray-500 dark:border-dark-600">
            {{ t('admin.clineUpstream.dialog.knownUpstreamsEmpty') }}
          </div>

          <div v-else class="mt-3 space-y-2">
            <div
              v-for="upstream in knownUpstreams"
              :key="upstream"
              class="flex flex-wrap items-center gap-3 rounded-xl border border-gray-200 px-3 py-2 dark:border-dark-700"
              :data-testid="`cline-upstream-option-${upstream}`"
            >
              <label class="flex items-center gap-2 text-sm text-gray-800 dark:text-gray-200">
                <input
                  type="checkbox"
                  class="h-4 w-4"
                  :disabled="meta ? meta.probed_at > 0 && !meta.pinnable : false"
                  :checked="draft.upstreams.includes(upstream)"
                  @change="toggleUpstream(upstream)"
                />
                <span class="font-mono text-xs">{{ upstream }}</span>
              </label>
              <span class="badge" :class="statusClass(metaStatus(upstream))">
                {{ statusLabel(metaStatus(upstream)) }}
              </span>
              <label class="flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
                <input
                  type="checkbox"
                  class="h-4 w-4"
                  :checked="draft.exclude.includes(upstream)"
                  @change="toggleExclude(upstream)"
                />
                <span>{{ t('admin.clineUpstream.dialog.exclude') }}</span>
              </label>
              <div v-if="draft.upstreams.includes(upstream)" class="ml-auto flex items-center gap-1">
                <span class="font-mono text-[11px] text-gray-400">
                  #{{ draft.upstreams.indexOf(upstream) + 1 }}
                </span>
                <button
                  type="button"
                  class="rounded p-1 text-gray-400 hover:text-gray-700 disabled:opacity-30 dark:hover:text-gray-200"
                  :disabled="draft.upstreams.indexOf(upstream) === 0"
                  :title="t('admin.clineUpstream.dialog.upstreamMoveUp')"
                  @click="moveUpstream(upstream, -1)"
                >
                  <Icon name="arrowUp" size="xs" />
                </button>
                <button
                  type="button"
                  class="rounded p-1 text-gray-400 hover:text-gray-700 disabled:opacity-30 dark:hover:text-gray-200"
                  :disabled="draft.upstreams.indexOf(upstream) === draft.upstreams.length - 1"
                  :title="t('admin.clineUpstream.dialog.upstreamMoveDown')"
                  @click="moveUpstream(upstream, 1)"
                >
                  <Icon name="arrowDown" size="xs" />
                </button>
              </div>
            </div>
          </div>

          <p v-if="!meta?.upstreams?.length" class="mt-2 text-xs text-amber-600 dark:text-amber-400">
            {{ t('admin.clineUpstream.dialog.needProbe') }}
          </p>
          <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.clineUpstream.dialog.excludeHint') }}
          </p>
        </div>

        <!-- Sort -->
        <div>
          <div class="text-sm font-medium text-gray-800 dark:text-gray-200">
            {{ t('admin.clineUpstream.dialog.sort') }}
          </div>
          <select v-model="draft.sort" class="input mt-2" data-testid="cline-upstream-sort">
            <option value="">{{ t('admin.clineUpstream.dialog.sortDefault') }}</option>
            <option value="cost">{{ t('admin.clineUpstream.dialog.sortCost') }}</option>
            <option value="ttft">{{ t('admin.clineUpstream.dialog.sortTtft') }}</option>
            <option value="tps">{{ t('admin.clineUpstream.dialog.sortTps') }}</option>
          </select>
        </div>

        <!-- Test -->
        <div class="rounded-xl border border-gray-200 p-3 dark:border-dark-700">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div class="text-sm font-medium text-gray-800 dark:text-gray-200">
              {{ t('admin.clineUpstream.dialog.testResultTitle') }}
            </div>
            <button
              type="button"
              class="btn btn-secondary btn-sm"
              :disabled="testing || !state?.supported"
              data-testid="cline-upstream-test"
              @click="runTest"
            >
              <Icon name="play" size="xs" />
              <span>{{ testing ? t('admin.clineUpstream.dialog.testing') : t('admin.clineUpstream.dialog.testWithDraft') }}</span>
            </button>
          </div>

          <div v-if="testResult" class="mt-3 space-y-1 text-xs" data-testid="cline-upstream-test-result">
            <p
              class="inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 font-medium"
              :class="testResult.ok
                ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300'
                : 'bg-red-50 text-red-700 dark:bg-red-500/10 dark:text-red-300'"
              data-testid="cline-upstream-adherence"
            >
              <Icon :name="testResult.ok ? 'check' : 'x'" size="xs" />
              {{ adherenceLabel(testResult.adherence) }}
            </p>
            <p v-if="!testResult.ok" class="text-red-600 dark:text-red-400">
              {{ testResult.error }}
            </p>
            <template v-if="testResult.ok">
              <p class="text-gray-700 dark:text-gray-300">
                <span class="font-medium">{{ t('admin.clineUpstream.dialog.testActual') }}:</span>
                <span class="ml-1 font-mono">{{ testResult.actual_provider || '—' }}</span>
                <span class="ml-2 text-gray-400">{{ t('admin.clineUpstream.dialog.testMs', { ms: testResult.ms }) }}</span>
              </p>
              <p class="text-gray-700 dark:text-gray-300">
                <span class="font-medium">{{ t('admin.clineUpstream.dialog.testCandidates') }}:</span>
                <span class="ml-1 font-mono">{{ (testResult.candidates || []).join(' > ') || '—' }}</span>
              </p>
              <p v-if="testResult.content" class="text-gray-500 dark:text-gray-400">
                {{ t('admin.clineUpstream.dialog.testContent') }}: {{ testResult.content }}
              </p>
            </template>
          </div>
        </div>
      </div>

      <template #footer>
        <button type="button" class="btn btn-secondary" @click="closeDialog">
          {{ t('admin.clineUpstream.actions.cancel') }}
        </button>
        <button
          type="button"
          class="btn btn-primary"
          :disabled="saving"
          data-testid="cline-upstream-save"
          @click="savePins"
        >
          <Icon name="check" size="sm" />
          <span>{{ saving ? t('admin.clineUpstream.actions.saving') : t('admin.clineUpstream.actions.save') }}</span>
        </button>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { adminAPI } from '@/api/admin'
import {
  getClineUpstreams,
  probeClineUpstreams,
  testClineUpstreams,
  updateClineUpstreams,
  validateClineUpstreams,
  type ClinePinMode,
  type ClineUpstreamModelState,
  type ClineUpstreamPin,
  type ClineUpstreamState,
  type ClineUpstreamTestResult
} from '@/api/admin/clineUpstream'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/format'
import type { AccountListItem } from '@/types'

const { t } = useI18n()
const appStore = useAppStore()
const route = useRoute()

const pinModes: ClinePinMode[] = ['off', 'strict', 'preferred']

const accounts = ref<AccountListItem[]>([])
const selectedAccountId = ref<number | null>(null)
const state = ref<ClineUpstreamState | null>(null)
const loadingAccounts = ref(false)
const loadingState = ref(false)
const loadError = ref('')
const probing = ref(false)
const busyModel = ref('')
const saving = ref(false)

const expandedModels = ref<Set<string>>(new Set())
const manualModel = ref('')
const oneClickOpen = ref(false)
const editingModel = ref<string | null>(null)
const draft = reactive<ClineUpstreamPin>({
  mode: 'off',
  upstreams: [],
  exclude: [],
  sort: ''
})
const testing = ref(false)
const testResult = ref<ClineUpstreamTestResult | null>(null)

const configuredCount = computed(() => asList(state.value?.models).filter((item) => item.configured).length)
const fetchedAtLabel = computed(() => {
  const seconds = state.value?.fetched_at || 0
  return seconds ? formatDateTime(new Date(seconds * 1000)) : '—'
})

const editingEntry = computed<ClineUpstreamModelState | null>(
  () => asList(state.value?.models).find((item) => item.model === editingModel.value) || null
)
const meta = computed(() => editingEntry.value?.meta || null)
const dialogTitle = computed(() =>
  t('admin.clineUpstream.dialog.title', { model: editingModel.value || '' })
)
const knownUpstreams = computed(() => {
  const seen = new Set<string>()
  const out: string[] = []
  const push = (value: string) => {
    const trimmed = (value || '').trim()
    if (!trimmed || seen.has(trimmed)) return
    seen.add(trimmed)
    out.push(trimmed)
  }
  ;(meta.value?.upstreams || []).forEach(push)
  draft.upstreams.forEach(push)
  draft.exclude.forEach(push)
  return out
})

function errorMessage(error: unknown, fallback: string): string {
  const candidate = error as { response?: { data?: { message?: string; detail?: string } }; message?: string }
  return candidate?.response?.data?.message || candidate?.response?.data?.detail || candidate?.message || fallback
}

/**
 * 后端列表字段在 Go 里是 nil slice 时会序列化成 null，模板里按数组使用会抛
 * "Cannot read properties of null (reading 'length')"。这里在入口统一补齐，
 * 保证视图层拿到的永远是数组/对象。
 */
function asList<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : []
}

function normalizeState(raw: ClineUpstreamState | null | undefined): ClineUpstreamState | null {
  if (!raw) return null
  return {
    ...raw,
    models: asList(raw.models).map((item) => ({
      ...item,
      candidates: asList(item.candidates),
      pin: {
        mode: item.pin?.mode || 'off',
        upstreams: asList(item.pin?.upstreams),
        exclude: asList(item.pin?.exclude),
        sort: item.pin?.sort || ''
      },
      meta: {
        ...item.meta,
        upstreams: asList(item.meta?.upstreams),
        statuses: item.meta?.statuses || {},
        harvest_note: item.meta?.harvest_note || '',
        last_error: item.meta?.last_error || '',
        response_model: item.meta?.response_model || '',
        routing_raw: item.meta?.routing_raw || ''
      }
    }))
  }
}

function normalizeTestResult(raw: ClineUpstreamTestResult | null): ClineUpstreamTestResult | null {
  if (!raw) return null
  return {
    ...raw,
    targets: asList(raw.targets),
    exclude: asList(raw.exclude),
    candidates: asList(raw.candidates)
  }
}

function sourceLabel(source: string): string {
  if (source === 'subscription') return t('admin.clineUpstream.source.subscription')
  if (source === 'catalog') return t('admin.clineUpstream.source.catalog')
  return t('admin.clineUpstream.source.configured')
}

function pipelineLabel(pipeline: string): string {
  if (pipeline === 'direct') return t('admin.clineUpstream.pipeline.direct')
  if (pipeline === 'planner') return t('admin.clineUpstream.pipeline.planner')
  return t('admin.clineUpstream.pipeline.unknown')
}

function pipelineClass(pipeline: string): string {
  const base = 'badge'
  if (pipeline === 'direct') return `${base} badge-primary`
  if (pipeline === 'planner') return `${base} badge-purple`
  return `${base} badge-gray`
}

function toggleDetail(model: string) {
  const next = new Set(expandedModels.value)
  if (next.has(model)) next.delete(model)
  else next.add(model)
  expandedModels.value = next
}

function formatTs(seconds: number): string {
  return seconds ? formatDateTime(new Date(seconds * 1000)) : t('admin.clineUpstream.detail.never')
}

function adherenceLabel(adherence?: string): string {
  switch (adherence) {
    case 'adopted':
      return t('admin.clineUpstream.adherence.adopted')
    case 'fallback':
      return t('admin.clineUpstream.adherence.fallback')
    case 'not-adopted':
      return t('admin.clineUpstream.adherence.notAdopted')
    case 'violated':
      return t('admin.clineUpstream.adherence.violated')
    default:
      return t('admin.clineUpstream.adherence.unresolved')
  }
}

function normalizeChannel(value: string): string {
  return (value || '').toLowerCase().replace(/[^a-z0-9]/g, '')
}

// 模型家族 → 厂商直供渠道名（两条管道的拼写都列上）。
const VENDOR_CHANNEL_ALIASES: Record<string, string[]> = {
  deepseek: ['deepseek'],
  qwen: ['alibaba'],
  glm: ['zai', 'z-ai', 'alibaba'],
  kimi: ['moonshot', 'kimi'],
  minimax: ['minimax'],
  mimo: ['xiaomi', 'mimo']
}

/**
 * 推荐钉住的渠道：只在该模型的探测渠道池里找厂商直供，找不到就返回 null。
 * 依次尝试：显式家族别名 → canonical slug 的厂商前缀 → 模型 id 家族名。
 */
function recommendedChannel(item: ClineUpstreamModelState): string | null {
  if (!item.meta?.pinnable || !item.meta.upstreams.length) return null
  const pool = item.meta.upstreams
  const family = (item.model.replace(/^cline-pass\//, '').split(/[-.]/)[0] || '').toLowerCase()
  const vendor = (item.meta.canonical_slug.split('/')[0] || '').toLowerCase()
  const candidates = [...(VENDOR_CHANNEL_ALIASES[family] || []), vendor, family].filter(Boolean)
  for (const candidate of candidates) {
    const hit = pool.find((upstream) => normalizeChannel(upstream) === normalizeChannel(candidate))
    if (hit) return hit
  }
  return null
}

function sourceHint(source: string): string {
  if (source === 'subscription') return t('admin.clineUpstream.sourceHint.subscription')
  if (source === 'catalog') return t('admin.clineUpstream.sourceHint.catalog')
  return t('admin.clineUpstream.sourceHint.configured')
}

function pipelineTitle(pipeline: string): string {
  if (pipeline === 'direct') return t('admin.clineUpstream.pipelineHintDirect')
  if (pipeline === 'planner') return t('admin.clineUpstream.pipelineHintPlanner')
  return t('admin.clineUpstream.pipelineHintUnknown')
}

function statusTitle(upstream: string, item: ClineUpstreamModelState): string {
  const status = item.meta?.statuses?.[upstream]
  if (!status) return upstream
  return [upstream, statusLabel(status.status), status.note].filter(Boolean).join(' · ')
}

function modeLabel(mode: string): string {
  if (mode === 'strict') return t('admin.clineUpstream.mode.strict')
  if (mode === 'preferred') return t('admin.clineUpstream.mode.preferred')
  return t('admin.clineUpstream.mode.off')
}

function modeClass(mode: string): string {
  const base = 'badge'
  if (mode === 'strict') return `${base} badge-success`
  if (mode === 'preferred') return `${base} badge-primary`
  return `${base} badge-gray`
}

function modeHint(mode: string): string {
  if (mode === 'strict') return t('admin.clineUpstream.dialog.modeHintStrict')
  if (mode === 'preferred') return t('admin.clineUpstream.dialog.modeHintPreferred')
  return t('admin.clineUpstream.dialog.modeHintOff')
}

function statusLabel(status?: string): string {
  switch (status) {
    case 'ok':
      return t('admin.clineUpstream.status.ok')
    case 'limited':
      return t('admin.clineUpstream.status.limited')
    case 'bad':
      return t('admin.clineUpstream.status.bad')
    case 'auth':
      return t('admin.clineUpstream.status.auth')
    case 'not-adopted':
      return t('admin.clineUpstream.status.notAdopted')
    default:
      return t('admin.clineUpstream.status.unknown')
  }
}

function statusClass(status?: string): string {
  const base = 'badge'
  switch (status) {
    case 'ok':
      return `${base} badge-success`
    case 'limited':
      return `${base} badge-warning`
    case 'bad':
      return `${base} badge-danger`
    case 'auth':
      return `${base} badge-danger`
    case 'not-adopted':
      return `${base} badge-danger`
    default:
      return `${base} badge-gray`
  }
}

function metaStatus(upstream: string): string | undefined {
  return meta.value?.statuses?.[upstream]?.status
}

async function loadAccounts() {
  loadingAccounts.value = true
  try {
    const response = await adminAPI.accounts.list(1, 200, { platform: 'cline' })
    accounts.value = response.items || []
    // 支持从账号列表的「Cline 上游钉住」入口带 ?account=<id> 直达。
    const requested = Number(route.query.account)
    if (Number.isFinite(requested) && accounts.value.some((item) => item.id === requested)) {
      selectedAccountId.value = requested
    }
    if (accounts.value.length && selectedAccountId.value === null) {
      selectedAccountId.value = accounts.value[0].id
    }
  } catch (error) {
    loadError.value = errorMessage(error, t('admin.clineUpstream.messages.loadFailed'))
  } finally {
    loadingAccounts.value = false
  }
}

async function loadState() {
  if (!selectedAccountId.value) return
  loadingState.value = true
  loadError.value = ''
  try {
    state.value = normalizeState(await getClineUpstreams(selectedAccountId.value))
  } catch (error) {
    state.value = null
    loadError.value = errorMessage(error, t('admin.clineUpstream.messages.loadFailed'))
    appStore.showError(loadError.value)
  } finally {
    loadingState.value = false
  }
}

async function probeAll() {
  if (!selectedAccountId.value) return
  probing.value = true
  try {
    const response = await probeClineUpstreams(selectedAccountId.value)
    appStore.showSuccess(t('admin.clineUpstream.messages.probed', { count: asList(response?.results).length }))
    await loadState()
  } catch (error) {
    appStore.showError(errorMessage(error, t('admin.clineUpstream.messages.probeFailed')))
  } finally {
    probing.value = false
  }
}

async function probeModel(model: string) {
  if (!selectedAccountId.value) return
  busyModel.value = model
  try {
    await probeClineUpstreams(selectedAccountId.value, [model])
    appStore.showSuccess(t('admin.clineUpstream.messages.probed', { count: 1 }))
    await loadState()
  } catch (error) {
    appStore.showError(errorMessage(error, t('admin.clineUpstream.messages.probeFailed')))
  } finally {
    busyModel.value = ''
  }
}

// 手动探测任意模型 ID：用于确认某个 id（例如新发布的 4.1 flash 变体）在 Cline 侧
// 到底有哪些渠道，而不必先出现在推荐清单里。
async function probeManual() {
  const model = manualModel.value.trim()
  if (!selectedAccountId.value || !model) return
  probing.value = true
  try {
    const response = await probeClineUpstreams(selectedAccountId.value, [model])
    appStore.showSuccess(t('admin.clineUpstream.messages.probed', { count: asList(response?.results).length }))
    await loadState()
    expandedModels.value = new Set([...expandedModels.value, model])
  } catch (error) {
    appStore.showError(errorMessage(error, t('admin.clineUpstream.messages.probeFailed')))
  } finally {
    probing.value = false
  }
}

async function validateModel(model: string) {
  if (!selectedAccountId.value) return
  busyModel.value = model
  try {
    const result = await validateClineUpstreams(selectedAccountId.value, model)
    appStore.showSuccess(
      t('admin.clineUpstream.messages.validated', { count: Object.keys(result?.statuses || {}).length })
    )
    await loadState()
  } catch (error) {
    appStore.showError(errorMessage(error, t('admin.clineUpstream.messages.validateFailed')))
  } finally {
    busyModel.value = ''
  }
}

function openDialog(item: ClineUpstreamModelState) {
  editingModel.value = item.model
  draft.mode = item.pin?.mode || 'off'
  draft.upstreams = [...asList(item.pin?.upstreams)]
  draft.exclude = [...asList(item.pin?.exclude)]
  draft.sort = item.pin.sort || ''
  testResult.value = null
}

function closeDialog() {
  editingModel.value = null
  testResult.value = null
}

function toggleUpstream(upstream: string) {
  if (draft.upstreams.includes(upstream)) {
    draft.upstreams = draft.upstreams.filter((value) => value !== upstream)
    return
  }
  draft.upstreams = [...draft.upstreams, upstream]
  draft.exclude = draft.exclude.filter((value) => value !== upstream)
}

function toggleExclude(upstream: string) {
  if (draft.exclude.includes(upstream)) {
    draft.exclude = draft.exclude.filter((value) => value !== upstream)
    return
  }
  draft.exclude = [...draft.exclude, upstream]
  draft.upstreams = draft.upstreams.filter((value) => value !== upstream)
}

function moveUpstream(upstream: string, delta: number) {
  const index = draft.upstreams.indexOf(upstream)
  const next = index + delta
  if (index < 0 || next < 0 || next >= draft.upstreams.length) return
  const list = [...draft.upstreams]
  list.splice(index, 1)
  list.splice(next, 0, upstream)
  draft.upstreams = list
}

async function runTest() {
  if (!selectedAccountId.value || !editingModel.value) return
  testing.value = true
  testResult.value = null
  try {
    testResult.value = normalizeTestResult(
      await testClineUpstreams(selectedAccountId.value, {
        model: editingModel.value,
        mode: draft.mode,
        upstreams: [...draft.upstreams],
        exclude: [...draft.exclude],
        sort: draft.sort
      })
    )
  } catch (error) {
    appStore.showError(errorMessage(error, t('admin.clineUpstream.messages.probeFailed')))
  } finally {
    testing.value = false
  }
}

type OneClickRow = { model: string; channel: string }
type OneClickSkip = { model: string; reason: 'notProbed' | 'notPinnable' | 'noVendorChannel' }

// 一键配置只做一件事：在已探测出渠道池、且池里确实有厂商直供渠道的模型上，
// 严格钉住那个渠道。任何不确定的情况一律跳过，并在弹窗里逐条列明。
const oneClickPlan = computed<{ pinnable: OneClickRow[]; skipped: OneClickSkip[] }>(() => {
  const pinnable: OneClickRow[] = []
  const skipped: OneClickSkip[] = []
  for (const item of asList(state.value?.models)) {
    if (!item.meta.probed_at) {
      skipped.push({ model: item.model, reason: 'notProbed' })
      continue
    }
    if (!item.meta.pinnable) {
      skipped.push({ model: item.model, reason: 'notPinnable' })
      continue
    }
    const channel = recommendedChannel(item)
    if (!channel) {
      skipped.push({ model: item.model, reason: 'noVendorChannel' })
      continue
    }
    pinnable.push({ model: item.model, channel })
  }
  return { pinnable, skipped }
})

function openOneClick() {
  oneClickOpen.value = true
}

async function applyOneClick() {
  if (!selectedAccountId.value || !state.value) return
  saving.value = true
  try {
    const pins: Record<string, ClineUpstreamPin> = {}
    const recommended = new Map(oneClickPlan.value.pinnable.map((row) => [row.model, row.channel]))
    for (const item of asList(state.value.models)) {
      const channel = recommended.get(item.model)
      if (channel) {
        pins[item.model] = { mode: 'strict', upstreams: [channel], exclude: [], sort: item.pin?.sort || '' }
        continue
      }
      // 未被推荐的模型保留原配置，避免一键操作顺手清掉手工设置。
      const current = item.pin
      const isEmpty = current.mode === 'off' && !current.upstreams.length && !current.exclude.length && !current.sort
      if (!isEmpty) pins[item.model] = { ...current }
    }
    state.value = normalizeState(await updateClineUpstreams(selectedAccountId.value, pins))
    appStore.showSuccess(t('admin.clineUpstream.oneClick.applied', { count: oneClickPlan.value.pinnable.length }))
    oneClickOpen.value = false
  } catch (error) {
    appStore.showError(errorMessage(error, t('admin.clineUpstream.messages.saveFailed')))
  } finally {
    saving.value = false
  }
}

async function savePins() {
  if (!selectedAccountId.value || !state.value || !editingModel.value) return
  saving.value = true
  try {
    const pins: Record<string, ClineUpstreamPin> = {}
    for (const item of asList(state.value.models)) {
      const source = item.model === editingModel.value ? { ...draft } : item.pin
      const isEmpty =
        source.mode === 'off' && source.upstreams.length === 0 && source.exclude.length === 0 && !source.sort
      if (isEmpty) continue
      pins[item.model] = {
        mode: source.mode,
        upstreams: [...source.upstreams],
        exclude: [...source.exclude],
        sort: source.sort
      }
    }
    state.value = normalizeState(await updateClineUpstreams(selectedAccountId.value, pins))
    appStore.showSuccess(t('admin.clineUpstream.messages.saved'))
    closeDialog()
  } catch (error) {
    appStore.showError(errorMessage(error, t('admin.clineUpstream.messages.saveFailed')))
  } finally {
    saving.value = false
  }
}

watch(selectedAccountId, () => {
  state.value = null
  void loadState()
})

onMounted(async () => {
  await loadAccounts()
  if (selectedAccountId.value) {
    await loadState()
  }
})
</script>
