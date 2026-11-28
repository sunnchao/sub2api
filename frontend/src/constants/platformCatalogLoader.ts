import { fetchPlatformCatalog } from '@/api/platformCatalog'
import { setPlatformCatalog, type PlatformCatalog } from './platformCatalog'

/**
 * 启动时拉取公开平台目录并安装；失败时静默保留内置清单作为兜底。
 *
 * 单独成模块，避免 `platformCatalog.ts` 在导入时把 `api/client`（以及 i18n）
 * 带进所有仅需要平台常量的组件与测试。
 */
export async function loadPlatformCatalog(): Promise<PlatformCatalog | null> {
  try {
    const catalog = await fetchPlatformCatalog()
    if (catalog && Array.isArray(catalog.platforms)) {
      setPlatformCatalog(catalog)
      return catalog
    }
  } catch {
    // 平台目录是增强项：网络失败时保持内置清单，不影响首屏。
  }
  return null
}
