import { quoteFor, formatPrice, groupOffers, compareHomepageModels, prioritizeHomepageGroups, publicGroupLabel } from './catalog-pricing.mjs';
const SITE = 'https://www.turtleligpt.com';
const providerInfo = {
  openai: ['OpenAI', 'O'], anthropic: ['Anthropic', 'A'], deepseek: ['DeepSeek', 'D'],
  kimi: ['Kimi', 'K'], minimax: ['MiniMax', 'M'], zhipu: ['智谱 GLM', 'Z'], gemini: ['Gemini', 'G'],
};
const state = { models: [], groups: [], provider: 'all', search: '', source: '', language: 'python', page: 1 };
const groupPages = new Map();
class CatalogError extends Error {}

function node(tag, className = '', text = '') {
  const element = document.createElement(tag);
  element.className = className;
  element.textContent = text;
  return element;
}
function providerName(id) { return providerInfo[id]?.[0] ?? id; }
function mark(id) {
  const element = node('span', 'provider-mark', providerInfo[id]?.[1] ?? id.slice(0, 1).toUpperCase());
  element.dataset.provider = id;
  element.setAttribute('aria-hidden', 'true');
  return element;
}
function parseCatalog(payload) {
  if (!payload || payload.code !== 0 || !Array.isArray(payload.data?.groups)) throw new CatalogError('目录暂不可用');
  const models = new Map();
  const groups = payload.data.groups.filter(group => typeof group?.name === 'string' && typeof group.platform === 'string' && Array.isArray(group.models))
    .map(group => ({...group, label: publicGroupLabel(group), offers: groupOffers(group)}));
  for (const group of groups) {
    for (const model of group.models) {
      if (typeof model?.name !== 'string' || typeof model.platform !== 'string') continue;
      const key = `${model.platform}:${model.name}`;
      const item = models.get(key) ?? { name: model.name, platform: model.platform, groups: [], quotes: [], official: model.official_pricing };
      const quote = quoteFor(group, model);
      if (!item.quotes.some(existing => JSON.stringify(existing) === JSON.stringify(quote))) item.quotes.push(quote);
      if (!item.groups.some(existing => existing.id === group.id)) item.groups.push({ id: group.id, name: group.name, platform: group.platform, description: typeof group.description === 'string' ? group.description : '' });
      models.set(key, item);
    }
  }
  return { groups: prioritizeHomepageGroups(groups), models: [...models.values()].sort(compareHomepageModels) };
}
function modelCard(model) {
  const article = node('article', 'model-card');
  const button = node('button', 'model-open'); button.type = 'button';
  button.setAttribute('aria-label', `查看 ${model.name} 的倍率与价格`);
  const top = node('div', 'model-card-top'); top.append(mark(model.platform), node('span', 'model-family', providerName(model.platform)));
  const meta = node('div', 'model-meta'); meta.append(node('span', '', '标准时段基础档'), node('span', 'model-arrow', '↗'));
  button.append(top, node('h3', 'model-name', model.name), quoteTable(model), meta);
  button.addEventListener('click', () => openModel(model));
  article.append(button); return article;
}
function quoteTable(model) {
  const table = node('div', 'quote-table');
  const header = node('div', 'quote-row quote-heading');
  const tokenOnly = model.quotes.every(quote => quote.token);
  for (const label of tokenOnly ? ['倍率', '输入', '输出', '缓存命中'] : ['倍率', '价格']) header.append(node('span', label === '价格' ? 'quote-media' : '', label));
  table.append(header);
  for (const quote of [...model.quotes].sort((a,b) => (a.rate ?? Infinity) - (b.rate ?? Infinity))) {
    const row = node('div', 'quote-row');
    row.append(node('strong', 'quote-rate', quote.rate === null ? '—' : quote.rate + '×'));
    if (quote.token) {
      for (const value of [quote.input, quote.output, quote.cache]) row.append(node('span', '', formatPrice(value)));
    } else {
      const low = quote.requests[0], high = quote.requests.at(-1);
      row.append(node('span', 'quote-media', low === undefined ? '价格待公布' : formatPrice(low) + (high !== low ? ' – ' + formatPrice(high) : '') + ' / ' + quote.unit));
    }
    table.append(row);
  }
  return table;
}
function renderModels() {
  const query = state.search.trim().toLowerCase();
  if (document.querySelector('[data-grouped]')) {
    const normalize = value => value.toLowerCase().replace(/\s+/gu, '');
    const term = normalize(query);
    const groups = state.groups.map(group => {
      const matchesGroup = [group.name, group.label].some(value => normalize(value).includes(term));
      const offers = group.offers.filter(model => (state.provider === 'all' || model.platform === state.provider)
        && (matchesGroup || [model.name, providerName(model.platform)].some(value => normalize(value).includes(term))));
      return {...group, offers};
    }).filter(group => group.offers.length);
    document.querySelectorAll('[data-models]').forEach(container => {
      const limit = Number(container.dataset.limit ?? 6) * state.page;
      container.replaceChildren(...groups.slice(0, limit).map(groupCard));
      container.setAttribute('aria-busy', 'false');
      document.querySelectorAll('[data-shown-count]').forEach(element => { element.textContent = String(Math.min(groups.length, limit)); });
      document.querySelectorAll('[data-load-more]').forEach(element => { element.hidden = groups.length <= limit; });
    });
    document.querySelectorAll('[data-empty]').forEach(element => { element.hidden = groups.length > 0; });
    document.querySelectorAll('[data-result-count]').forEach(element => { element.textContent = String(groups.length); });
    return;
  }
  const matches = state.models.filter(model => (state.provider === 'all' || model.platform === state.provider) && `${model.name} ${providerName(model.platform)} ${model.groups.map(group => group.name).join(' ')}`.toLowerCase().includes(query));
  document.querySelectorAll('[data-models]').forEach(container => {
    const limit = Number(container.dataset.limit ?? 6) * state.page;
    container.replaceChildren(...matches.slice(0, limit).map(modelCard));
    container.setAttribute('aria-busy', 'false');
    document.querySelectorAll('[data-shown-count]').forEach(element => { element.textContent = String(Math.min(matches.length, limit)); });
    document.querySelectorAll('[data-load-more]').forEach(element => { element.hidden = matches.length <= limit; });
  });
  document.querySelectorAll('[data-empty]').forEach(element => { element.hidden = matches.length > 0; });
  document.querySelectorAll('[data-result-count]').forEach(element => { element.textContent = String(matches.length); });
}

