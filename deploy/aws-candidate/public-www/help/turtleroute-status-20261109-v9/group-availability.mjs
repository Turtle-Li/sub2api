const labels = { operational: '运行正常', degraded: '部分异常', down: '检测异常', unknown: '暂无近期数据', insufficient: '样本不足', observed: '有成功请求', errors_observed: '有失败记录', collecting: '本时段采集中' };
const platforms = { openai: 'GPT', anthropic: 'Claude', deepseek: 'DeepSeek', kimi: 'Kimi', zhipu: 'GLM', minimax: 'MiniMax', gemini: 'Gemini', antigravity: 'Antigravity' };
const validPercent = value => typeof value === 'number' && Number.isFinite(value) && value >= 0 && value <= 100;
const validTime = value => typeof value === 'number' && Number.isFinite(value) && value >= 0;
const percent = value => validPercent(value) ? `${value.toFixed(1)}%` : '—';
const seconds = value => `${(value / 1000).toFixed(1)}s`;

export function cacheCapability(status, now = Date.now()) {
  if (status?.probe_cache_verification_version !== 1 || status.probe_cache_verification_window_hours !== 24 || !status.probe_enabled) return null;
  const verified = Date.parse(status.probe_cache_verified_at ?? '');
  return Number.isFinite(verified) && verified <= now && now - verified <= 86400000 ? 'verified' : 'pending';
}
export function quotaNotice(status, now = Date.now()) {
  const observed = Date.parse(status?.pool_quota_observed_at ?? '');
  if (!status || !['limited', 'exhausted'].includes(status.pool_quota_state) || !Number.isFinite(observed) || observed > now + 60000 || now - observed > 90000) return null;
  const reset = Date.parse(status.pool_quota_resets_at ?? '');
  const remaining = Number.isFinite(reset) && reset > observed ? Math.max(0, Math.ceil((reset - now) / 1000)) : null;
  const clock = remaining === null ? null : [Math.floor(remaining / 3600), Math.floor(remaining / 60) % 60, remaining % 60].map(n => String(n).padStart(2, '0')).join(':');
  return { state: status.pool_quota_state, remaining, clock };
}
const quotaText = quota => quota.remaining === null ? '恢复时间待上游确认' : quota.remaining === 0 ? '额度窗口已到，等待检测确认' : '预计部分额度恢复倒计时 ' + quota.clock;
function updateQuotaNotice(notice) {
  const quota = quotaNotice({ pool_quota_state: notice.dataset.state, pool_quota_observed_at: notice.dataset.observed, pool_quota_resets_at: notice.dataset.reset });
  notice.hidden = !quota;
  if (quota) notice.querySelector('[data-quota-countdown]').textContent = quotaText(quota);
  else {
    const badge = notice.parentElement.querySelector('.status-state');
    badge.dataset.state = notice.dataset.originalState;
    badge.textContent = labels[notice.dataset.originalState] ?? labels.unknown;
  }
}

