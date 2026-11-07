import test from 'node:test';
import assert from 'node:assert/strict';
import { statusMetrics } from '../help/turtleroute-status-20261108-v5/group-availability.mjs';
test('status module hides stale or insufficient request metrics even if old values are present', () => {
  for (const state of ['none', 'insufficient', 'stale']) assert.deepEqual(statusMetrics({ request_sample_state: state, request_success_rate: 66.7, cache_read_rate: 90, ttft_p50_ms: 2000 }), [null, '—', '—']);
});
test('status module preserves actual group success zero and missing latency independently', () => {
  assert.deepEqual(statusMetrics({ request_sample_state: 'ready', request_success_rate: 0, cache_read_rate: 0, ttft_p50_ms: null }), [null, '0.0%', '—']);
  assert.deepEqual(statusMetrics({ cache_sample_state: 'ready', cache_scope: 'shared_account_pool', request_sample_state: 'ready', request_success_rate: 99.9, cache_read_rate: 97.2, ttft_p50_ms: 3000 }), ['97.2%', '99.9%', '3.0s']);
});
test('pool cache can be ready while the cheap check group has no real requests', () => {
  assert.deepEqual(statusMetrics({ cache_sample_state: 'ready', cache_scope: 'shared_account_pool', request_sample_state: 'none', cache_read_rate: 96.3 }), ['96.3%', '—', '—']);
});
test('unpublished, below-standard or invalid cache never becomes a public metric', () => {
  for (const cache_read_rate of [0, 52, 89.99, 101, NaN, Infinity, null]) {
    assert.equal(statusMetrics({ cache_sample_state: 'ready', cache_scope: 'shared_account_pool', cache_read_rate })[0], null);
  }
  for (const cache_sample_state of ['none', 'insufficient', 'below_threshold', undefined]) {
    assert.equal(statusMetrics({ cache_sample_state, cache_scope: 'shared_account_pool', cache_read_rate: 99 })[0], null);
  }
  assert.equal(statusMetrics({ cache_sample_state: 'ready', cache_read_rate: 99 })[0], null);
  assert.equal(statusMetrics({ cache_sample_state: 'ready', cache_scope: 'shared_account_pool', cache_read_rate: 90 })[0], '90.0%');
});
