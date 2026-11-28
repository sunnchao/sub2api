import { apiClient } from './client'
import type {
  ActionCaptchaRequestProof,
  CheckinCalendarResponse,
  CheckinExecuteResponse,
  CheckinRecord,
  CheckinStatus,
  ExpiringBalanceLot,
  ExpiringBalanceLotStatus,
  PaginatedResponse
} from '@/types'

export async function getStatus(): Promise<CheckinStatus> {
  const { data } = await apiClient.get<CheckinStatus>('/checkin/status')
  return data
}

export async function checkin(proof?: ActionCaptchaRequestProof): Promise<CheckinExecuteResponse> {
  const { data } = await apiClient.post<CheckinExecuteResponse>('/checkin', proof ?? {})
  return data
}

export async function getCalendar(month?: string): Promise<CheckinCalendarResponse> {
  const { data } = await apiClient.get<CheckinCalendarResponse>('/checkin/calendar', {
    params: month ? { month } : undefined
  })
  return data
}

export async function getRecords(
  page = 1,
  pageSize = 20
): Promise<PaginatedResponse<CheckinRecord>> {
  const { data } = await apiClient.get<PaginatedResponse<CheckinRecord>>('/checkin/records', {
    params: { page, page_size: pageSize }
  })
  return data
}

export async function getLots(
  page = 1,
  pageSize = 20,
  status: 'all' | ExpiringBalanceLotStatus = 'all'
): Promise<PaginatedResponse<ExpiringBalanceLot>> {
  const { data } = await apiClient.get<PaginatedResponse<ExpiringBalanceLot>>('/checkin/lots', {
    params: { page, page_size: pageSize, status }
  })
  return data
}

export const checkinAPI = {
  getStatus,
  checkin,
  getCalendar,
  getRecords,
  getLots
}

export default checkinAPI
