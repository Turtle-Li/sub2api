import { test } from 'node:test';
import assert from 'node:assert/strict';
import { quoteFor, formatPrice, groupOffers, prioritizeHomepageGroups, publicGroupLabel } from '../help/turtleroute-20261106-v5/catalog-pricing.mjs';

test('orders GPT by verified release date across model families, with stable same-day ties', () => {
  const models = ['gpt-5.5', 'gpt-image-2.5-sunburst', 'gpt-6-astra', 'gpt-5.4-mini', 'gpt-6.1-sol', 'gpt-6-luna',
    'gpt-6-sol', 'gpt-5.6-luna', 'gpt-5.6-terra', 'gpt-5.6-sol', 'gpt-image-2', 'gpt-image-2.5-flare', 'gpt-99-undated']
    .map(name => ({name, platform:'openai'}));
  const group = {id:6, models};
  assert.deepEqual(groupOffers(group).map(model => model.name), [
    'gpt-6.1-sol', 'gpt-6-sol', 'gpt-6-luna', 'gpt-image-2.5-flare', 'gpt-image-2.5-sunburst', 'gpt-6-astra',
    'gpt-5.6-sol', 'gpt-5.6-terra', 'gpt-5.6-luna', 'gpt-5.5', 'gpt-image-2', 'gpt-5.4-mini', 'gpt-99-undated',
  ]);
  assert.equal(group.models[0].name, 'gpt-5.5');
});

test('shows the latest Fable, Opus and Sonnet first, then the other Claude versions', () => {
  const models = ['claude-haiku-4-5-20251001','claude-opus-5-5','claude-fable-5-1','claude-haiku-5-5',
    'claude-fable-5','claude-sonnet-5-5','claude-opus-5-10','claude-sonnet-5-9'].map(name => ({name, platform:'anthropic'}));
  assert.deepEqual(groupOffers({platform:'anthropic',models}).map(model => model.name), [
    'claude-fable-5-1','claude-opus-5-10','claude-sonnet-5-9','claude-fable-5',
    'claude-opus-5-5','claude-sonnet-5-5','claude-haiku-5-5','claude-haiku-4-5-20251001',
  ]);
});

test('leads with GPT, the highest Claude tier and DeepSeek while retaining every group', () => {
  const group = (id, platform, name, ...models) => {
    const raw = {id, platform, name, models:models.map(model => ({name:model, platform}))};
    return {...raw, offers:groupOffers(raw)};
  };
  const groups = [
    group(35,'zhipu','GLM','glm-5.3'),
    group(29,'anthropic','kiro','claude-opus-5-5'),
    group(6,'openai','按量-0.25倍率','gpt-6.1-sol'),
    group(17,'deepseek','DeepSeek','deepseek-v4.1-flash'),
    group(30,'anthropic','Claude 高智99缓','claude-fable-5-1','claude-opus-5-5','claude-sonnet-5-5'),
    group(10,'anthropic','Claude 官方','claude-fable-5-1','claude-opus-5-5','claude-sonnet-5-5','claude-haiku-5-5'),
  ];
  const ordered = prioritizeHomepageGroups(groups);
  assert.deepEqual(ordered.map(g => g.id), [6,10,17,30,29,35]);
  assert.equal(ordered[4], groups[1]);
});

test('keeps meaningful group qualifiers without inventing a route, discount, or plan', () => {
  const label = name => publicGroupLabel({name, platform:'anthropic'});
  assert.deepEqual(['kiro','Claude 90缓','Claude 高智99缓 ','Claude 官方','Claude Kiro缓存99%','Claude 官方max'].map(label),
    ['Kiro','缓存 90%','高智 缓存 99%','官方','Kiro 缓存 99%','官方 Max']);
  assert.equal(label('Claude'), '');
  assert.equal(publicGroupLabel({name:'按量-0.25倍率', platform:'openai'}), '');
});

