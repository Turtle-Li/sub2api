<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <button type="button" class="btn btn-primary" :disabled="loading" @click="openAccountPicker">
          <Icon name="plus" size="sm" />
          {{ t('admin.modelTrace.addAccount') }}
        </button>
        <div class="flex flex-wrap items-center gap-3">
          <!-- Master Switch -->
          <div class="flex items-center gap-2 rounded-xl border border-gray-200 bg-white px-3 py-1.5 shadow-sm dark:border-dark-700 dark:bg-dark-800">
            <span class="text-xs font-medium text-gray-700 dark:text-gray-300">
              {{ t('admin.modelTrace.globalSwitch') }}:
            </span>
            <Toggle
              :model-value="globalConfig.enabled"
              :disabled="loading || !globalConfigLoaded || updatingGlobalConfig"
              :aria-label="t('admin.modelTrace.globalSwitch')"
              @update:model-value="toggleGlobalSwitch"
            />
            <span
              class="text-[11px] font-semibold"
              :class="globalConfig.enabled ? 'text-emerald-600 dark:text-emerald-400' : 'text-gray-400'"
            >
              {{ globalConfig.enabled ? t('admin.modelTrace.globalSwitchEnabled') : t('admin.modelTrace.globalSwitchDisabled') }}
            </span>
          </div>

          <!-- Global Settings Button -->
          <button
            type="button"
            class="btn btn-secondary"
            :disabled="loading || !globalConfigLoaded"
            :title="t('admin.modelTrace.globalSettings')"
            @click="openGlobalConfigDialog"
          >
            <Icon name="cog" size="sm" />
            <span>{{ t('admin.modelTrace.globalSettings') }}</span>
          </button>

          <!-- Refresh Button -->
          <button
            type="button"
            class="btn btn-secondary"
            :disabled="loading"
            :title="t('admin.modelTrace.refresh')"
            @click="loadAll"
          >
            <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />
            <span>{{ t('admin.modelTrace.refresh') }}</span>
          </button>
        </div>
      </div>
      <p v-if="globalConfigLoaded && !globalConfig.enabled" class="text-sm text-gray-500 dark:text-gray-400">
        {{ t('admin.modelTrace.monitoringDisabledHint') }}
      </p>

      <!-- Stat Cards -->
      <div class="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <div class="card flex items-center gap-4 p-4 dark:bg-dark-800">
          <div class="flex h-12 w-12 items-center justify-center rounded-xl bg-blue-50 text-blue-600 dark:bg-blue-950/40 dark:text-blue-400">
            <Icon name="server" size="md" />
          </div>
          <div>
            <div class="text-xs font-medium text-gray-500 dark:text-gray-400">
              {{ t('admin.modelTrace.totalAccounts') }}
            </div>
            <div class="mt-0.5 text-xl font-bold text-gray-900 dark:text-white">
              {{ monitors.length }}
            </div>
          </div>
        </div>

        <div class="card flex items-center gap-4 p-4 dark:bg-dark-800">
          <div class="flex h-12 w-12 items-center justify-center rounded-xl bg-emerald-50 text-emerald-600 dark:bg-emerald-950/40 dark:text-emerald-400">
            <Icon name="sparkles" size="md" />
          </div>
          <div>
            <div class="text-xs font-medium text-gray-500 dark:text-gray-400">
              {{ t('admin.modelTrace.totalModels') }}
            </div>
            <div class="mt-0.5 text-xl font-bold text-gray-900 dark:text-white">
              {{ totalConfiguredModels }}
            </div>
          </div>
        </div>

        <div class="card flex items-center gap-4 p-4 dark:bg-dark-800">
          <div class="flex h-12 w-12 items-center justify-center rounded-xl bg-purple-50 text-purple-600 dark:bg-purple-950/40 dark:text-purple-400">
            <Icon name="clock" size="md" />
          </div>
          <div>
            <div class="text-xs font-medium text-gray-500 dark:text-gray-400">
              {{ t('admin.modelTrace.lastCycleRun') }}
            </div>
            <div class="mt-0.5 text-sm font-semibold text-gray-900 dark:text-white">
              {{ lastCycleRunTimeFormatted }}
            </div>
          </div>
        </div>
      </div>

      <!-- Loading State -->
      <div v-if="loading && monitors.length === 0" class="flex min-h-48 items-center justify-center text-sm text-gray-500">
        <Icon name="refresh" size="md" class="mr-2 animate-spin text-primary-500" />
        {{ t('common.loading') }}
      </div>

      <!-- Empty State -->
      <div
        v-else-if="monitors.length === 0"
        class="card flex min-h-56 flex-col items-center justify-center border-dashed border-gray-300 p-8 text-center dark:border-dark-600 dark:bg-dark-800"
      >
        <Icon name="cube" size="xl" class="text-gray-400" />
        <p class="mt-3 font-medium text-gray-800 dark:text-gray-200">
          {{ t('admin.modelTrace.noAccounts') }}
        </p>
        <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.addAccountHint') }}</p>
      </div>

      <!-- Accounts List -->
      <div v-else class="space-y-5">
        <div
          v-for="account in monitors"
          :key="account.account_id"
          class="card overflow-hidden border border-gray-200 dark:border-dark-700 dark:bg-dark-800"
        >
          <!-- Account Card Header -->
          <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 bg-gray-50/60 p-4 dark:border-dark-700 dark:bg-dark-900">
            <div class="flex flex-wrap items-center gap-3">
              <span class="text-base font-semibold text-gray-900 dark:text-white">
                {{ account.account_name }}
              </span>
              <PlatformTypeBadge :platform="account.platform" :type="account.type" />
              <RouterLink v-if="account.schedulable === false" to="/admin/accounts" class="text-xs font-medium text-rose-700 underline dark:text-rose-300">
                {{ t('admin.modelTrace.callsPaused') }}
              </RouterLink>

              <!-- Last run time -->
              <span v-if="account.last_run_at" class="text-xs text-gray-400 dark:text-gray-500">
                {{ t('admin.modelTrace.recentCycle', { time: formatRelativeTime(account.last_run_at) }) }}
              </span>
            </div>

            <!-- Action buttons -->
            <div class="flex flex-wrap items-center gap-2">
              <label class="inline-flex items-center gap-2 text-xs text-gray-600 dark:text-gray-300">
                <Toggle
                  :model-value="account.enabled !== false"
                  :disabled="togglingAccounts[account.account_id]"
                  :aria-label="t('admin.modelTrace.accountMonitoring', { name: account.account_name })"
                  @update:model-value="toggleAccountMonitoring(account, $event)"
                />
                {{ t(account.enabled === false ? 'admin.modelTrace.monitoringPaused' : 'admin.modelTrace.monitoringActive') }}
              </label>
              <button
                type="button"
                class="btn btn-secondary btn-sm"
                @click="openConfigDialog(account)"
              >
                <Icon name="cog" size="sm" class="mr-1" />
                {{ t('admin.modelTrace.configureModels') }}
              </button>
              <button type="button" class="btn btn-secondary btn-sm" @click="removingAccount = account">
                {{ t('admin.modelTrace.removeAccount') }}
              </button>
            </div>
          </div>

          <!-- Account Card Body: Models Table -->
          <div class="p-0">
            <div v-if="Object.keys(account.models).length === 0" class="p-6 text-center text-sm text-gray-500 dark:text-gray-400">
              <p>{{ t('admin.modelTrace.noModelsConfigured') }}</p>
              <button
                type="button"
                class="btn btn-secondary btn-sm mt-3"
                @click="openConfigDialog(account)"
              >
                <Icon name="plus" size="sm" class="mr-1" />
                {{ t('admin.modelTrace.configureModels') }}
              </button>
            </div>

            <div v-else class="overflow-x-auto">
              <table class="min-w-full divide-y divide-gray-200 dark:divide-dark-700">
                <thead class="bg-gray-50 dark:bg-dark-900">
                  <tr>
                    <th scope="col" class="px-4 py-2.5 text-left text-xs font-semibold text-gray-600 dark:text-gray-300">
                      {{ t('admin.modelTrace.configuredModel') }}
                    </th>
                    <th scope="col" class="px-4 py-2.5 text-left text-xs font-semibold text-gray-600 dark:text-gray-300">
                      {{ t('admin.modelTrace.detectedModel') }}
                    </th>
                    <th scope="col" class="px-4 py-2.5 text-left text-xs font-semibold text-gray-600 dark:text-gray-300">
                      {{ t('admin.modelTrace.probability') }}
                    </th>
                    <th scope="col" class="px-4 py-2.5 text-left text-xs font-semibold text-gray-600 dark:text-gray-300">
                      {{ t('admin.modelTrace.latency') }}
                    </th>
                    <th scope="col" class="px-4 py-2.5 text-left text-xs font-semibold text-gray-600 dark:text-gray-300">
                      {{ t('admin.modelTrace.testedAt') }}
                    </th>
                    <th scope="col" class="px-4 py-2.5 text-right text-xs font-semibold text-gray-600 dark:text-gray-300">
                      {{ t('admin.modelTrace.actions') }}
                    </th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-gray-100 bg-white dark:divide-dark-700 dark:bg-dark-800">
                  <tr
                    v-for="(cfg, modelId) in account.models"
                    :key="modelId"
                    class="transition-colors hover:bg-gray-50/50 dark:hover:bg-dark-700/50"
                  >
                    <!-- Configured Model -->
                    <td class="whitespace-nowrap px-4 py-3 font-mono text-sm font-semibold text-gray-900 dark:text-gray-100">
                      {{ modelId }}
                    </td>

                    <!-- Detected Model -->
                    <td class="whitespace-nowrap px-4 py-3 text-sm">
                      <span v-if="cfg.task?.status === 'failed' && showTaskFailure(cfg)" class="text-xs text-rose-600" :title="cfg.task.error">
                        {{ t('admin.modelTrace.probeFailed', { error: cfg.task.error || '' }) }}
                      </span>
                      <template v-else-if="cfg.latest">
                        <span
                          v-if="cfg.latest.error"
                          class="inline-flex items-center rounded-md bg-rose-50 px-2 py-0.5 font-mono text-xs font-medium text-rose-700 ring-1 ring-inset ring-rose-600/20 dark:bg-rose-900/30 dark:text-rose-300"
                          :title="cfg.latest.error"
                        >
                          {{ t('admin.modelTrace.probeErrorPrefix', { error: cfg.latest.error }) }}
                        </span>
                        <span
                          v-else
                          class="inline-flex items-center rounded-md bg-blue-50 px-2.5 py-0.5 font-mono text-xs font-semibold text-blue-700 ring-1 ring-inset ring-blue-700/10 dark:bg-blue-900/30 dark:text-blue-300"
                        >
                          {{ cfg.latest.detected_model }}
                        </span>
                        <ModelTraceResultStatus :record="cfg.latest" class="mt-1" />
                      </template>
                      <span v-else class="text-xs text-gray-400 dark:text-gray-500">
                        {{ t('admin.modelTrace.untested') }}
                      </span>
                    </td>

                    <!-- Probability -->
                    <td class="whitespace-nowrap px-4 py-3 text-sm">
                      <template v-if="cfg.latest && !cfg.latest.error">
                        <div class="flex items-center gap-2">
                          <span
                            class="font-mono text-xs font-semibold"
                            :class="probTextClass(cfg.latest.probability)"
                          >
                            {{ (cfg.latest.probability * 100).toFixed(1) }}%
                          </span>
                          <div class="h-1.5 w-16 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700">
                            <div
                              class="h-full rounded-full transition-all duration-300"
                              :class="probBarClass(cfg.latest.probability)"
                              :style="{ width: `${Math.min(100, Math.max(0, cfg.latest.probability * 100))}%` }"
                            ></div>
                          </div>
                        </div>
                      </template>
                      <span v-else class="text-xs text-gray-400">-</span>
                    </td>

                    <!-- Latency -->
                    <td class="whitespace-nowrap px-4 py-3 font-mono text-xs text-gray-600 dark:text-gray-300">
                      {{ cfg.latest && !cfg.latest.error ? `${cfg.latest.latency_ms} ms` : '-' }}
                    </td>

                    <!-- Tested At -->
                    <td class="whitespace-nowrap px-4 py-3 text-xs text-gray-500 dark:text-gray-400">
                      <span v-if="cfg.latest" :title="formatDateTime(cfg.latest.tested_at)">
                        {{ formatRelativeTime(cfg.latest.tested_at) }}
                      </span>
                      <span v-else>-</span>
                    </td>

                    <!-- Actions -->
                    <td class="whitespace-nowrap px-4 py-3 text-right text-xs">
                      <div class="inline-flex items-center gap-1.5">
                        <button
                          type="button"
                          class="btn btn-secondary btn-sm"
                          :disabled="account.enabled === false || togglingAccounts[account.account_id] || isProbing(account.account_id, String(modelId))"
                          @click="handleRunProbe(account, String(modelId))"
                        >
                          <Icon
                            name="refresh"
                            size="xs"
                            class="mr-1"
                            :class="{ 'animate-spin': isProbing(account.account_id, String(modelId)) }"
                          />
                          <span>{{ cfg.task?.status === 'queued' ? t('admin.modelTrace.queued') : isProbing(account.account_id, String(modelId)) ? t('admin.modelTrace.probing') : t('admin.modelTrace.runProbe') }}</span>
                        </button>

                        <button
                          type="button"
                          class="btn btn-secondary btn-sm"
                          :disabled="!cfg.latest && !cfg.history?.length"
                          @click="openHistoryDialog(account, String(modelId), cfg.latest ? [cfg.latest, ...(cfg.history || [])] : (cfg.history || []))"
                        >
                          <Icon name="clock" size="xs" class="mr-1" />
                          <span>{{ t('admin.modelTrace.history') }}</span>
                          <span v-if="cfg.latest || cfg.history?.length" class="ml-0.5 text-[10px] text-gray-400">
                            ({{ (cfg.history?.length || 0) + (cfg.latest ? 1 : 0) }})
                          </span>
                        </button>
                      </div>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </div>
      </div>
    </div>

    <BaseDialog :show="showAccountPicker" :title="t('admin.modelTrace.addAccount')" @close="closeAccountPicker">
      <form class="flex gap-2" @submit.prevent="loadAccountCandidates(1)">
        <input v-model.trim="accountSearch" type="search" class="input flex-1" :placeholder="t('admin.modelTrace.searchAccounts')" />
        <button type="submit" class="btn btn-secondary" :disabled="loadingCandidates">{{ t('common.search') }}</button>
      </form>
      <div v-if="loadingCandidates" class="py-6 text-center text-sm text-gray-500">{{ t('common.loading') }}</div>
      <div v-else-if="candidateError" class="py-6 text-center text-sm text-rose-600">{{ candidateError }}</div>
      <div v-else class="mt-4 space-y-2">
        <p v-if="accountCandidates.length === 0" class="py-6 text-center text-sm text-gray-500">{{ t('admin.modelTrace.noMatchingAccounts') }}</p>
        <button
          v-for="account in accountCandidates"
          :key="account.id"
          type="button"
          class="flex w-full items-center justify-between gap-3 rounded-lg border border-gray-200 p-3 text-left hover:border-primary-500 dark:border-dark-700"
          :disabled="monitoredAccountIds.has(account.id)"
          @click="selectMonitoringAccount(account)"
        >
          <div class="min-w-0">
            <span class="block truncate font-medium text-gray-900 dark:text-white">{{ account.name }}</span>
            <span class="text-xs text-gray-500">#{{ account.id }}</span>
          </div>
          <span v-if="monitoredAccountIds.has(account.id)" class="text-xs text-gray-400">{{ t('admin.modelTrace.alreadyAdded') }}</span>
          <PlatformTypeBadge v-else :platform="account.platform" :type="account.type" />
        </button>
        <Pagination v-if="candidateTotal > candidatePageSize" :page="candidatePage" :page-size="candidatePageSize" :total="candidateTotal" :show-page-size-selector="false" @update:page="loadAccountCandidates" />
      </div>
      <template #footer>
        <button type="button" class="btn btn-secondary" @click="closeAccountPicker">{{ t('common.cancel') }}</button>
      </template>
    </BaseDialog>

    <ConfirmDialog
      :show="removingAccount !== null"
      :title="t('admin.modelTrace.removeAccount')"
      :message="t('admin.modelTrace.removeAccountConfirm', { name: removingAccount?.account_name || '' })"
      danger
      @cancel="removingAccount = null"
      @confirm="removeMonitor"
    />

    <!-- Configure Models Dialog (Per-Account) -->
    <BaseDialog
      :show="showConfigDialog"
      :title="t('admin.modelTrace.modalTitle', { name: editingAccount?.account_name || '' })"
      @close="closeConfigDialog"
    >
      <form @submit.prevent="saveConfig" class="space-y-5">
        <!-- Models List -->
        <div>
          <div class="flex items-center justify-between">
            <label class="block text-sm font-medium text-gray-900 dark:text-white">
              {{ t('admin.modelTrace.monitoredModels') }}
            </label>
            <button
              type="button"
              class="text-xs text-primary-600 hover:underline dark:text-primary-400"
              @click="showBatchInput = !showBatchInput"
            >
              {{ showBatchInput ? t('admin.modelTrace.collapseBatchInput') : t('admin.modelTrace.pasteBatchInput') }}
            </button>
          </div>

          <!-- Batch Textarea -->
          <div v-if="showBatchInput" class="mt-2 space-y-2">
            <textarea
              v-model="batchInputText"
              rows="3"
              class="input font-mono text-xs"
              :placeholder="t('admin.modelTrace.batchAddPlaceholder')"
            ></textarea>
            <button
              type="button"
              class="btn btn-secondary btn-sm"
              @click="handleBatchAddModels"
            >
              {{ t('admin.modelTrace.batchAdd') }}
            </button>
          </div>

          <!-- Single Add Input -->
          <div class="mt-2 flex gap-2">
            <input
              v-model.trim="newModelInput"
              type="text"
              class="input flex-1 font-mono text-sm"
              :placeholder="t('admin.modelTrace.addModelPlaceholder')"
              @keydown.enter.prevent="handleAddModel"
            />
            <button
              type="button"
              class="btn btn-secondary"
              @click="handleAddModel"
            >
              {{ t('admin.modelTrace.add') }}
            </button>
          </div>

          <!-- Model Tags -->
          <div class="mt-3 flex flex-wrap gap-2">
            <span
              v-for="(m, idx) in configForm.models"
              :key="m"
              class="inline-flex items-center gap-1.5 rounded-lg bg-gray-100 px-2.5 py-1 font-mono text-xs text-gray-800 dark:bg-dark-700 dark:text-gray-200"
            >
              <span>{{ m }}</span>
              <button
                type="button"
                class="rounded-full text-gray-400 hover:bg-gray-200 hover:text-gray-600 dark:hover:bg-dark-600 dark:hover:text-gray-200"
                @click="configForm.models.splice(idx, 1)"
              >
                <Icon name="x" size="xs" />
              </button>
            </span>
            <span v-if="configForm.models.length === 0" class="text-xs text-gray-400">
              {{ t('admin.modelTrace.noModelsYet') }}
            </span>
          </div>
        </div>
      </form>

      <template #footer>
        <div class="flex justify-end gap-2">
          <button
            type="button"
            class="btn btn-secondary"
            :disabled="saving"
            @click="closeConfigDialog"
          >
            {{ t('common.cancel') }}
          </button>
          <button
            type="button"
            class="btn btn-primary"
            :disabled="saving"
            @click="saveConfig"
          >
            {{ saving ? t('common.submitting') : t('admin.modelTrace.saveConfig') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- Global Settings Dialog -->
    <BaseDialog
      :show="showGlobalConfigDialog"
      :title="t('admin.modelTrace.globalSettingsTitle')"
      @close="showGlobalConfigDialog = false"
    >
      <form @submit.prevent="saveGlobalConfig" class="space-y-4">
        <!-- Master Switch in Settings -->
        <div class="flex items-center justify-between border-b border-gray-100 pb-3 dark:border-dark-700">
          <div>
            <label class="text-sm font-medium text-gray-900 dark:text-white">
              {{ t('admin.modelTrace.globalSwitch') }}
            </label>
            <p class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.modelTrace.globalSwitchHelp') }}
            </p>
          </div>
          <Toggle v-model="globalForm.enabled" :aria-label="t('admin.modelTrace.globalSwitch')" />
        </div>

        <fieldset class="space-y-4 border-b border-gray-100 pb-4 dark:border-dark-700">
          <legend class="mb-3 text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.modelTrace.mismatchPolicy') }}</legend>
          <div class="flex items-center justify-between gap-4">
            <div>
              <label for="modeltrace-notify" class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.modelTrace.notifyOnMismatch') }}</label>
              <p class="mt-1 text-xs text-gray-600 dark:text-gray-300">{{ t('admin.modelTrace.notifyHelp') }}</p>
            </div>
            <Toggle id="modeltrace-notify" v-model="globalForm.notify_on_mismatch" :aria-label="t('admin.modelTrace.notifyOnMismatch')" />
          </div>
          <div class="flex items-center justify-between gap-4">
            <div>
              <label for="modeltrace-pause" class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.modelTrace.pauseOnMismatch') }}</label>
              <p class="mt-1 text-xs text-gray-600 dark:text-gray-300">{{ t('admin.modelTrace.pauseHelp') }}</p>
            </div>
            <Toggle id="modeltrace-pause" v-model="globalForm.pause_on_mismatch" :aria-label="t('admin.modelTrace.pauseOnMismatch')" />
          </div>
          <div v-if="globalForm.notify_on_mismatch">
            <label for="modeltrace-cooldown" class="block text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.modelTrace.notificationCooldown') }}</label>
            <input id="modeltrace-cooldown" v-model.number="globalForm.notification_cooldown_minutes" type="number" min="1" max="1440" class="input mt-1" />
          </div>
          <div>
            <label for="modeltrace-allowed" class="block text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.modelTrace.allowedDetections') }}</label>
            <textarea id="modeltrace-allowed" v-model="allowedDetectionsInput" rows="3" class="input mt-1 font-mono text-xs" placeholder="gpt-6.1-sol = gpt-6-astra" aria-describedby="modeltrace-allowed-help"></textarea>
            <p id="modeltrace-allowed-help" class="mt-1 text-xs text-gray-600 dark:text-gray-300">{{ t('admin.modelTrace.allowedDetectionsHelp') }}</p>
          </div>
          <details class="text-xs text-gray-600 dark:text-gray-300">
            <summary class="cursor-pointer font-medium">{{ t('admin.modelTrace.detectionNotes') }}</summary>
            <p class="mt-2 leading-relaxed">{{ t('admin.modelTrace.detectionNotesHelp') }}</p>
          </details>
        </fieldset>

        <!-- Probe Count -->
        <div>
          <label class="block text-sm font-medium text-gray-900 dark:text-white">
            {{ t('admin.modelTrace.probeCount') }}
          </label>
          <input
            v-model.number="globalForm.probe_count"
            type="number"
            min="1"
            max="10"
            class="input mt-1 font-mono"
          />
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.modelTrace.probeCountHelp') }}
          </p>
        </div>

        <!-- Interval Seconds -->
        <div>
          <label class="block text-sm font-medium text-gray-900 dark:text-white">
            {{ t('admin.modelTrace.intervalSeconds') }}
          </label>
          <input
            v-model.number="globalForm.interval_seconds"
            type="number"
            min="0"
            max="60"
            class="input mt-1 font-mono"
          />
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.modelTrace.intervalSecondsHelp') }}
          </p>
        </div>

        <!-- Reasoning Effort -->
        <div>
          <label class="block text-sm font-medium text-gray-900 dark:text-white">
            {{ t('admin.modelTrace.reasoningEffort') }}
          </label>
          <select v-model="globalForm.reasoning_effort" class="input mt-1">
            <option value="low">{{ t('admin.modelTrace.lowRecommendation') }}</option>
            <option value="medium">medium</option>
            <option value="high">high</option>
          </select>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.modelTrace.reasoningEffortHelp') }}
          </p>
        </div>

        <!-- Default Cron Expression -->
        <div>
          <label class="block text-sm font-medium text-gray-900 dark:text-white">
            {{ t('admin.modelTrace.cronExpression') }}
          </label>
          <input
            v-model.trim="globalForm.cron_expression"
            type="text"
            class="input mt-1 font-mono"
            placeholder="*/30 * * * *"
          />
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.modelTrace.cronHelp') }}
          </p>
        </div>
      </form>

      <template #footer>
        <div class="flex justify-end gap-2">
          <button
            type="button"
            class="btn btn-secondary"
            :disabled="savingGlobalConfig"
            @click="showGlobalConfigDialog = false"
          >
            {{ t('common.cancel') }}
          </button>
          <button
            type="button"
            class="btn btn-primary"
            :disabled="savingGlobalConfig"
            @click="saveGlobalConfig"
          >
            {{ savingGlobalConfig ? t('common.submitting') : t('common.save') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- History Dialog -->
    <BaseDialog
      :show="showHistoryDialog"
      :title="t('admin.modelTrace.historyTitle', { name: historyAccount?.account_name || '', model: historyModelId })"
      width="extra-wide"
      @close="showHistoryDialog = false"
    >
      <div v-if="historyRecords.length === 0" class="py-8 text-center text-sm text-gray-500">
        {{ t('admin.modelTrace.noHistory') }}
      </div>

      <div v-else class="overflow-x-auto">
        <table class="min-w-full divide-y divide-gray-200 dark:divide-dark-700">
          <thead class="bg-gray-50 dark:bg-dark-900">
            <tr>
              <th scope="col" class="px-3 py-2 text-left text-xs font-semibold text-gray-600 dark:text-gray-300">
                {{ t('admin.modelTrace.testedAt') }}
              </th>
              <th scope="col" class="px-3 py-2 text-left text-xs font-semibold text-gray-600 dark:text-gray-300">
                {{ t('admin.modelTrace.detectedModel') }}
              </th>
              <th scope="col" class="px-3 py-2 text-left text-xs font-semibold text-gray-600 dark:text-gray-300">
                {{ t('admin.modelTrace.probability') }}
              </th>
              <th scope="col" class="px-3 py-2 text-left text-xs font-semibold text-gray-600 dark:text-gray-300">
                {{ t('admin.modelTrace.latency') }}
              </th>
              <th scope="col" class="px-3 py-2 text-left text-xs font-semibold text-gray-600 dark:text-gray-300">
                {{ t('admin.modelTrace.status') }}
              </th>
              <th scope="col" class="px-3 py-2 text-right text-xs font-semibold text-gray-600 dark:text-gray-300">
                {{ t('admin.modelTrace.requestRecords') }}
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 bg-white dark:divide-dark-700 dark:bg-dark-800">
            <template v-for="(rec, idx) in historyRecords" :key="idx">
            <tr class="text-xs">
              <td class="whitespace-nowrap px-3 py-2 text-gray-500 dark:text-gray-400">
                {{ formatDateTime(rec.tested_at) }}
              </td>
              <td class="whitespace-nowrap px-3 py-2 font-mono font-medium text-gray-900 dark:text-gray-100">
                {{ rec.detected_model || '-' }}
              </td>
              <td class="whitespace-nowrap px-3 py-2">
                <span v-if="!rec.error" :class="probTextClass(rec.probability)" class="font-mono font-semibold">
                  {{ (rec.probability * 100).toFixed(1) }}%
                </span>
                <span v-else class="text-gray-400">-</span>
              </td>
              <td class="whitespace-nowrap px-3 py-2 font-mono text-gray-600 dark:text-gray-300">
                {{ rec.latency_ms ? `${rec.latency_ms} ms` : '-' }}
              </td>
              <td class="min-w-44 px-3 py-2">
                <span
                  v-if="rec.error"
                  class="inline-flex max-w-xs items-center break-words rounded bg-rose-50 px-2 py-0.5 text-[11px] font-medium text-rose-700 ring-1 ring-inset ring-rose-600/20 dark:bg-rose-900/30 dark:text-rose-300"
                  :title="rec.error"
                >
                  {{ rec.error }}
                </span>
                <ModelTraceResultStatus v-else-if="rec.match_status" :record="rec" />
                <span
                  v-else
                  class="inline-flex items-center rounded bg-emerald-50 px-2 py-0.5 text-[11px] font-medium text-emerald-700 ring-1 ring-inset ring-emerald-600/20 dark:bg-emerald-900/30 dark:text-emerald-300"
                >
                  {{ t('admin.modelTrace.success') }}
                </span>
              </td>
              <td class="whitespace-nowrap px-3 py-2 text-right">
                <button
                  v-if="rec.requests?.length"
                  type="button"
                  class="inline-flex items-center gap-2 rounded-lg px-2 py-1.5 text-primary-600 hover:bg-primary-50 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 dark:text-primary-400 dark:hover:bg-dark-700"
                  :aria-expanded="expandedHistoryRecords.has(idx)"
                  :aria-controls="`modeltrace-requests-${idx}`"
                  @click="toggleHistoryRequests(idx)"
                >
                  <span>{{ t('admin.modelTrace.requestCount', { count: rec.requests.length }) }}</span>
                  <span v-if="rec.requests.some(request => !!request.error)" class="text-rose-600 dark:text-rose-300">
                    {{ t('admin.modelTrace.requestErrors', { count: rec.requests.filter(request => !!request.error).length }) }}
                  </span>
                  <Icon :name="expandedHistoryRecords.has(idx) ? 'chevronDown' : 'chevronRight'" size="xs" />
                </button>
                <span v-else class="text-gray-400 dark:text-gray-500">-</span>
              </td>
            </tr>
            <tr v-if="rec.requests?.length && expandedHistoryRecords.has(idx)" :id="`modeltrace-requests-${idx}`">
              <td colspan="6" class="bg-gray-50/70 p-3 dark:bg-dark-900/50 sm:p-4">
                <div class="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
                  <div v-for="(request, requestIndex) in rec.requests" :key="request.id" class="space-y-3 border-b border-gray-100 p-4 last:border-b-0 dark:border-dark-700">
                    <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
                      <span class="font-medium text-gray-700 dark:text-gray-200">{{ t('admin.modelTrace.requestLabel', { index: requestIndex + 1 }) }}</span>
                      <span class="text-gray-500 dark:text-gray-400">{{ formatDateTime(request.started_at) }}</span>
                      <span class="break-all font-mono text-gray-700 dark:text-gray-200">{{ request.endpoint || '-' }}</span>
                      <span class="sm:ml-auto" :class="request.error ? 'text-rose-600 dark:text-rose-300' : 'text-emerald-600 dark:text-emerald-300'">
                        {{ request.error ? t('admin.modelTrace.requestError') : request.valid_sample ? t('admin.modelTrace.validSample') : t('admin.modelTrace.invalidSample') }}
                      </span>
                    </div>
                    <dl class="grid grid-cols-2 gap-3 text-xs sm:grid-cols-4">
                      <div>
                        <dt class="text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.duration') }}</dt>
                        <dd class="mt-1 font-mono text-gray-900 dark:text-gray-100">{{ request.latency_ms }} ms</dd>
                      </div>
                      <div>
                        <dt class="text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.firstToken') }}</dt>
                        <dd class="mt-1 font-mono text-gray-900 dark:text-gray-100">{{ request.first_token_ms == null ? '-' : `${request.first_token_ms} ms` }}</dd>
                      </div>
                      <div>
                        <dt class="text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.tokenUsage') }}</dt>
                        <dd class="mt-1 text-gray-900 dark:text-gray-100">{{ request.usage_reported ? `${request.input_tokens} / ${request.output_tokens}` : t('admin.modelTrace.upstreamNoUsage') }}</dd>
                      </div>
                      <div class="col-span-2 sm:col-span-1">
                        <dt class="text-gray-500 dark:text-gray-400">{{ t('admin.modelTrace.requestId') }}</dt>
                        <dd class="mt-1 break-all font-mono text-gray-900 dark:text-gray-100">{{ request.id }}</dd>
                      </div>
                    </dl>
                    <p v-if="request.error" class="whitespace-pre-wrap break-words rounded-md bg-rose-50 p-3 text-xs text-rose-700 dark:bg-rose-900/20 dark:text-rose-300">{{ request.error }}</p>
                  </div>
                </div>
              </td>
            </tr>
            </template>
          </tbody>
        </table>
      </div>

      <template #footer>
        <div class="flex justify-end">
          <button type="button" class="btn btn-secondary" @click="showHistoryDialog = false">
            {{ t('common.close') }}
          </button>
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import {
  modelTraceAPI,
  type ModelTraceMonitorItem,
  type ModelTraceModelConfig,
  type ModelTraceRecord,
  type ModelTraceGlobalConfig
} from '@/api/admin/modeltrace'
import { list as listAccounts } from '@/api/admin/accounts'
import type { AccountListItem } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime, formatRelativeTime } from '@/utils/format'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import Toggle from '@/components/common/Toggle.vue'
import PlatformTypeBadge from '@/components/common/PlatformTypeBadge.vue'
import Icon from '@/components/icons/Icon.vue'
import ModelTraceResultStatus from '@/components/account/ModelTraceResultStatus.vue'
import { RouterLink } from 'vue-router'

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const monitors = ref<ModelTraceMonitorItem[]>([])
const probingMap = reactive<Record<string, boolean>>({})
const togglingAccounts = reactive<Record<number, boolean>>({})

async function toggleAccountMonitoring(account: ModelTraceMonitorItem, enabled: boolean) {
  const id = account.account_id
  if (togglingAccounts[id]) return
  togglingAccounts[id] = true
  try {
    await modelTraceAPI.updateAccountConfig(id, { enabled })
    const current = monitors.value.find(item => item.account_id === id)
    if (current) current.enabled = enabled
    appStore.showSuccess(t(enabled ? 'admin.modelTrace.monitoringResumed' : 'admin.modelTrace.monitoringPauseSaved'))
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    togglingAccounts[id] = false
  }
}
const globalConfigLoaded = ref(false)
const showAccountPicker = ref(false)
const accountSearch = ref('')
const accountCandidates = ref<AccountListItem[]>([])
const candidatePage = ref(1)
const candidatePageSize = 20
const candidateTotal = ref(0)
const loadingCandidates = ref(false)
const candidateError = ref('')
let candidateRequest = 0
const monitoredAccountIds = computed(() => new Set(monitors.value.map((account) => account.account_id)))
const removingAccount = ref<ModelTraceMonitorItem | null>(null)
const removing = ref(false)

function openAccountPicker() {
  accountSearch.value = ''
  accountCandidates.value = []
  candidateTotal.value = 0
  showAccountPicker.value = true
  void loadAccountCandidates(1)
}

function closeAccountPicker() {
  showAccountPicker.value = false
  candidateRequest++
}

async function loadAccountCandidates(page: number) {
  const request = ++candidateRequest
  loadingCandidates.value = true
  candidateError.value = ''
  try {
    const data = await listAccounts(page, candidatePageSize, { search: accountSearch.value, platform: 'openai', lite: '1', include_scheduler_score: '0' })
    if (request !== candidateRequest || !showAccountPicker.value) return
    accountCandidates.value = data.items
    candidatePage.value = data.page
    candidateTotal.value = data.total
  } catch (err: unknown) {
    if (request === candidateRequest) candidateError.value = extractApiErrorMessage(err, t('common.error'))
  } finally {
    if (request === candidateRequest) loadingCandidates.value = false
  }
}

function selectMonitoringAccount(account: AccountListItem) {
  if (monitoredAccountIds.value.has(account.id)) return
  closeAccountPicker()
  openConfigDialog({ account_id: account.id, account_name: account.name, platform: account.platform, type: account.type, models: {} })
}

async function removeMonitor() {
  if (!removingAccount.value || removing.value) return
  const accountId = removingAccount.value.account_id
  removing.value = true
  try {
    await modelTraceAPI.removeAccountMonitor(accountId)
    monitors.value = monitors.value.filter((account) => account.account_id !== accountId)
    if (removingAccount.value?.account_id === accountId) removingAccount.value = null
    appStore.showSuccess(t('admin.modelTrace.accountRemoved'))
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    removing.value = false
  }
}

// Global Config State
const globalConfig = ref<ModelTraceGlobalConfig>({
  enabled: false,
  probe_count: 3,
  interval_seconds: 1,
  reasoning_effort: 'low',
  cron_expression: '*/30 * * * *',
  notify_on_mismatch: false,
  pause_on_mismatch: false,
  notification_cooldown_minutes: 60,
  allowed_detections: { 'gpt-6.1-sol': ['gpt-6-astra'] }
})
const updatingGlobalConfig = ref(false)
const showGlobalConfigDialog = ref(false)
const savingGlobalConfig = ref(false)
const globalForm = reactive<ModelTraceGlobalConfig>({
  enabled: false,
  probe_count: 3,
  interval_seconds: 1,
  reasoning_effort: 'low',
  cron_expression: '*/30 * * * *',
  notify_on_mismatch: false,
  pause_on_mismatch: false,
  notification_cooldown_minutes: 60,
  allowed_detections: { 'gpt-6.1-sol': ['gpt-6-astra'] }
})
const allowedDetectionsInput = ref('')

// Stats
const totalConfiguredModels = computed(() => {
  return monitors.value.reduce((acc, m) => acc + Object.keys(m.models).length, 0)
})

const lastCycleRunTimeFormatted = computed(() => {
  let latestTime: string | null = null
  for (const m of monitors.value) {
    if (m.last_run_at) {
      if (!latestTime || new Date(m.last_run_at) > new Date(latestTime)) {
        latestTime = m.last_run_at
      }
    }
  }
  return latestTime ? formatRelativeTime(latestTime) : t('admin.modelTrace.untested')
})

function probTextClass(prob: number): string {
  if (prob >= 0.8) return 'text-emerald-600 dark:text-emerald-400'
  if (prob >= 0.5) return 'text-amber-600 dark:text-amber-400'
  return 'text-gray-500 dark:text-gray-400'
}

function probBarClass(prob: number): string {
  if (prob >= 0.8) return 'bg-emerald-500'
  if (prob >= 0.5) return 'bg-amber-500'
  return 'bg-gray-400'
}

function isProbing(accountId: number, modelId: string): boolean {
  const task = monitors.value.find((account) => account.account_id === accountId)?.models[modelId]?.task
  return Boolean(probingMap[`${accountId}:${modelId}`]) || task?.status === 'queued' || task?.status === 'running'
}

function showTaskFailure(cfg: ModelTraceModelConfig): boolean {
  if (cfg.task?.status !== 'failed') return false
  if (!cfg.latest?.tested_at || !cfg.task.updated_at) return true
  return new Date(cfg.task.updated_at).getTime() >= new Date(cfg.latest.tested_at).getTime()
}

async function loadAll() {
  loading.value = true
  try {
    const [monitorsData, cfgData] = await Promise.all([
      modelTraceAPI.listMonitors(),
      modelTraceAPI.getGlobalConfig()
    ])
    monitors.value = monitorsData
    globalConfig.value = { ...globalConfig.value, ...cfgData }
    globalConfigLoaded.value = true
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    loading.value = false
  }
}

// Master Toggle
async function toggleGlobalSwitch(val: boolean) {
  if (!globalConfigLoaded.value || loading.value) return
  updatingGlobalConfig.value = true
  const oldVal = globalConfig.value.enabled
  globalConfig.value.enabled = val

  try {
    await modelTraceAPI.updateGlobalConfig({
      ...globalConfig.value,
      enabled: val
    })
    appStore.showSuccess(val ? t('admin.modelTrace.globalSwitchEnabled') : t('admin.modelTrace.globalSwitchDisabled'))
  } catch (err: unknown) {
    globalConfig.value.enabled = oldVal
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    updatingGlobalConfig.value = false
  }
}

// Global Config Dialog
function openGlobalConfigDialog() {
  Object.assign(globalForm, globalConfig.value)
  allowedDetectionsInput.value = Object.entries(globalConfig.value.allowed_detections)
    .map(([model, candidates]) => `${model} = ${candidates.join(', ')}`).join('\n')
  showGlobalConfigDialog.value = true
}

async function saveGlobalConfig() {
  const allowed = new Map<string, string[]>()
  for (const line of allowedDetectionsInput.value.split('\n').filter(line => line.trim())) {
    const parts = line.split('=')
    const model = parts[0]?.trim().toLowerCase()
    const candidates = parts[1]?.split(',').map(candidate => candidate.trim().toLowerCase())
    if (parts.length !== 2 || !model || !candidates?.length || candidates.some(candidate => !candidate) || allowed.has(model)) {
      appStore.showError(t('admin.modelTrace.allowedDetectionsInvalid'))
      return
    }
    allowed.set(model, candidates)
  }
  savingGlobalConfig.value = true
  try {
    const res = await modelTraceAPI.updateGlobalConfig({ ...globalForm, allowed_detections: Object.fromEntries(allowed) })
    if (res.config) {
      globalConfig.value = res.config
    }
    appStore.showSuccess(t('admin.modelTrace.configSaved'))
    showGlobalConfigDialog.value = false
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('admin.modelTrace.configSaveFailed')))
  } finally {
    savingGlobalConfig.value = false
  }
}

// Single Model Probe
async function handleRunProbe(account: ModelTraceMonitorItem, modelId: string) {
  const key = `${account.account_id}:${modelId}`
  if (account.enabled === false || togglingAccounts[account.account_id] || probingMap[key]) return
  probingMap[key] = true

  try {
    const task = await modelTraceAPI.probeModel(account.account_id, modelId)
    if (account.models[modelId]) account.models[modelId].task = task
    appStore.showSuccess(t('admin.modelTrace.taskSubmitted'))
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    probingMap[key] = false
  }
}

// Config Dialog (Account)
const showConfigDialog = ref(false)
const editingAccount = ref<ModelTraceMonitorItem | null>(null)
const configForm = reactive({
  models: [] as string[]
})
const newModelInput = ref('')
const showBatchInput = ref(false)
const batchInputText = ref('')
const saving = ref(false)

function openConfigDialog(account: ModelTraceMonitorItem) {
  editingAccount.value = account
  configForm.models = Object.keys(account.models)
  newModelInput.value = ''
  showBatchInput.value = false
  batchInputText.value = ''
  showConfigDialog.value = true
}

function closeConfigDialog() {
  showConfigDialog.value = false
  editingAccount.value = null
}

function handleAddModel() {
  const m = newModelInput.value.trim()
  if (!m) return
  if (!configForm.models.includes(m)) {
    configForm.models.push(m)
  }
  newModelInput.value = ''
}

function handleBatchAddModels() {
  if (!batchInputText.value.trim()) return
  const parts = batchInputText.value
    .split(/[\n,]+/)
    .map((s) => s.trim())
    .filter(Boolean)

  for (const part of parts) {
    if (!configForm.models.includes(part)) {
      configForm.models.push(part)
    }
  }
  batchInputText.value = ''
  showBatchInput.value = false
}

async function saveConfig() {
  if (!editingAccount.value) return
  saving.value = true

  try {
    await modelTraceAPI.updateAccountConfig(editingAccount.value.account_id, {
      models: configForm.models
    })
    appStore.showSuccess(t('admin.modelTrace.configSaved'))
    showConfigDialog.value = false
    await loadAll()
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('admin.modelTrace.configSaveFailed')))
  } finally {
    saving.value = false
  }
}

