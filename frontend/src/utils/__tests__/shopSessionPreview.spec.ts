import { describe, expect, it } from 'vitest'
import { previewShopSession } from '../shopSessionPreview'

const now = Date.parse('2026-11-05T12:00:00Z')
const authKey = 'https://api.openai.com/auth'
const profileKey = 'https://api.openai.com/profile'
const auth = { chatgpt_account_id: 'account-1', chatgpt_user_id: 'user-1', chatgpt_plan_type: 'plus' }
const encode = (value: unknown) => btoa(String.fromCharCode(...new TextEncoder().encode(JSON.stringify(value)))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
function fixture(sessionOverrides: Record<string, unknown> = {}, claimOverrides: Record<string, unknown> = {}, header: unknown = { alg: 'RS256' }) {
  const claims = { exp: 4070908800, [authKey]: auth, [profileKey]: { email: 'target@example.invalid' }, ...claimOverrides }
  return JSON.stringify({ user: { id: 'user-1', name: '测试用户', email: 'target@example.invalid' }, account: { id: 'account-1', planType: 'plus' }, expires: '2099-01-01T00:00:00Z', accessToken: `${encode(header)}.${encode(claims)}.c3ludGhldGlj`, ...sessionOverrides })
}

describe('local shop Session preview', () => {
  it('extracts identity and subscription without returning raw credentials', () => {
    const raw = ` ${fixture()}\n`
    const result = previewShopSession(raw, now)!
    expect(result.accountName).toBe('测试用户')
    expect(result.check).toEqual({ account: 'target@example.invalid', plan_type: 'plus', expires_at: '2099-01-01T00:00:00.000Z', warnings: [] })
    expect(JSON.stringify(result)).not.toContain(JSON.parse(raw).accessToken)
    expect(JSON.stringify(result)).not.toContain('accessToken')
  })

  it('uses token plan when the Session does not provide it and decodes UTF-8', () => {
    const result = previewShopSession(fixture({ account: { id: 'account-1' }, user: { id: 'user-1', email: '用户@example.invalid' } }, { [profileKey]: { email: '用户@example.invalid' } }), now)!
    expect(result.check?.account).toBe('用户@example.invalid')
    expect(result.check?.plan_type).toBe('plus')
    expect(result.check?.warnings).toEqual([])
  })

  it.each([
    { expires: '2098-01-01T00:00:00Z', exp: 4070908800, expected: '2098-01-01T00:00:00.000Z' },
    { expires: '2099-01-01T00:00:00Z', exp: 4039372800, expected: '2098-01-01T00:00:00.000Z' },
  ])('shows the earliest credential expiry: $expected', ({ expires, exp, expected }) => {
    expect(previewShopSession(fixture({ expires }, { exp }), now)?.check?.expires_at).toBe(expected)
  })

  it.each([
    { [profileKey]: { email: 'other@example.invalid' } },
    { [authKey]: { ...auth, chatgpt_user_id: 'other-user' } },
    { [authKey]: { ...auth, chatgpt_account_id: 'other-account' } },
  ])('warns about mismatched account identifiers', claims => {
    expect(previewShopSession(fixture({}, claims), now)?.check?.warnings.join('')).toContain('账号信息不一致')
  })

  it('reports subscription differences without inventing eligibility', () => {
    const result = previewShopSession(fixture({}, { [authKey]: { ...auth, chatgpt_plan_type: 'free' } }), now)!
    expect(result.check?.plan_type).toBe('plus')
    expect(result.check?.warnings.join('')).toContain('订阅信息不同')
    const unknown = previewShopSession(fixture({ account: {} }, { [authKey]: {} }), now)!
    expect(unknown.check?.plan_type).toBe('unknown')
    expect(unknown.check?.warnings.join('')).toContain('未识别到订阅类型')
  })

  it.each(['{', 'null', '[]', '"not a Session"'])('handles malformed or non-object JSON without echoing it: %s', content => {
    const result = previewShopSession(content, now)!
    expect(result.check).toBeNull()
    expect(result.error).toContain('JSON')
    expect(result.accountName).toBe('')
  })

  it('warns when the pasted object lacks an account and token', () => {
    const result = previewShopSession('{}', now)!
    expect(result.check?.account).toBe('')
    expect(result.check?.warnings.join('')).toContain('账号邮箱')
    expect(result.check?.warnings.join('')).toContain('accessToken')
  })

  it.each(['not-a-jwt', 'e30.W10.c3ludGhldGlj', 'eyJhbGciOiJSUzI1NiJ9.e30.!'])('warns about malformed JWT data without throwing', accessToken => {
    expect(previewShopSession(fixture({ accessToken }), now)?.check?.warnings.join('')).toContain('令牌格式')
  })

  it('does not accept an unsigned token as well-formed', () => {
    expect(previewShopSession(fixture({}, {}, { alg: 'none' }), now)?.check?.warnings.join('')).toContain('令牌格式')
  })

  it.each([0, -1, 4070908800000, '4070908800'])('warns about an invalid JWT expiry: %s', exp => {
    expect(previewShopSession(fixture({}, { exp }), now)?.check?.warnings.join('')).toContain('令牌有效期格式')
  })

  it('identifies expired and not-yet-valid credentials separately from subscription type', () => {
    const expired = previewShopSession(fixture({ expires: '2026-11-01T00:00:00Z' }), now)!
    expect(expired.check?.warnings.join('')).toContain('已过期')
    expect(expired.check?.plan_type).toBe('plus')
    expect(previewShopSession(fixture({}, { nbf: now / 1000 + 120 }), now)?.check?.warnings.join('')).toContain('尚未生效')
  })

  it('reports malformed Session dates and enforces the raw UTF-8 size limit', () => {
    expect(previewShopSession(fixture({ expires: 'not-a-date' }), now)?.check?.warnings.join('')).toContain('Session 有效期格式')
    expect(previewShopSession(`{"extra":"${'中'.repeat(90000)}"}`, now)?.error).toContain('内容过长')
    expect(previewShopSession(' '.repeat(262145) + '{}', now)?.error).toContain('内容过长')
  })

  it('removes all hints when the editor is cleared', () => {
    expect(previewShopSession(' \n\t', now)).toBeNull()
  })
})
