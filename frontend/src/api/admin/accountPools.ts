/**
 * Admin account pool API.
 *
 * Pools are display/management containers only: groups, proxies and
 * scheduling stay bound to each member account. Pool-level edits go through
 * the account bulk endpoints with `filters.pool` / member ids.
 */

import { apiClient } from '../client'

export interface AccountPoolCodexQuota {
  accounts: number
  avg_5h_used_percent?: number | null
  avg_7d_used_percent?: number | null
  exhausted: number
}

export interface AccountPoolStats {
  total: number
  normal: number
  rate_limited: number
  temp_unschedulable: number
  unschedulable: number
  error: number
  inactive: number
  expired: number
  codex?: AccountPoolCodexQuota | null
}

export interface AccountPoolGrokFreeQuota {
  accounts: number
  used_tokens: number
  limit_tokens: number
  near_limit: number
}

export interface AccountPoolUsage {
  window_hours: number
  requests: number
  tokens: number
  cost: number
  grok_free?: AccountPoolGrokFreeQuota | null
  updated_at: string
}

export interface AccountPool {
  id: number
  name: string
  platform: string
  notes?: string | null
  stats: AccountPoolStats
  usage?: AccountPoolUsage | null
  created_at: string
  updated_at: string
}

export async function list(platform?: string): Promise<AccountPool[]> {
  const { data } = await apiClient.get<AccountPool[]>('/admin/account-pools', {
    params: platform ? { platform } : undefined
  })
  return data ?? []
}

export async function get(id: number): Promise<AccountPool> {
  const { data } = await apiClient.get<AccountPool>(`/admin/account-pools/${id}`)
  return data
}

export async function create(payload: { name: string; platform: string; notes?: string | null }): Promise<AccountPool> {
  const { data } = await apiClient.post<AccountPool>('/admin/account-pools', payload)
  return data
}

export async function update(id: number, payload: { name?: string; notes?: string | null }): Promise<AccountPool> {
  const { data } = await apiClient.put<AccountPool>(`/admin/account-pools/${id}`, payload)
  return data
}

export async function deletePool(id: number): Promise<void> {
  await apiClient.delete(`/admin/account-pools/${id}`)
}

export async function addMembers(id: number, accountIds: number[]): Promise<{ affected: number }> {
  const { data } = await apiClient.post<{ affected: number }>(`/admin/account-pools/${id}/members`, {
    account_ids: accountIds
  })
  return data
}

export async function removeMembers(accountIds: number[]): Promise<{ affected: number }> {
  const { data } = await apiClient.post<{ affected: number }>('/admin/account-pools/members/remove', {
    account_ids: accountIds
  })
  return data
}

export async function listAccountIds(id: number, status?: string): Promise<number[]> {
  const { data } = await apiClient.get<{ account_ids: number[] }>(`/admin/account-pools/${id}/account-ids`, {
    params: status ? { status } : undefined
  })
  return data?.account_ids ?? []
}

export const accountPoolsAPI = {
  list,
  get,
  create,
  update,
  delete: deletePool,
  addMembers,
  removeMembers,
  listAccountIds
}

export default accountPoolsAPI
