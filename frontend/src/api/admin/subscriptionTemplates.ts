/**
 * Admin Subscription Templates API endpoints
 * Manages reusable subscription quota templates bound to standard or subscription groups.
 */

import { apiClient } from '../client'
import type { Group } from '@/types'

export interface SubscriptionTemplate {
  id: number
  name: string
  description: string
  group_id: number
  daily_limit_usd: number | null
  weekly_limit_usd: number | null
  monthly_limit_usd: number | null
  hourly_limit_usd: number | null
  hourly_window_hours: number
  default_validity_days: number
  status: 'active' | 'inactive'
  sort_order: number
  created_at: string
  updated_at: string
  group?: Group
}

export interface SubscriptionTemplateFilters {
  group_id?: number
  status?: 'active' | 'inactive' | ''
  platform?: string
  search?: string
}

export interface CreateSubscriptionTemplateRequest {
  name: string
  description?: string
  group_id: number
  daily_limit_usd?: number | null
  weekly_limit_usd?: number | null
  monthly_limit_usd?: number | null
  hourly_limit_usd?: number | null
  hourly_window_hours?: number
  default_validity_days?: number
  status?: 'active' | 'inactive'
  sort_order?: number
}

export interface UpdateSubscriptionTemplateRequest {
  name?: string
  description?: string
  group_id?: number
  daily_limit_usd?: number | null
  weekly_limit_usd?: number | null
  monthly_limit_usd?: number | null
  hourly_limit_usd?: number | null
  hourly_window_hours?: number
  default_validity_days?: number
  status?: 'active' | 'inactive'
  sort_order?: number
}

export async function list(filters?: SubscriptionTemplateFilters): Promise<SubscriptionTemplate[]> {
  const params: Record<string, string | number> = {}
  if (filters?.group_id) params.group_id = filters.group_id
  if (filters?.status) params.status = filters.status
  if (filters?.platform) params.platform = filters.platform
  if (filters?.search?.trim()) params.search = filters.search.trim()
  const { data } = await apiClient.get<SubscriptionTemplate[]>('/admin/subscription-templates', { params })
  return data || []
}

export async function getById(id: number): Promise<SubscriptionTemplate> {
  const { data } = await apiClient.get<SubscriptionTemplate>(`/admin/subscription-templates/${id}`)
  return data
}

export async function create(request: CreateSubscriptionTemplateRequest): Promise<SubscriptionTemplate> {
  const { data } = await apiClient.post<SubscriptionTemplate>('/admin/subscription-templates', request)
  return data
}

export async function update(
  id: number,
  request: UpdateSubscriptionTemplateRequest
): Promise<SubscriptionTemplate> {
  const { data } = await apiClient.put<SubscriptionTemplate>(`/admin/subscription-templates/${id}`, request)
  return data
}

export async function remove(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.delete<{ message: string }>(`/admin/subscription-templates/${id}`)
  return data
}

export const subscriptionTemplatesAPI = {
  list,
  getById,
  create,
  update,
  remove
}

export default subscriptionTemplatesAPI
