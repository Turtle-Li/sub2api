import { describe, expect, it } from 'vitest'
import { parseShopAccountDelivery } from '../shopAccountDelivery'

describe('account delivery recognition', () => {
  it('splits a single delivery into account and password', () => {
    expect(parseShopAccountDelivery('buyer@example.invalid----Test!2026')).toEqual({ account: 'buyer@example.invalid', password: 'Test!2026' })
  })
  it('preserves password bytes and additional delimiters', () => {
    expect(parseShopAccountDelivery(' buyer@example.invalid ---- pass----word ')).toEqual({ account: ' buyer@example.invalid ', password: ' pass----word ' })
  })
  it('accepts an editor line ending without including it in the password', () => {
    expect(parseShopAccountDelivery('buyer@example.invalid----Test!2026\r\n')).toEqual({ account: 'buyer@example.invalid', password: 'Test!2026' })
  })
  it.each(['', '----password', 'account----', 'account----  ', '账号：buyer\n密码：test', 'one----two\nthree----four', 'one\rtwo----three'])('leaves ambiguous or incomplete delivery unchanged: %j', content => {
    expect(parseShopAccountDelivery(content)).toBeNull()
  })
})
