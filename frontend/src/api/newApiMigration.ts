import { apiClient } from './client'
import type { PaginatedResponse } from '@/types'

export type NewApiMigrationDirection = 'from_new_api' | 'to_new_api'
export type NewApiMigrationStatus =
  | 'pending'
  | 'completed'
  | 'failed'
  | 'compensated'
  | 'needs_manual_review'

export interface NewApiMigrationUserInfo {
  enabled: boolean
  migrate_from_enabled: boolean
  migrate_to_enabled: boolean
  site_name: string
  site_url: string
  quota_per_unit: number
  exchange_rate: number
}

export interface NewApiMigrationPreviewResult {
  site_url: string
  site_name: string
  new_api_user_id: number
  new_api_username: string
  new_api_display_name: string
  new_api_email: string
  new_api_role: number
  new_api_status: number
  regular_quota: number
  gift_quota: number
  total_quota: number
  used_quota: number
  quota_per_unit: number
  exchange_rate: number
  raw_usd: number
  estimated_credit: number
  current_balance: number
  estimated_new_balance: number
  already_migrated: boolean
  can_migrate: boolean
  cannot_migrate_reason?: string
}

export interface NewApiMigrationRecord {
  id: number
  direction: NewApiMigrationDirection
  status: NewApiMigrationStatus
  site_url: string
  sub2api_user_id: number
  sub2api_user_email: string
  new_api_user_id: number
  new_api_username: string
  new_api_display_name: string
  new_api_email: string
  new_api_quota_before: number
  new_api_quota_delta: number
  new_api_quota_after: number
  new_api_gift_quota: number
  quota_per_unit: number
  exchange_rate: number
  sub2api_balance_before: number
  sub2api_balance_delta: number
  sub2api_balance_after: number
  redeem_code_id?: number
  error_message?: string
  client_ip?: string
  completed_at?: string
  created_at: string
  updated_at: string
}

export interface NewApiMigrationExecuteResult {
  record: NewApiMigrationRecord
  new_balance: number
}

export async function getInfo(): Promise<NewApiMigrationUserInfo> {
  const { data } = await apiClient.get<NewApiMigrationUserInfo>('/new-api-migration/info')
  return data
}

export async function preview(accessToken: string): Promise<NewApiMigrationPreviewResult> {
  const { data } = await apiClient.post<NewApiMigrationPreviewResult>(
    '/new-api-migration/preview',
    { access_token: accessToken },
    { timeout: 60000 }
  )
  return data
}

export async function execute(
  accessToken: string,
  expectedNewApiUserId?: number
): Promise<NewApiMigrationExecuteResult> {
  const { data } = await apiClient.post<NewApiMigrationExecuteResult>(
    '/new-api-migration/execute',
    {
      access_token: accessToken,
      expected_new_api_user_id: expectedNewApiUserId
    },
    { timeout: 60000 }
  )
  return data
}

export async function listRecords(
  page = 1,
  pageSize = 20
): Promise<PaginatedResponse<NewApiMigrationRecord>> {
  const { data } = await apiClient.get<PaginatedResponse<NewApiMigrationRecord>>(
    '/new-api-migration/records',
    {
      params: { page, page_size: pageSize }
    }
  )
  return data
}

export const newApiMigrationAPI = {
  getInfo,
  preview,
  execute,
  listRecords
}

export default newApiMigrationAPI
