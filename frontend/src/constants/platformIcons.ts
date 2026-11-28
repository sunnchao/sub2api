/**
 * Single source of truth for whitelisted generic builtin icons selectable by
 * custom platforms (must stay aligned with backend `builtinIconKeys` in
 * `backend/internal/service/custom_platform_service.go`).
 */

export const BUILTIN_PLATFORM_ICON_KEYS = [
  'cloud',
  'cube',
  'bolt',
  'spark',
  'server',
  'globe',
  'brain',
  'leaf'
] as const

export type BuiltinPlatformIconKey = (typeof BUILTIN_PLATFORM_ICON_KEYS)[number]

/** 24x24 monochrome stroke SVG paths shared by PlatformIcon and CustomPlatformModal. */
export const BUILTIN_PLATFORM_ICON_PATHS: Record<BuiltinPlatformIconKey, string> = {
  cloud:
    'M2.25 15a4.5 4.5 0 004.5 4.5H18a3.75 3.75 0 001.332-7.257 3 3 0 00-3.758-3.848 5.25 5.25 0 00-10.233 2.33A4.502 4.502 0 002.25 15z',
  cube: 'M20 7l-8-4-8 4m16 0l-8 4m8-4v10l-8 4m0-10L4 7m8 4v10M4 7v10l8 4',
  bolt: 'M13 10V3L4 14h7v7l9-11h-7z',
  spark:
    'M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9L12 3zM18.5 15.5l.7 1.8 1.8.7-1.8.7-.7 1.8-.7-1.8-1.8-.7 1.8-.7.7-1.8z',
  server:
    'M5.25 14.25h13.5m-13.5 0a3 3 0 01-3-3m3 3a3 3 0 100 6h13.5a3 3 0 100-6m-16.5-3a3 3 0 013-3h13.5a3 3 0 013 3m-19.5 0a4.5 4.5 0 01.9-2.7L5.737 5.1a3.375 3.375 0 012.7-1.35h7.126c1.062 0 2.062.5 2.7 1.35l2.587 3.45a4.5 4.5 0 01.9 2.7',
  globe:
    'M12 21a9.004 9.004 0 008.716-6.747M12 21a9.004 9.004 0 01-8.716-6.747M12 21c2.485 0 4.5-4.03 4.5-9S14.485 3 12 3m0 18c-2.485 0-4.5-4.03-4.5-9S9.515 3 12 3m0 0a8.997 8.997 0 017.843 4.582M12 3a8.997 8.997 0 00-7.843 4.582M3 12h18',
  brain:
    'M12 8.25a1.5 1.5 0 100-3 1.5 1.5 0 000 3zm0 0v3m-3-1.5a1.5 1.5 0 100-3 1.5 1.5 0 000 3zm0 0h6m-3 4.5a1.5 1.5 0 100-3 1.5 1.5 0 000 3z',
  leaf: 'M4 20c8 0 14-4 16-16C12 4 4 8 4 16v4zm0 0c0-4 2-7 5-9'
}
