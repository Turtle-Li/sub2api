import test from 'node:test';
import assert from 'node:assert/strict';
import { statusMetrics } from '../help/turtleroute-status-20261108-v6/group-availability.mjs';
const cacheStatus = (overrides = {}) => ({ cache_sample_state: 'ready', cache_scope: 'shared_account_pool', cache_statistic: 'best_complete_hour', cache_lookback_hours: 24, cache_hour_start: '2026-11-07T08:00:00Z', cache_hour_end: '2026-11-07T09:00:00Z', cache_read_rate: null, cache_peak_hour_rate: 97.2, ...overrides });
test('status module hides stale or insufficient request metrics even if old values are present', () => {
  for (const state of ['none', 'insufficient', 'stale']) assert.deepEqual(statusMetrics({ request_sample_state: state, request_success_rate: 66.7, cache_read_rate: 90, ttft_p50_ms: 2000 }), [null, '—', '—']);
});
test('status module preserves actual group success zero and missing latency independently', () => {
  assert.deepEqual(statusMetrics({ request_sample_state: 'ready', request_success_rate: 0, cache_read_rate: 0, ttft_p50_ms: null }), [null, '0.0%', '—']);
  assert.deepEqual(statusMetrics(cacheStatus({ request_sample_state: 'ready', request_success_rate: 99.9, ttft_p50_ms: 3000 })), ['97.2%', '99.9%', '3.0s']);
});
test('pool cache can be ready while the cheap check group has no real requests', () => {
  assert.deepEqual(statusMetrics(cacheStatus({ request_sample_state: 'none', cache_peak_hour_rate: 96.3 })), ['96.3%', '—', '—']);
});
test('unpublished, legacy or invalid cache never becomes a public metric', () => {
  for (const cache_peak_hour_rate of [-1, 101, NaN, Infinity, null]) {
    assert.equal(statusMetrics(cacheStatus({ cache_peak_hour_rate }))[0], null);
  }
  for (const cache_sample_state of ['none', 'insufficient', 'below_threshold', undefined]) {
    assert.equal(statusMetrics(cacheStatus({ cache_sample_state }))[0], null);
  }
  assert.equal(statusMetrics({ cache_sample_state: 'ready', cache_read_rate: 99 })[0], null);
  assert.equal(statusMetrics({ cache_sample_state: 'ready', cache_scope: 'shared_account_pool', cache_read_rate: 99 })[0], null);
  for (const overrides of [{ cache_scope: undefined }, { cache_statistic: undefined }, { cache_lookback_hours: 3 }, { cache_hour_start: null }, { cache_hour_end: 'bad' }, { cache_hour_end: '2026-11-07T09:10:00Z' }, { cache_hour_start: '2026-11-07T08:10:00Z', cache_hour_end: '2026-11-07T09:10:00Z' }]) {
    assert.equal(statusMetrics(cacheStatus(overrides))[0], null);
  }
});
test('qualified best-hour ratios retain actual zero and low values without a floor', () => {
  for (const cache_peak_hour_rate of [0, 52, 89.99, 100]) {
    assert.equal(statusMetrics(cacheStatus({ cache_peak_hour_rate }))[0], `${cache_peak_hour_rate.toFixed(1)}%`);
  }
});