test('keeps identical models in separate groups with one associated quote each', () => {
  const models = [{name:'same-model',platform:'anthropic',pricing:{input_price:1e-6}}];
  const first = groupOffers({id:10,rate_multiplier:.4,display_currency:'USD',models})[0];
  const second = groupOffers({id:20,rate_multiplier:.8,display_currency:'USD',models})[0];
  assert.equal(first.quotes.length,1);
  assert.equal(second.quotes.length,1);
  assert.equal(first.groupId,10);
  assert.equal(second.groupId,20);
  assert.equal(formatPrice(first.quotes[0].input),'$0.40');
  assert.equal(formatPrice(second.quotes[0].input),'$0.80');
});

test('falls back to root media price when intervals have no request prices', () => {
  const quote = quoteFor({rate_multiplier:.2,display_currency:'USD'}, {pricing:{billing_mode:'image',per_request_price:.1,intervals:[{min_tokens:0,per_request_price:null}]}});
  assert.equal(formatPrice(quote.requests[0]), '$0.02');
});

test('applies the plaza group display rate and multiplier exactly once', () => {
  const group = { rate_multiplier:.3, display_currency:'CNY', display_usd_rate:7 };
  const model = { pricing:{ billing_mode:'token', input_price:1.14285714286e-6, output_price:4e-6, cache_read_price:2.85714285714e-7 } };
  const quote = quoteFor(group, model);
  assert.equal(formatPrice(quote.input), '$2.40');
  assert.equal(formatPrice(quote.output), '$8.40');
  assert.equal(formatPrice(quote.cache), '$0.60');
});
test('honors the owner one-to-one display rate and user multiplier', () => {
  const quote = quoteFor({rate_multiplier:.25,user_rate_multiplier:.2,display_currency:'CNY',display_usd_rate:1}, {pricing:{input_price:5e-6,output_price:30e-6}});
  assert.equal(quote.rate,.2);
  assert.equal(formatPrice(quote.input),'$1.00');
  assert.equal(formatPrice(quote.output),'$6.00');
});
test('uses image multiplier and per-image tiers without token scaling', () => {
  const quote = quoteFor({rate_multiplier:.25,image_rate_independent:true,image_rate_multiplier:.6,display_currency:'CNY',display_usd_rate:1}, {pricing:{billing_mode:'image',intervals:[{per_request_price:.1},{per_request_price:.2}]}});
  assert.equal(quote.rate,.6);
  assert.equal(quote.unit,'张');
  assert.deepEqual(quote.requests,[.06,.12]);
});
test('distinguishes free, missing price and missing conversion', () => {
  assert.equal(formatPrice(0),'$0.00');
  assert.equal(formatPrice(null),'—');
  assert.equal(quoteFor({rate_multiplier:1,display_currency:'CNY'}, {pricing:{input_price:1e-6}}).input,null);
  assert.equal(quoteFor({rate_multiplier:1,display_currency:'USD'}, {pricing:null}).input,null);
});
test('uses video independent multiplier and preserves tiny prices', () => {
  const quote=quoteFor({rate_multiplier:1,video_rate_independent:true,video_rate_multiplier:.2,display_currency:'USD'}, {pricing:{billing_mode:'video',per_request_price:.01}});
  assert.equal(quote.unit,'次');
  assert.deepEqual(quote.requests,[.002]);
  assert.equal(formatPrice(.002),'$0.002');
});
test('takes the first context tier with absolute and multiplier fallback prices', () => {
  const quote = quoteFor({rate_multiplier:.5,display_currency:'USD'}, {pricing:{input_price:1e-6,output_price:2e-6,cache_read_price:1e-7,intervals:[{min_tokens:1000,input_price:9e-6},{min_tokens:0,input_price:3e-6,output_price:4e-6,cache_read_multiplier:2}]}});
  assert.equal(formatPrice(quote.input),'$1.50');
  assert.equal(formatPrice(quote.output),'$2.00');
  assert.equal(formatPrice(quote.cache),'$0.10');
});
