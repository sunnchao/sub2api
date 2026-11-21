import { apiClient } from '../client'
import type { PaginatedResponse } from '@/types'
import type {
  NewApiMigrationDirection,
  NewApiMigrationRecord,
  NewApiMigrationStatus
} from '../newApiMigration'

export interface NewApiMigrationAdminConfig {
  enabled: boolean
  base_url: string
  site_name: string
  admin_access_token?: string
  admin_access_token_configured: boolean
  clear_admin_access_token?: boolean
  quota_per_unit: number
  auto_detect_quota_per_unit: boolean
  exchange_rate: number
  migrate_from_enabled: boolean
  migrate_to_enabled: boolean
  allow_insecure_http: boolean
}

export interface NewApiMigrationTestConnectionRequest {
  base_url?: string
  admin_access_token?: string
  allow_insecure_http?: boolean
}

export interface NewApiMigrationTestConnectionResult {
  connected: boolean
  site_url: string
  system_name: string
  version: string
  quota_per_unit: number
  display_in_currency: boolean
  admin_user_id: number
  admin_username: string
  admin_role: number
  is_root: boolean
  user_read_ok: boolean
  warning?: string
}

export interface NewApiMigrationStats {
  total_count: number
  completed_count: number
  pending_count: number
  failed_count: number
  compensated_count: number
  needs_manual_review_count: number
  total_migrated_balance: number
  total_migrated_quota: number
}

export async function getConfig(): Promise<NewApiMigrationAdminConfig> {
  const { data } = await apiClient.get<NewApiMigrationAdminConfig>(
    '/admin/new-api-migration/config'
  )
  return data
}

export async function updateConfig(
  payload: Partial<NewApiMigrationAdminConfig>
): Promise<NewApiMigrationAdminConfig> {
  const { data } = await apiClient.put<NewApiMigrationAdminConfig>(
    '/admin/new-api-migration/config',
    payload
  )
  return data
}

export async function testConnection(
  payload?: NewApiMigrationTestConnectionRequest
): Promise<NewApiMigrationTestConnectionResult> {
  const { data } = await apiClient.post<NewApiMigrationTestConnectionResult>(
    '/admin/new-api-migration/test-connection',
    payload ?? {}
  )
  return data
}

export async function listRecords(params: {
  page?: number
  page_size?: number
  status?: NewApiMigrationStatus | ''
  direction?: NewApiMigrationDirection | ''
  search?: string
}): Promise<PaginatedResponse<NewApiMigrationRecord>> {
  const { data } = await apiClient.get<PaginatedResponse<NewApiMigrationRecord>>(
    '/admin/new-api-migration/records',
    { params }
  )
  return data
}

export async function getStats(): Promise<NewApiMigrationStats> {
  const { data } = await apiClient.get<NewApiMigrationStats>('/admin/new-api-migration/stats')
  return data
}

export const adminNewApiMigrationAPI = {
  getConfig,
  updateConfig,
  testConnection,
  listRecords,
  getStats
}

export default adminNewApiMigrationAPI
