/** @param {unknown} value */
const valid = value => typeof value === 'number' && Number.isFinite(value) && value >= 0;

const priorityPlatforms = ['openai', 'anthropic', 'deepseek'];
const platformOrder = platform => {
  const index = priorityPlatforms.indexOf(platform);
  return index < 0 ? priorityPlatforms.length : index;
};
const versionFormats = new Map([
  ['anthropic', /^claude-[a-z]+-(\d+(?:[.-]\d{1,2}(?=[.-]|$))*)(?=-|$)/],
  ['deepseek', /^deepseek-v(\d+(?:\.\d+)*)(?=-|$)/],
  ['gemini', /^gemini-(\d+(?:\.\d+)*)(?=-|$)/],
  ['kimi', /^kimi-k(\d+(?:\.\d+)*)(?=-|$)/],
  ['minimax', /^minimax-m(\d+(?:\.\d+)*)(?=-|$)/],
  ['zhipu', /^glm-(\d+(?:\.\d+)*)(?=-|$)/],
]);

// Public API launch dates from the OpenAI API changelog; the plaza has no dates.
// Entries with the same date use the launch announcement's model order.
const openAiReleases = new Map([
  ['gpt-6.1-sol', 20260929],
  ['gpt-6-sol', 20260922], ['gpt-6-luna', 20260922],
  ['gpt-image-2.5-flare', 20260908], ['gpt-image-2.5-sunburst', 20260908],
  ['gpt-6-astra', 20260903],
  ['gpt-5.6-sol', 20260709], ['gpt-5.6-terra', 20260709], ['gpt-5.6-luna', 20260709],
  ['gpt-5.5', 20260424],
  ['gpt-image-2', 20260421],
  ['gpt-5.4-mini', 20260317],
].map(([name, day], position) => [name, {day, position}]));

const featuredClaudeTiers = ['fable', 'opus', 'sonnet'];
const claudeTiers = [...featuredClaudeTiers, 'haiku'];
const claudeFamily = name => /^claude-([a-z]+)-/u.exec(name)?.[1];
const claudeTierOrder = name => {
  const index = claudeTiers.indexOf(claudeFamily(name));
  return index < 0 ? claudeTiers.length : index;
};

export function compareHomepageModels(a, b) {
  if (a.platform !== b.platform) return platformOrder(a.platform) - platformOrder(b.platform) || a.platform.localeCompare(b.platform);
  const left = a.name.toLowerCase(), right = b.name.toLowerCase();
  if (a.platform === 'openai') {
    const first = openAiReleases.get(left), second = openAiReleases.get(right);
    return (second?.day ?? 0) - (first?.day ?? 0) || (first?.position ?? 0) - (second?.position ?? 0);
  }
  if (a.platform === 'anthropic') {
    const tier = claudeTierOrder(left) - claudeTierOrder(right);
    if (tier) return tier;
  }
  const pattern = versionFormats.get(a.platform);
  const version = name => pattern?.exec(name)?.[1].split(/[.-]/).map(Number);
  const leftVersion = version(left), rightVersion = version(right);
  if (!leftVersion || !rightVersion) return Number(Boolean(rightVersion)) - Number(Boolean(leftVersion));
  for (let i = 0; i < Math.max(leftVersion.length, rightVersion.length); i++) {
    const difference = (rightVersion[i] ?? 0) - (leftVersion[i] ?? 0);
    if (difference) return difference;
  }
  if (a.platform === 'gemini') {
    const imageOrder = Number(left.includes('-image')) - Number(right.includes('-image'));
    if (imageOrder) return imageOrder;
  }
  return 0;
}

export function groupOffers(group) {
  const sorted = group.models.filter(model => typeof model?.name === 'string' && typeof model.platform === 'string')
    .sort(compareHomepageModels);
  const featured = group.platform === 'anthropic' ? featuredClaudeTiers
    .map(tier => sorted.find(model => model.platform === 'anthropic' && claudeFamily(model.name.toLowerCase()) === tier))
    .filter(Boolean) : [];
  return [...featured, ...sorted.filter(model => !featured.includes(model))].map(model => ({
    name: model.name, platform: model.platform, groupId: group.id,
    quotes: [quoteFor(group, model)], groups: [{id: group.id}],
  }));
}

