import { apiClient } from '../client'

export interface UnifiedPaymentBindingStatus {
 base_url: string
 app_id: string
 environment: string
 return_url: string
 webhook_url: string
 revision: number
 configured: boolean
 bootstrap_ready: boolean
 runtime_enabled: boolean
 pending_restart: boolean
 saved_at?: string
}
const endpoint = '/admin/settings/unified-payment-binding'
export async function getUnifiedPaymentBinding(): Promise<UnifiedPaymentBindingStatus> {
 const { data } = await apiClient.get<UnifiedPaymentBindingStatus>(endpoint)
 return data
}
export async function bindUnifiedPayment(baseURL: string, bindingCode: string, revision: number, idempotencyKey: string): Promise<UnifiedPaymentBindingStatus> {
 const { data } = await apiClient.post<UnifiedPaymentBindingStatus>(endpoint, { base_url: baseURL, binding_code: bindingCode, revision }, { headers: { 'Idempotency-Key': idempotencyKey } })
 return data
}
export async function useManualUnifiedPayment(revision: number, idempotencyKey: string): Promise<UnifiedPaymentBindingStatus> {
 const { data } = await apiClient.delete<UnifiedPaymentBindingStatus>(endpoint, { data: { revision }, headers: { 'Idempotency-Key': idempotencyKey } })
 return data
}