export function statusMetricDetails(status) {
  const ready = status?.request_sample_state === 'ready';
  const probeReady = status?.probe_metrics_version === 1 && status.probe_enabled && status.probe_sample_state === 'ready';
  const start = Date.parse(status?.cache_hour_start ?? ''), end = Date.parse(status?.cache_hour_end ?? '');
  const cacheReady = status?.cache_sample_state === 'ready' && status.cache_scope === 'shared_account_pool' && status.cache_statistic === 'best_complete_hour' && status.cache_lookback_hours === 24 && Number.isFinite(start) && start % 3600000 === 0 && end - start === 3600000 && validPercent(status.cache_peak_hour_rate);
  const capability = cacheCapability(status);
  const realSuccess = ready && validPercent(status.request_success_rate);
  const realTime = ready && validTime(status.ttft_p50_ms);
  const probeSuccess = !realSuccess && probeReady && validPercent(status.probe_success_rate);
  const probeTTFT = !realTime && probeReady && validTime(status.probe_ttft_p50_ms);
  const probeLatency = !realTime && !probeTTFT && probeReady && validTime(status.probe_latency_p50_ms);
  return [
    { label: cacheReady ? '最佳小时缓存' : '缓存能力', scope: cacheReady ? '同账号池 · 24h' : '本分组 · 检测验证', value: cacheReady ? percent(status.cache_peak_hour_rate) : capability === 'verified' ? '缓存已验证' : capability === 'pending' ? '待验证' : null, probe: false, cacheHour: cacheReady },
    { label: probeSuccess ? '检测成功率' : '请求成功率', scope: probeSuccess ? '本分组 · 定时检测' : '本分组 · 全模型', value: realSuccess ? percent(status.request_success_rate) : probeSuccess ? percent(status.probe_success_rate) : '—', probe: probeSuccess },
    { label: probeTTFT ? '检测首 Token P50' : probeLatency ? '检测耗时 P50' : '首 Token P50', scope: probeTTFT || probeLatency ? '本分组 · 定时检测' : '本分组 · 全模型', value: realTime ? seconds(status.ttft_p50_ms) : probeTTFT ? seconds(status.probe_ttft_p50_ms) : probeLatency ? seconds(status.probe_latency_p50_ms) : '—', probe: probeTTFT || probeLatency },
  ];
}
export function statusMetrics(status) { return statusMetricDetails(status).map(metric => metric.value); }
const el = (tag, className, text) => { const node = document.createElement(tag); node.className = className; if (text != null) node.textContent = text; return node; };
const time = date => new Date(date).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });
export function renderStatusGroup(group) {
  const status = group.availability;
  const card = el('article', 'status-group');
  const heading = el('header', 'status-group-heading');
  const title = el('div', ''); title.append(el('span', 'status-platform-label', platforms[group.platform] ?? group.platform), el('h3', '', group.name));
  const badge = el('span', 'status-state', labels[status?.status] ?? labels.unknown); badge.dataset.state = status?.status ?? 'unknown'; heading.append(title, badge);
  const quota = quotaNotice(status);
  let notice;
  if (quota) {
    notice = el('div', 'status-quota');
    Object.assign(notice.dataset, { state: quota.state, observed: status.pool_quota_observed_at, reset: status.pool_quota_resets_at ?? '', originalState: status.status });
    const recovery = el('p', '', quotaText(quota)); recovery.dataset.quotaCountdown = '';
    notice.append(el('p', 'status-quota-title', quota.state === 'exhausted' ? '当前账号池额度不足' : '部分账号额度不足'), recovery);
    if (quota.state === 'exhausted') { badge.textContent = '额度不足'; badge.dataset.state = 'quota'; }
  }
  const details = statusMetricDetails(status), metrics = el('dl', 'status-metrics');
  metrics.style.setProperty('--status-metric-count', details.filter(metric => metric.value !== null).length);
  details.forEach((metric, index) => {
    if (metric.value === null) return;
    const item = el('div', ''), title = el('dt', '', metric.label);
    title.append(el('span', 'status-metric-scope', metric.scope));
    const value = el('dd', index === 0 && !metric.cacheHour ? 'status-cache-capability' : '', metric.value);
    item.append(title, value);
    if (index === 0 && metric.cacheHour) {
      value.title = `统计小时：${new Date(status.cache_hour_start).toLocaleString('zh-CN')} – ${new Date(status.cache_hour_end).toLocaleString('zh-CN')}`;
      const date = new Date(status.cache_hour_start).toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit' });
      item.append(el('span', 'status-cache-hour', `${date} ${time(status.cache_hour_start)}–${time(status.cache_hour_end)}`));
    }
    if (index === 0 && !metric.cacheHour) item.append(el('span', 'status-cache-hour', '近 24h · 缓存能力验证'));
    metrics.append(item);
  });
  const cells = Array.isArray(status?.cells) && status.cells.length > 0 && status.cells.length <= 144 ? status.cells : Array.from({ length: 72 }, () => ({ status: 'unknown', source: 'none' }));
  const slotHeading = el('div', 'status-slot-heading', `近 ${cells.length * 10 / 60} 小时 · 每格 10 分钟`);
  const detail = el('p', 'status-detail', !status ? '状态暂不可用' : details.some(metric => metric.probe) ? '真实请求样本不足的指标由定时检测补充，两类结果分别统计。' : status.request_sample_state === 'insufficient' ? `本分组样本不足，至少 ${status.min_requests ?? 20} 条有效请求后展示成功率与延迟` : status.request_sample_state === 'stale' ? '请求统计暂不可用' : status.request_sample_state === 'none' ? '本分组近期暂无真实请求，成功率与延迟待采集。' : '成功率与延迟统计本分组真实请求；无数据不计失败');
  const slots = el('div', 'status-slots'); slots.style.setProperty('--status-cell-count', cells.length); slots.setAttribute('aria-label', `近 ${cells.length} 个十分钟时段`);
  for (const [index, cell] of cells.entries()) {
    const label = `${cell.start ? time(cell.start) + ' · ' : ''}${labels[cell.status] ?? labels.unknown} · ${cell.source === 'requests' ? '全组请求' : cell.source === 'probe' ? '分组检测' : '无结果'}`;
    const button = el('button', ''); button.type = 'button'; button.dataset.state = cell.status; button.setAttribute('aria-label', label); button.title = label;
    button.tabIndex = index === cells.length - 1 ? 0 : -1;
    button.addEventListener('keydown', event => {
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? cells.length - 1 : event.key === 'ArrowLeft' ? Math.max(0, index - 1) : event.key === 'ArrowRight' ? Math.min(cells.length - 1, index + 1) : null;
      if (next === null) return; event.preventDefault(); slots.children[next]?.focus();
    });
    const explain = () => { for (const node of slots.children) node.tabIndex = node === button ? 0 : -1; detail.textContent = label; }; button.addEventListener('focus', explain); button.addEventListener('click', explain); button.addEventListener('mouseenter', explain); slots.append(button);
  }
  const footer = el('footer', 'status-check', status?.probe_enabled ? '分组检测 · 每 10 分钟一次' : '未启用定时检测');
  card.append(heading); if (notice) card.append(notice); card.append(el('p', 'status-window', '近 180 分钟 · 分组表现'), metrics, slotHeading, slots, detail, footer); return card;
}

