import { apiClient } from '../client'

export interface AttachmentGatewayR2Config {
  enabled: boolean
  endpoint: string
  region: string
  bucket: string
  access_key_id: string
  secret_access_key?: string
  prefix: string
  force_path_style: boolean
  presign_expiry_minutes: number
  secret_configured?: boolean
  configured?: boolean
}

export interface AttachmentGatewayR2TestResponse {
  ok: boolean
  message: string
}

export async function getR2Config(): Promise<AttachmentGatewayR2Config> {
  const { data } = await apiClient.get<AttachmentGatewayR2Config>('/admin/attachment-gateway/r2-config')
  return data
}

export async function updateR2Config(config: AttachmentGatewayR2Config): Promise<AttachmentGatewayR2Config> {
  const { data } = await apiClient.put<AttachmentGatewayR2Config>('/admin/attachment-gateway/r2-config', config)
  return data
}

export async function testR2Connection(config: AttachmentGatewayR2Config): Promise<AttachmentGatewayR2TestResponse> {
  const { data } = await apiClient.post<AttachmentGatewayR2TestResponse>('/admin/attachment-gateway/r2-config/test', config)
  return data
}

export const attachmentGatewayAPI = {
  getR2Config,
  updateR2Config,
  testR2Connection,
}

export default attachmentGatewayAPI