function groupCard(group) {
  const card = node('article', 'plaza-group');
  card.dataset.groupId = String(group.id);
  const header = node('header', 'plaza-group-header');
  const identity = node('div', 'plaza-group-identity');
  identity.append(mark(group.platform), node('h3', '', providerName(group.platform)));
  const rawRate = group.user_rate_multiplier ?? group.rate_multiplier;
  const rate = typeof rawRate === 'number' && Number.isFinite(rawRate) && rawRate >= 0 ? rawRate : null;
  const badge = node('span', 'plaza-rate', rate !== null ? rate + '× 倍率' : '倍率待公布');
  header.append(identity, badge);
  const context = node('div', 'plaza-group-context');
  const billing = group.subscription_type === 'subscription' ? '订阅分组' : group.is_exclusive ? '专属分组' : '';
  const label = [group.label, billing].filter(Boolean).join(' · ');
  const labelElement = node('span', group.label ? 'plaza-group-label' : '', label || '按量计费');
  if (group.label) labelElement.title = label;
  context.append(labelElement);
  context.append(node('span', '', `${group.offers.length} 款模型`));
  const body = node('div', 'plaza-group-models');
  const footer = node('footer', 'plaza-group-footer');
  const link = node('a', '', '完整定价 ↗'); link.href = `${SITE}/model-plaza`; link.target = '_blank'; link.rel = 'noopener';
  const pager = node('div', 'plaza-group-pager');
  const previous = node('button', '', '←'), next = node('button', '', '→');
  previous.type = next.type = 'button';
  previous.setAttribute('aria-label', '上一页模型'); next.setAttribute('aria-label', '下一页模型');
  const position = node('span'); position.setAttribute('aria-live', 'polite');
  pager.append(previous, position, next);
  footer.append(link, pager);
  const pageSize = 3, pages = Math.ceil(group.offers.length / pageSize);
  let page = Math.min(groupPages.get(group.id) ?? 0, pages - 1);
  const render = () => {
    groupPages.set(group.id, page);
    body.replaceChildren(...group.offers.slice(page * pageSize, (page + 1) * pageSize).map(model => {
      const row = node('button', 'plaza-model-row'); row.type = 'button'; row.dataset.model = model.name;
      row.setAttribute('aria-label', ['查看', model.name, group.label, model.quotes[0].rate ?? '未知', '倍率定价'].filter(value => value !== '').join(' '));
      const name = node('div', 'plaza-model-title'); name.append(node('strong', '', model.name), node('span', '', '↗'));
      const prices = node('div', 'plaza-model-prices');
      const quote = model.quotes[0];
      if (quote.token) {
        for (const [label, amount] of [['输入',quote.input],['输出',quote.output],['缓存',quote.cache]]) {
          const price = node('span'); price.append(node('small', '', label), node('b', '', formatPrice(amount))); prices.append(price);
        }
      } else {
        const value = quote.requests[0], high = quote.requests.at(-1);
        const price = node('span', 'plaza-media-price'); price.append(node('small', '', '每' + quote.unit), node('b', '', value === undefined ? '价格待公布' : formatPrice(value) + (high === value ? '' : ' – ' + formatPrice(high)))); prices.append(price);
      }
      if (quote.rate !== rate) name.append(node('span', 'quote-rate', quote.rate === null ? '—' : quote.rate + '×'));
      row.append(name, prices); row.addEventListener('click', () => openModel({...model, groupLabel: group.label})); return row;
    }));
    position.textContent = `${page + 1} / ${pages}`;
    previous.disabled = page === 0; next.disabled = page === pages - 1;
    pager.hidden = pages === 1;
  };
  previous.addEventListener('click', () => { page--; render(); });
  next.addEventListener('click', () => { page++; render(); });
  card.append(header, context, body, footer); render(); return card;
}
function renderProviders() {
  const platforms = [...new Set(state.models.map(model => model.platform))];
  document.querySelectorAll('[data-platform-track]').forEach(track => {
    const strip = node('div', 'platform-strip');
    for (const platform of platforms) strip.append(node('span', '', providerName(platform)));
    const copy = strip.cloneNode(true);
    copy.setAttribute('aria-hidden', 'true');
    track.replaceChildren(strip, copy);
  });
  document.querySelectorAll('[data-providers]').forEach(container => {
    container.replaceChildren(...['all', ...platforms].map(id => {
      const button = node('button', 'provider-filter', id === 'all' ? '全部' : providerName(id));
      button.type = 'button'; button.dataset.provider = id;
      button.setAttribute('aria-pressed', String(id === state.provider));
      button.addEventListener('click', () => {
        state.provider = id;
        state.page = 1;
        groupPages.clear();
        document.querySelectorAll('.provider-filter').forEach(item => item.setAttribute('aria-pressed', String(item.dataset.provider === id)));
        renderModels();
      });
      return button;
    }));
  });
}
function renderFeatured() {
  const picks = new Map();
  for (const model of state.models) if (!picks.has(model.platform)) picks.set(model.platform, model);
  document.querySelectorAll('[data-featured]').forEach(container => {
    container.replaceChildren(...[...picks.values()].slice(0, 3).map(model => {
      const button = node('button', 'feature-model'); button.type = 'button';
      const text = node('span'); text.append(node('strong', '', model.name), node('small', '', providerName(model.platform)));
      button.append(mark(model.platform), text); button.addEventListener('click', () => openModel(model)); return button;
    }));
  });
}
function renderCatalog(catalog, source) {
  state.models = catalog.models; state.groups = catalog.groups; state.source = source;
  if (!state.models.some(model => model.platform === state.provider)) state.provider = 'all';
  for (const [selector, value] of [['[data-model-count]', state.models.length], ['[data-group-count]', state.groups.length], ['[data-provider-count]', new Set(state.models.map(model => model.platform)).size]]) {
    document.querySelectorAll(selector).forEach(element => { element.textContent = String(value); });
  }
  document.querySelectorAll('[data-source-status]').forEach(element => { element.textContent = source; });
  renderProviders(); renderModels(); renderFeatured(); renderCode();
  document.dispatchEvent(new Event('sub2:catalog'));
}
async function refreshCatalog() {
  document.querySelectorAll('[data-retry]').forEach(button => { button.disabled = true; });
  document.querySelectorAll('[data-models]').forEach(container => {
    container.setAttribute('aria-busy', 'true');
    if (!state.models.length) container.replaceChildren(node('p', 'catalog-loading', '正在读取模型与价格'));
  });
  try {
    const url = '/api/v1/model-plaza';
    const response = await fetch(url, { credentials: 'omit', signal: AbortSignal.timeout(18_000) });
    if (!response.ok) throw new CatalogError(`目录请求失败 (${response.status})`);
    renderCatalog(parseCatalog(await response.json()), '已同步公开模型目录 · 模型与分组随目录更新');
  } catch (error) {
    if (!(error instanceof Error)) throw error;
    const snapshot = window.SUB2_SNAPSHOT;
    if (snapshot) renderCatalog(parseCatalog(snapshot.payload), `当前显示 ${snapshot.fetchedAt.slice(0, 10)} 目录快照 · 可刷新重试`);
    else {
      document.querySelectorAll('[data-source-status]').forEach(element => { element.textContent = state.models.length ? '刷新失败 当前保留上次目录 可重试更新' : '暂时无法读取模型目录 可点击刷新重试'; });
      if (!state.models.length) document.querySelectorAll('[data-models]').forEach(container => container.replaceChildren(node('p', 'catalog-loading', '模型目录暂时不可用 请点击刷新重试')));
    }
  } finally {
    document.querySelectorAll('[data-models]').forEach(container => container.setAttribute('aria-busy', 'false'));
    document.querySelectorAll('[data-retry]').forEach(button => { button.disabled = false; });
  }
}

