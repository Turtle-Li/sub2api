import test from 'node:test';
import assert from 'node:assert/strict';
import { statusMetrics } from '../help/turtleroute-status-20261108-v4/group-availability.mjs';
test('status module hides stale or insufficient request metrics even if old values are present', () => {
  for (const state of ['none', 'insufficient', 'stale']) assert.deepEqual(statusMetrics({ request_sample_state: state, request_success_rate: 66.7, cache_read_rate: 90, ttft_p50_ms: 2000 }), ['—', '—', '—']);
});
test('status module preserves actual zero and missing latency independently', () => {
  assert.deepEqual(statusMetrics({ request_sample_state: 'ready', request_success_rate: 0, cache_read_rate: 0, ttft_p50_ms: null }), ['0.0%', '0.0%', '—']);
  assert.deepEqual(statusMetrics({ request_sample_state: 'ready', request_success_rate: 99.9, cache_read_rate: 97.2, ttft_p50_ms: 3000 }), ['97.2%', '99.9%', '3.0s']);
});
