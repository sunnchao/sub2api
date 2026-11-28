import { apiClient } from '../client'
import type {
  AdminCheckinRecord,
  AdminCheckinStats,
  CheckinSettings,
  PaginatedResponse
} from '@/types'

export interface AdminCheckinRecordsQuery {
  page?: number
  page_size?: number
  user_id?: number
  search?: string
  start_date?: string
  end_date?: string
  client_ip?: string
}

export async function getConfig(): Promise<CheckinSettings> {
  const { data } = await apiClient.get<CheckinSettings>('/admin/checkin/config')
  return data
}

export async function updateConfig(payload: CheckinSettings): Promise<CheckinSettings> {
  const { data } = await apiClient.put<CheckinSettings>('/admin/checkin/config', payload)
  return data
}

export async function getStats(days = 30): Promise<AdminCheckinStats> {
  const { data } = await apiClient.get<AdminCheckinStats>('/admin/checkin/stats', {
    params: { days }
  })
  return data
}

export async function listRecords(
  params: AdminCheckinRecordsQuery = {}
): Promise<PaginatedResponse<AdminCheckinRecord>> {
  const cleanParams: Record<string, string | number> = {}
  if (params.page != null) cleanParams.page = params.page
  if (params.page_size != null) cleanParams.page_size = params.page_size
  if (params.user_id != null && Number.isInteger(params.user_id) && params.user_id > 0) {
    cleanParams.user_id = params.user_id
  }
  if (params.search) cleanParams.search = params.search
  if (params.start_date) cleanParams.start_date = params.start_date
  if (params.end_date) cleanParams.end_date = params.end_date
  if (params.client_ip) cleanParams.client_ip = params.client_ip

  const { data } = await apiClient.get<PaginatedResponse<AdminCheckinRecord>>(
    '/admin/checkin/records',
    { params: cleanParams }
  )
  return data
}

export const adminCheckinAPI = {
  getConfig,
  updateConfig,
  getStats,
  listRecords
}

export default adminCheckinAPI