const dialog = node('dialog', 'model-dialog');
dialog.setAttribute('aria-labelledby', 'model-dialog-title');
const close = node('button', 'dialog-close', '×'); close.type = 'button'; close.setAttribute('aria-label', '关闭模型详情');
close.addEventListener('click', () => dialog.close());
dialog.addEventListener('click', event => { if (event.target === dialog) { const box = dialog.getBoundingClientRect(); if (event.clientX < box.left || event.clientX > box.right || event.clientY < box.top || event.clientY > box.bottom) dialog.close(); } });
document.body.append(dialog);
function openModel(model) {
  if (document.querySelector('[data-grouped]') && model.groupId === undefined) {
    state.search = model.name; state.provider = 'all'; state.page = 1;
    groupPages.clear();
    document.querySelectorAll('[data-search]').forEach(input => { input.value = model.name; });
    renderProviders(); renderModels();
    document.querySelector('#models')?.scrollIntoView({behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth'});
    return;
  }
  const title = node('h2', '', model.name); title.id = 'model-dialog-title';
  const units = [...new Set(model.quotes.map(quote => '$ / ' + quote.unit))].join(' 或 ');
  const intro = node('p', '', [providerName(model.platform), model.groupLabel, units].filter(Boolean).join(' · '));
  const note = node('p', '', '公开倍率下的标准时段基础档价格 已含倍率 不同倍率分别展示 长上下文 分时 高峰和推理强度价格以完整模型广场为准');
  const link = node('a', 'dialog-link', '在模型广场查看完整定价 ↗'); link.href = `${SITE}/model-plaza`; link.target = '_blank'; link.rel = 'noopener';
  dialog.replaceChildren(close, mark(model.platform), title, intro, quoteTable(model), note, link); dialog.showModal();
}
function codeText() {
  const model = state.models.find(item => item.name === 'gpt-6.1-sol')?.name ?? 'YOUR_MODEL_ID';
  if (state.language === 'curl') return [
    'curl https://api.turtleligpt.com/v1/responses \\',
    '  -H "Authorization: Bearer YOUR_API_KEY" \\',
    '  -H "Content-Type: application/json" \\',
    "  -d '{",
    '    "model": "' + model + '",',
    '    "input": "你好，介绍一下你自己。"',
    "  }'",
  ].join('\n');
  return [
    'from openai import OpenAI', '',
    'client = OpenAI(',
    '    api_key="YOUR_API_KEY",',
    '    base_url="https://api.turtleligpt.com/v1"',
    ')', '',
    'response = client.responses.create(',
    '    model="' + model + '",',
    '    input="你好，介绍一下你自己。"',
    ')', '',
    'print(response.output_text)',
  ].join('\n');
}
function renderCode() { document.querySelectorAll('[data-code]').forEach(element => { element.textContent = codeText(); }); }
document.querySelectorAll('[data-language]').forEach(button => {
  button.setAttribute('aria-pressed', String(button.dataset.language === state.language));
  button.addEventListener('click', () => {
    state.language = button.dataset.language;
    document.querySelectorAll('[data-language]').forEach(item => item.setAttribute('aria-pressed', String(item.dataset.language === state.language)));
    renderCode();
  });
});
document.querySelectorAll('[data-copy]').forEach(button => button.addEventListener('click', async () => {
  try { await navigator.clipboard.writeText(codeText()); button.textContent = '已复制 ✓'; }
  catch (error) { if (!(error instanceof Error)) throw error; button.textContent = '请选中代码复制'; }
}));
document.querySelectorAll('[data-search]').forEach(input => input.addEventListener('input', () => { state.search = input.value; state.page = 1; groupPages.clear(); renderModels(); }));
document.querySelectorAll('[data-load-more]').forEach(button => button.addEventListener('click', () => { state.page += 1; renderModels(); }));
document.querySelectorAll('[data-retry]').forEach(button => button.addEventListener('click', refreshCatalog));
document.querySelectorAll('[data-docs-link]').forEach(link => { link.href = 'developer.html#integrate'; });
if (window.SUB2_SNAPSHOT) renderCatalog(parseCatalog(window.SUB2_SNAPSHOT.payload), `当前显示 ${window.SUB2_SNAPSHOT.fetchedAt.slice(0, 10)} 目录快照 · 正在同步`);
else renderCode();
refreshCatalog();

export function observeCatalog(listener) {
  const update = () => listener({ models: state.models, groups: state.groups, source: state.source });
  document.addEventListener('sub2:catalog', update);
  if (state.models.length) update();
  return () => document.removeEventListener('sub2:catalog', update);
}
export { openModel as showModel };
