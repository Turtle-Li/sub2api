import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({
  post: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: { post }
}))

import { grantResetCards, grantResetCardsToSubscription } from '@/api/admin/subscriptions'
import { useResetCard } from '@/api/subscriptions'

describe('subscription reset card APIs', () => {
  beforeEach(() => {
    post.mockReset()
  })

  it('grants cards to selected subscription groups', async () => {
    const request = {
      group_ids: [2, 5],
      count: 3,
      expires_at: '2026-08-01T00:00:00.000Z'
    }
    const response = {
      group_ids: [2, 5],
      recipient_count: 4,
      card_count: 12,
      recipients_by_group: { '2': 1, '5': 3 },
      expires_at: request.expires_at
    }
    post.mockResolvedValue({ data: response })

    await expect(grantResetCards(request, 'grant-operation-1')).resolves.toEqual(response)
    expect(post).toHaveBeenCalledWith('/admin/subscriptions/reset-cards/grant', request, {
      headers: { 'Idempotency-Key': 'grant-operation-1' }
    })
  })

  it('uses one card on the current user subscription', async () => {
    const response = { id: 9, reset_card_count: 1 }
    post.mockResolvedValue({ data: response })

    await expect(useResetCard(9, 'use-operation-1')).resolves.toEqual(response)
    expect(post).toHaveBeenCalledWith('/subscriptions/9/use-reset-card', undefined, {
      headers: { 'Idempotency-Key': 'use-operation-1' }
    })
  })

  it('grants cards to one selected subscription', async () => {
    const request = {
      count: 2,
      expires_at: '2026-08-01T00:00:00.000Z'
    }
    const response = {
      subscription_id: 9,
      user_id: 3,
      group_id: 5,
      card_count: 2,
      expires_at: request.expires_at
    }
    post.mockResolvedValue({ data: response })

    await expect(
      grantResetCardsToSubscription(9, request, 'grant-user-operation-1')
    ).resolves.toEqual(response)
    expect(post).toHaveBeenCalledWith(
      '/admin/subscriptions/9/reset-cards/grant',
      request,
      { headers: { 'Idempotency-Key': 'grant-user-operation-1' } }
    )
  })
})
