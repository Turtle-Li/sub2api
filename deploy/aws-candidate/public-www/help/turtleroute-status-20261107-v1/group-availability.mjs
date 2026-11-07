import { observeCatalog } from '../turtleroute-20261106-v5/shared.js';

const states = { operational: '运行正常', degraded: '部分异常', down: '检测异常', unknown: '待确认', insufficient: '样本不足' };
const unknown = () => ({ status: 'unknown', source: 'none', checked_at: null, probe_enabled: false, request_success_rate: null, probe_success_rate: null, cells: [] });
let groups = new Map(), unavailable = false, pending = false;
const create = (tag, name, text = '') => { const element = document.createElement(tag); element.className = name; element.textContent = text; return element; };
const time = value => new Date(value).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });

export function groupAvailability(status, failed = false) {
  const root = create('section', 'plaza-availability');
  root.setAttribute('aria-label', '分组可用状态');
  root.dataset.state = failed ? 'unknown' : status.status;
  const heading = create('div', 'availability-heading');
  heading.append(create('span', 'availability-state', failed ? '状态读取失败' : states[status.status] ?? states.unknown));
  const real = status.request_success_rate, probe = status.probe_success_rate;
  const rate = real ?? probe;
  const label = real !== null ? '24h 请求成功率' : probe !== null ? '24h 检测正常率' : status.probe_enabled ? '等待首次检测' : '暂无请求样本';
  const metric = create('span', 'availability-rate', label);
  if (typeof rate === 'number' && Number.isFinite(rate)) metric.append(create('strong', '', ` ${rate.toFixed(1)}%`));
  heading.append(metric);
  const strip = create('div', 'availability-strip');
  strip.setAttribute('role', 'img'); strip.setAttribute('aria-label', '最近六小时状态，灰色表示没有足够样本');
  for (const cell of status.cells.length ? status.cells : Array.from({ length: 36 }, () => ({ status: 'unknown' }))) {
    const bar = create('span', ''); bar.dataset.state = cell.status;
    bar.title = cell.start ? `${time(cell.start)} · ${states[cell.status] ?? states.unknown} · ${cell.source === 'probe' ? '主动检测' : cell.source === 'requests' ? '真实请求' : '暂无样本'}` : '暂无样本';
    strip.append(bar);
  }
  const dates = create('div', 'availability-dates');
  dates.append(create('span', '', '6 小时前'), create('span', '', status.checked_at && !failed ? `${status.source === 'probe' ? '检测于' : '采样于'} ${time(status.checked_at)}` : status.probe_enabled ? '等待首次检测' : '未启用定时检测'));
  const details = create('details', 'availability-details');
  details.append(create('summary', '', status.probe_enabled ? '每 10 分钟检测 · 查看口径' : '真实请求统计 · 查看口径'));
  if (status.probe_model) details.append(create('p', 'availability-model', `检测模型：${status.probe_model}`));
  details.append(create('p', '', '定时检测只验证代表模型；请求成功率统计分组真实请求，排除用户中断、余额不足等。灰色表示没有足够样本。'));
  if (real !== null && probe !== null) details.append(create('p', '', `24h 检测正常率 ${probe.toFixed(1)}%`));
  root.append(heading, strip, dates, details);
  return root;
}

function render() {
  document.querySelectorAll('.plaza-group[data-group-id]').forEach(card => {
    const prior = card.querySelector('.plaza-availability');
    const replacement = groupAvailability(groups.get(Number(card.dataset.groupId)) ?? unknown(), unavailable);
    const open = prior?.querySelector('details')?.open;
    const focused = prior?.contains(document.activeElement);
    if (open) replacement.querySelector('details').open = true;
    if (prior) prior.replaceWith(replacement);
    else card.querySelector('.plaza-group-models')?.before(replacement);
    if (focused) replacement.querySelector('summary').focus({ preventScroll: true });
  });
}

async function refresh() {
  if (pending || document.hidden) return;
  pending = true;
  try {
    const response = await fetch('/api/v1/model-plaza', { credentials: 'omit', cache: 'no-store', signal: AbortSignal.timeout(12_000) });
    if (!response.ok) throw new Error('status unavailable');
    const payload = await response.json();
    if (payload?.code !== 0 || !Array.isArray(payload.data?.groups)) throw new Error('status unavailable');
    groups = new Map(payload.data.groups.map(group => [group.id, group.availability ?? unknown()]));
    unavailable = Boolean(payload.data.status_unavailable);
  } catch {
    groups = new Map(); unavailable = true;
  } finally { pending = false; render(); }
}

// Catalog snapshots carry prices only. Status always comes from a fresh,
// sanitized public response, never from a saved snapshot or fallback cache.
const stop = observeCatalog(render);
const observers = [...document.querySelectorAll('[data-models][data-grouped]')].map(grid => {
  const observer = new MutationObserver(render); observer.observe(grid, { childList: true }); return observer;
});
const timer = setInterval(refresh, 60_000);
const visible = () => { if (!document.hidden) void refresh(); };
document.addEventListener('visibilitychange', visible);
window.addEventListener('pagehide', event => { if (event.persisted) return; clearInterval(timer); stop(); observers.forEach(observer => observer.disconnect()); document.removeEventListener('visibilitychange', visible); });
void refresh();