// History Dialog
const showHistoryDialog = ref(false)
const historyAccount = ref<ModelTraceMonitorItem | null>(null)
const historyModelId = ref('')
const historyRecords = ref<ModelTraceRecord[]>([])
const expandedHistoryRecords = reactive(new Set<number>())

function toggleHistoryRequests(index: number) {
  if (expandedHistoryRecords.has(index)) expandedHistoryRecords.delete(index)
  else expandedHistoryRecords.add(index)
}

function openHistoryDialog(
  account: ModelTraceMonitorItem,
  modelId: string,
  history: ModelTraceRecord[]
) {
  historyAccount.value = account
  historyModelId.value = modelId
  historyRecords.value = history
  expandedHistoryRecords.clear()
  showHistoryDialog.value = true
}

let refreshTimer: ReturnType<typeof setTimeout> | undefined
let disposed = false

async function pollMonitors() {
  try {
    const data = await modelTraceAPI.listMonitors()
    if (!disposed && !loading.value) monitors.value = data
  } catch {
    // Keep the last snapshot during temporary network failures; retry next tick.
  } finally {
    if (!disposed) refreshTimer = setTimeout(pollMonitors, 5000)
  }
}

onMounted(async () => {
  await loadAll()
  if (!disposed) refreshTimer = setTimeout(pollMonitors, 5000)
})
onUnmounted(() => {
  disposed = true
  if (refreshTimer) clearTimeout(refreshTimer)
})
</script>
