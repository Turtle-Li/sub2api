import test from 'node:test';
import assert from 'node:assert/strict';
import { statusMetrics, statusMetricDetails } from '../help/turtleroute-status-20261108-v7/group-availability.mjs';
const cacheStatus = (overrides = {}) => ({ cache_sample_state: 'ready', cache_scope: 'shared_account_pool', cache_statistic: 'best_complete_hour', cache_lookback_hours: 24, cache_hour_start: '2026-11-07T08:00:00Z', cache_hour_end: '2026-11-07T09:00:00Z', cache_read_rate: null, cache_peak_hour_rate: 97.2, ...overrides });
const probeStatus = (overrides = {}) => ({ request_sample_state: 'none', probe_metrics_version: 1, probe_enabled: true, probe_sample_state: 'ready', probe_success_rate: 100, probe_ttft_p50_ms: 1200, probe_latency_p50_ms: 4500, ...overrides });
test('quiet groups use separately labelled qualified check metrics, preserving real pool cache', () => {
  const status = cacheStatus(probeStatus());
  assert.deepEqual(statusMetrics(status), ['97.2%', '100.0%', '1.2s']);
  const metrics = statusMetricDetails(status);
  assert.equal(metrics[1].label, '检测成功率');
  assert.equal(metrics[2].label, '检测首 Token P50');
  assert.equal(metrics[2].scope, '本分组 · 定时检测');
  assert.equal(metrics[0].probe, false);
});
test('legacy checks expose total duration without claiming a first-token measurement', () => {
  const status = probeStatus({ probe_ttft_p50_ms: null });
  assert.equal(statusMetrics(status)[2], '4.5s');
  assert.equal(statusMetricDetails(status)[2].label, '检测耗时 P50');
});
test('real success and first token independently take priority over checks', () => {
  const status = probeStatus({ request_sample_state: 'ready', request_success_rate: 99.9, ttft_p50_ms: 3000 });
  assert.deepEqual(statusMetrics(status), [null, '99.9%', '3.0s']);
  assert.equal(statusMetricDetails(status)[1].probe, false);
  status.ttft_p50_ms = null;
  assert.deepEqual(statusMetrics(status), [null, '99.9%', '1.2s']);
  assert.equal(statusMetricDetails(status)[2].probe, true);
});
test('incompatible, stale, disabled and insufficient check metrics stay unknown', () => {
  for (const overrides of [{ probe_metrics_version: undefined }, { probe_metrics_version: 2 }, { probe_enabled: false }, ...['stale', 'none', 'insufficient', 'disabled'].map(probe_sample_state => ({ probe_sample_state }))]) {
    assert.deepEqual(statusMetrics(probeStatus(overrides)), [null, '—', '—']);
  }
  for (const value of [-1, Infinity, NaN, null]) {
    assert.deepEqual(statusMetrics(probeStatus({ probe_success_rate: value, probe_ttft_p50_ms: value, probe_latency_p50_ms: value })), [null, '—', '—']);
  }
  assert.equal(statusMetrics(probeStatus({ probe_success_rate: 101 }))[1], '—');
  assert.deepEqual(statusMetrics(probeStatus({ probe_success_rate: 0, probe_ttft_p50_ms: 0 })), [null, '0.0%', '0.0s']);
});
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
