/**
 * Public platform catalog API.
 *
 * Kept separate from the constants module so the catalog store can be used by
 * API modules without a runtime import cycle.
 */

import { apiClient } from './client'
import type { PlatformCatalog } from '@/constants/platformCatalog'

export async function fetchPlatformCatalog(): Promise<PlatformCatalog> {
  const { data } = await apiClient.get<PlatformCatalog>('/platform-catalog')
  return data
}
