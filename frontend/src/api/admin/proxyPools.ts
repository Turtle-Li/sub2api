/**
 * Admin proxy pool API.
 *
 * Proxy pools group reusable proxies for automatic account imports. Existing
 * accounts remain pinned to their selected proxy even when pool membership
 * changes later.
 */

import { apiClient } from '../client'
import type { ProxyPool } from '@/types'

export type { ProxyPool, ProxyPoolStats } from '@/types'

export interface CreateProxyPoolRequest {
  name: string
  notes?: string | null
}

export interface UpdateProxyPoolRequest {
  name?: string
  notes?: string | null
}

export async function list(): Promise<ProxyPool[]> {
  const { data } = await apiClient.get<ProxyPool[]>('/admin/proxy-pools')
  return data ?? []
}

export async function get(id: number): Promise<ProxyPool> {
  const { data } = await apiClient.get<ProxyPool>(`/admin/proxy-pools/${id}`)
  return data
}

export async function create(payload: CreateProxyPoolRequest): Promise<ProxyPool> {
  const { data } = await apiClient.post<ProxyPool>('/admin/proxy-pools', payload)
  return data
}

export async function update(id: number, payload: UpdateProxyPoolRequest): Promise<ProxyPool> {
  const { data } = await apiClient.put<ProxyPool>(`/admin/proxy-pools/${id}`, payload)
  return data
}

export async function deletePool(id: number): Promise<void> {
  await apiClient.delete(`/admin/proxy-pools/${id}`)
}

export async function addMembers(id: number, proxyIds: number[]): Promise<{ affected: number }> {
  const { data } = await apiClient.post<{ affected: number }>(`/admin/proxy-pools/${id}/members`, {
    proxy_ids: proxyIds
  })
  return data
}

export async function removeMembers(proxyIds: number[]): Promise<{ affected: number }> {
  const { data } = await apiClient.post<{ affected: number }>('/admin/proxy-pools/members/remove', {
    proxy_ids: proxyIds
  })
  return data
}

export async function listProxyIds(id: number): Promise<number[]> {
  const { data } = await apiClient.get<{ proxy_ids: number[] }>(`/admin/proxy-pools/${id}/proxy-ids`)
  return data?.proxy_ids ?? []
}

export const proxyPoolsAPI = {
  list,
  get,
  create,
  update,
  delete: deletePool,
  addMembers,
  removeMembers,
  listProxyIds
}

export default proxyPoolsAPI
