import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const dir = dirname(fileURLToPath(import.meta.url))

describe('invoice administration integration surface', () => {
  it('places the dedicated queue beside orders while remaining accessible with checkout disabled', () => {
    const sidebar = readFileSync(resolve(dir, '../../../../components/layout/AppSidebar.vue'), 'utf8')
    const router = readFileSync(resolve(dir, '../../../../router/index.ts'), 'utf8')

    expect(sidebar).toContain("path: '/admin/orders/invoices'")
    expect(sidebar).toContain("label: t('nav.invoiceRequests')")
    expect(router).toContain("path: '/admin/orders/invoices'")
    expect(router).toContain("name: 'AdminInvoiceRequests'")
    expect(router).toMatch(/name: 'AdminInvoiceRequests',[\s\S]*?requiresPayment: false/)
  })
})
