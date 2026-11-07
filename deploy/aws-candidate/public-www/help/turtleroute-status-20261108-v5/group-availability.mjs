const labels = { operational: '运行正常', degraded: '部分异常', down: '检测异常', unknown: '暂无近期数据', insufficient: '样本不足', observed: '有成功请求', errors_observed: '有失败记录', collecting: '本时段采集中' };
const platforms = { openai: 'GPT', anthropic: 'Claude', deepseek: 'DeepSeek', kimi: 'Kimi', zhipu: 'GLM', minimax: 'MiniMax', gemini: 'Gemini', antigravity: 'Antigravity' };
const percent = value => typeof value === 'number' && Number.isFinite(value) ? `${value.toFixed(1)}%` : '—';
export function statusMetrics(status) {
  const ready = status?.request_sample_state === 'ready';
  const cacheReady = status?.cache_sample_state === 'ready' && status?.cache_scope === 'shared_account_pool' && Number.isFinite(status.cache_read_rate) && status.cache_read_rate >= 90 && status.cache_read_rate <= 100;
  return [cacheReady ? percent(status.cache_read_rate) : null, ready ? percent(status.request_success_rate) : '—', ready && Number.isFinite(status.ttft_p50_ms) && status.ttft_p50_ms !== null ? `${(status.ttft_p50_ms / 1000).toFixed(1)}s` : '—'];
}
const el = (tag, className, text) => { const node = document.createElement(tag); node.className = className; if (text != null) node.textContent = text; return node; };
const time = date => new Date(date).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });
export function renderStatusGroup(group) {
  const status = group.availability;
  const card = el('article', 'status-group');
  const heading = el('header', 'status-group-heading');
  const title = el('div', ''); title.append(el('span', 'status-platform-label', platforms[group.platform] ?? group.platform), el('h3', '', group.name));
  const badge = el('span', 'status-state', labels[status?.status] ?? labels.unknown); badge.dataset.state = status?.status ?? 'unknown'; heading.append(title, badge);
  const values = statusMetrics(status), metrics = el('dl', 'status-metrics');
  metrics.style.setProperty('--status-metric-count', values.filter(value => value !== null).length);
  ['缓存读取占比', '请求成功率', '首 Token P50'].forEach((label, index) => {
    if (values[index] === null) return;
    const item = el('div', ''), title = el('dt', '', label);
    title.append(el('span', 'status-metric-scope', index === 0 ? '同账号池 · 全模型' : '本分组 · 全模型'));
    item.append(title, el('dd', '', values[index])); metrics.append(item);
  });
  const slotHeading = el('div', 'status-slot-heading', '近 3 小时 · 每格 10 分钟');
  const detail = el('p', 'status-detail', !status ? '状态暂不可用' : status.request_sample_state === 'insufficient' ? `本分组样本不足，至少 ${status.min_requests ?? 20} 条有效请求后展示成功率与延迟` : status.request_sample_state === 'stale' ? '请求统计暂不可用' : status.request_sample_state === 'none' ? '本分组近期暂无真实请求，成功率与延迟待采集。' : '成功率与延迟统计本分组真实请求；无数据不计失败');
  const slots = el('div', 'status-slots'); slots.setAttribute('aria-label', '近 18 个十分钟时段');
  const cells = status?.cells?.length === 18 ? status.cells : Array.from({ length: 18 }, () => ({ status: 'unknown', source: 'none' }));
  for (const cell of cells) {
    const label = `${cell.start ? time(cell.start) + ' · ' : ''}${labels[cell.status] ?? labels.unknown} · ${cell.source === 'requests' ? '全组请求' : cell.source === 'probe' ? '分组检测' : '无结果'}`;
    const button = el('button', ''); button.type = 'button'; button.dataset.state = cell.status; button.setAttribute('aria-label', label); button.title = label;
    const explain = () => { detail.textContent = label; }; button.addEventListener('focus', explain); button.addEventListener('click', explain); button.addEventListener('mouseenter', explain); slots.append(button);
  }
  const footer = el('footer', 'status-check', status?.probe_enabled ? '分组检测 · 每 10 分钟一次' : '未启用定时检测');
  card.append(heading, el('p', 'status-window', '近 180 分钟 · 真实请求'), metrics, slotHeading, slots, detail, footer); return card;
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