if (typeof document !== 'undefined') {
  const root = document.querySelector('[data-status-groups]');
  if (root) {
    let groups = [], pending = false, stopped = false;
    const query = document.querySelector('[data-status-search]'), refresh = document.querySelector('[data-status-refresh]'), message = document.querySelector('[data-status-message]');
    const controller = new AbortController();
    const render = () => {
      const term = query.value.trim().toLocaleLowerCase(); root.replaceChildren();
      for (const group of groups.filter(group => !term || group.name.toLocaleLowerCase().includes(term))) root.append(renderStatusGroup(group));
      if (!root.children.length) root.append(el('p', 'status-detail', term ? '没有匹配的公开分组' : '暂无已公开状态'));
    };
    const load = async () => {
      if (pending || stopped || document.hidden) return;
      pending = true; refresh.disabled = true;
      try {
        const response = await fetch('/api/v1/model-status', { cache: 'no-store', signal: controller.signal });
        if (!response.ok) throw new Error('status unavailable');
        const body = await response.json();
        if (body.code !== 0 || !Array.isArray(body.data?.groups)) throw new Error('status unavailable');
        if (stopped) return;
        groups = body.data.groups; message.textContent = '每 60 秒自动刷新'; render();
      } catch {
        if (stopped) return;
        groups = groups.map(group => ({ ...group, availability: null })); message.textContent = '暂时无法更新，旧状态已清除。请刷新重试。'; render();
      } finally { pending = false; refresh.disabled = false; }
    };
    query.addEventListener('input', render); refresh.addEventListener('click', load);
    const visible = () => { if (!document.hidden) load(); }; document.addEventListener('visibilitychange', visible);
    const clock = setInterval(() => { if (!document.hidden) root.querySelectorAll('.status-quota').forEach(updateQuotaNotice); }, 1000);
    const timer = setInterval(load, 60000); window.addEventListener('pagehide', event => { if (event.persisted) return; stopped = true; clearInterval(timer); clearInterval(clock); controller.abort(); document.removeEventListener('visibilitychange', visible); }); load();
  }
}
