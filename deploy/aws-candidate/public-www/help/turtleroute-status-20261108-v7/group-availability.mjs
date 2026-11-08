const labels = { operational: '运行正常', degraded: '部分异常', down: '检测异常', unknown: '暂无近期数据', insufficient: '样本不足', observed: '有成功请求', errors_observed: '有失败记录', collecting: '本时段采集中' };
const platforms = { openai: 'GPT', anthropic: 'Claude', deepseek: 'DeepSeek', kimi: 'Kimi', zhipu: 'GLM', minimax: 'MiniMax', gemini: 'Gemini', antigravity: 'Antigravity' };
const validPercent = value => typeof value === 'number' && Number.isFinite(value) && value >= 0 && value <= 100;
const validTime = value => typeof value === 'number' && Number.isFinite(value) && value >= 0;
const percent = value => validPercent(value) ? `${value.toFixed(1)}%` : '—';
const seconds = value => `${(value / 1000).toFixed(1)}s`;
export function statusMetricDetails(status) {
  const ready = status?.request_sample_state === 'ready';
  const probeReady = status?.probe_metrics_version === 1 && status.probe_enabled && status.probe_sample_state === 'ready';
  const start = Date.parse(status?.cache_hour_start ?? ''), end = Date.parse(status?.cache_hour_end ?? '');
  const cacheReady = status?.cache_sample_state === 'ready' && status.cache_scope === 'shared_account_pool' && status.cache_statistic === 'best_complete_hour' && status.cache_lookback_hours === 24 && Number.isFinite(start) && start % 3600000 === 0 && end - start === 3600000 && validPercent(status.cache_peak_hour_rate);
  const realSuccess = ready && validPercent(status.request_success_rate);
  const realTime = ready && validTime(status.ttft_p50_ms);
  const probeSuccess = !realSuccess && probeReady && validPercent(status.probe_success_rate);
  const probeTTFT = !realTime && probeReady && validTime(status.probe_ttft_p50_ms);
  const probeLatency = !realTime && !probeTTFT && probeReady && validTime(status.probe_latency_p50_ms);
  return [
    { label: '最佳小时缓存', scope: '同账号池 · 24h', value: cacheReady ? percent(status.cache_peak_hour_rate) : null, probe: false },
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
  const details = statusMetricDetails(status), metrics = el('dl', 'status-metrics');
  metrics.style.setProperty('--status-metric-count', details.filter(metric => metric.value !== null).length);
  details.forEach((metric, index) => {
    if (metric.value === null) return;
    const item = el('div', ''), title = el('dt', '', metric.label);
    title.append(el('span', 'status-metric-scope', metric.scope));
    const value = el('dd', '', metric.value);
    item.append(title, value);
    if (index === 0) {
      value.title = `统计小时：${new Date(status.cache_hour_start).toLocaleString('zh-CN')} – ${new Date(status.cache_hour_end).toLocaleString('zh-CN')}`;
      const date = new Date(status.cache_hour_start).toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit' });
      item.append(el('span', 'status-cache-hour', `${date} ${time(status.cache_hour_start)}–${time(status.cache_hour_end)}`));
    }
    metrics.append(item);
  });
  const slotHeading = el('div', 'status-slot-heading', '近 3 小时 · 每格 10 分钟');
  const detail = el('p', 'status-detail', !status ? '状态暂不可用' : details.some(metric => metric.probe) ? '真实请求样本不足的指标由定时检测补充，两类结果分别统计。' : status.request_sample_state === 'insufficient' ? `本分组样本不足，至少 ${status.min_requests ?? 20} 条有效请求后展示成功率与延迟` : status.request_sample_state === 'stale' ? '请求统计暂不可用' : status.request_sample_state === 'none' ? '本分组近期暂无真实请求，成功率与延迟待采集。' : '成功率与延迟统计本分组真实请求；无数据不计失败');
  const slots = el('div', 'status-slots'); slots.setAttribute('aria-label', '近 18 个十分钟时段');
  const cells = status?.cells?.length === 18 ? status.cells : Array.from({ length: 18 }, () => ({ status: 'unknown', source: 'none' }));
  for (const cell of cells) {
    const label = `${cell.start ? time(cell.start) + ' · ' : ''}${labels[cell.status] ?? labels.unknown} · ${cell.source === 'requests' ? '全组请求' : cell.source === 'probe' ? '分组检测' : '无结果'}`;
    const button = el('button', ''); button.type = 'button'; button.dataset.state = cell.status; button.setAttribute('aria-label', label); button.title = label;
    const explain = () => { detail.textContent = label; }; button.addEventListener('focus', explain); button.addEventListener('click', explain); button.addEventListener('mouseenter', explain); slots.append(button);
  }
  const footer = el('footer', 'status-check', status?.probe_enabled ? '分组检测 · 每 10 分钟一次' : '未启用定时检测');
  card.append(heading, el('p', 'status-window', '近 180 分钟 · 分组表现'), metrics, slotHeading, slots, detail, footer); return card;
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
    const timer = setInterval(load, 60000); window.addEventListener('pagehide', event => { if (event.persisted) return; stopped = true; clearInterval(timer); controller.abort(); document.removeEventListener('visibilitychange', visible); }); load();
  }
}