export function prioritizeHomepageGroups(groups) {
  const priority = priorityPlatforms.map(platform => groups.filter(group => group.platform === platform && group.offers.length)
    .sort((a, b) => compareHomepageModels(a.offers[0], b.offers[0]) || (platform === 'anthropic' ? b.offers.length - a.offers.length : 0)));
  const leading = priority.flatMap(matches => matches.slice(0, 1));
  const remainingPriority = priority.flatMap(matches => matches.slice(1));
  const selected = new Set([...leading, ...remainingPriority]);
  return [...leading, ...remainingPriority, ...groups.filter(group => !selected.has(group))];
}

export function publicGroupLabel(group) {
  let label = group.name.trim().replace(/\s+/gu, ' ').replace(/^(?:按量(?:计费)?|订阅(?:分组)?)[\s:：·_-]+/u, '');
  if (group.platform === 'anthropic') {
    label = label.replace(/^(?:claude|anthropic)(?=$|[\s:：·_-]|\p{Script=Han})[\s:：·_-]*/iu, '');
  }
  if (label.toLowerCase() === group.platform || (group.platform === 'zhipu' && /^glm$/iu.test(label))
    || /^(?:按量(?:计费)?|订阅(?:分组)?|专属(?:分组)?|\d+(?:\.\d+)?\s*(?:×\s*)?倍率)$/u.test(label)) return '';
  return label.replace(/\bkiro\b/giu, 'Kiro')
    .replace(/\s*(90|99)\s*缓(?:存)?(?=\s|$)/gu, ' 缓存 $1%')
    .replace(/\s*缓存\s*(\d{1,3})\s*%/gu, ' 缓存 $1%')
    .replace(/官方\s*max\b/giu, '官方 Max')
    .trim();
}

/** Standard-period base-tier quotes, matching PlazaModelPricingTable and settlementCurrency. */
export function quoteFor(group, model) {
  const pricing = model.pricing;
  const mode = pricing?.billing_mode || 'token';
  const generalRate = group.user_rate_multiplier ?? group.rate_multiplier;
  const rate = mode === 'image' && group.image_rate_independent ? group.image_rate_multiplier ?? 1
    : mode === 'video' && group.video_rate_independent ? group.video_rate_multiplier ?? 1 : generalRate;
  const conversion = group.display_currency === 'USD' ? 1
    : group.display_currency === 'CNY' && valid(group.display_usd_rate) && group.display_usd_rate > 0 ? group.display_usd_rate : null;
  const factor = valid(rate) && conversion !== null ? rate * conversion : null;
  const amount = (value, scale) => valid(value) && factor !== null ? value * factor * scale : null;
  const intervals = pricing?.intervals ?? [];
  const baseTier = [...intervals].sort((a,b) => (a.min_tokens ?? 0) - (b.min_tokens ?? 0))[0];
  const tierPrice = (field, multiplier) => baseTier?.[field] ?? (pricing?.[field] == null ? null : pricing[field] * (baseTier?.[multiplier] ?? 1));
  const requestPrices = intervals.map(tier => tier.per_request_price).filter(valid);
  const requests = (requestPrices.length ? requestPrices : [pricing?.per_request_price])
    .map(value => amount(value, 1)).filter(value => value !== null).sort((a,b) => a-b);
  return {
    rate: valid(rate) ? rate : null,
    unit: mode === 'token' ? '百万 tokens' : mode === 'image' ? '张' : '次',
    input: amount(tierPrice('input_price', 'input_multiplier'), 1_000_000),
    output: amount(tierPrice('output_price', 'output_multiplier'), 1_000_000),
    cache: amount(tierPrice('cache_read_price', 'cache_read_multiplier'), 1_000_000),
    requests,
    token: mode === 'token',
  };
}

/** @param {number | null} value */
export function formatPrice(value) {
  if (value === null || !Number.isFinite(value)) return '—';
  return '$' + Number(value.toPrecision(10)).toLocaleString('en-US', {minimumFractionDigits:2, maximumFractionDigits:10});
}
