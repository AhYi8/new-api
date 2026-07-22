import type { DifferencesMap, RatioType } from '../types'
import type { ResolutionsMap } from './upstream-ratio-sync-helpers'

export function normalizeLockedModels(value: unknown): string[] {
  if (!Array.isArray(value)) return []
  return [
    ...new Set(
      value
        .filter((model): model is string => typeof model === 'string')
        .map((model) => model.trim())
        .filter(Boolean)
    ),
  ].sort((left, right) => left.localeCompare(right))
}

export function isPriceLockActionDisabled(
  locked: boolean,
  hasUnsavedChanges: boolean,
  pending: boolean,
  lockStateUnavailable = false
): boolean {
  return lockStateUnavailable || pending || (!locked && hasUnsavedChanges)
}

export function applyPricingSyncResult(
  differences: DifferencesMap,
  resolutions: ResolutionsMap,
  appliedModels: readonly string[],
  ignoredLockedModels: readonly string[]
): DifferencesMap {
  const next = Object.fromEntries(
    Object.entries(differences).map(([model, fields]) => [model, { ...fields }])
  ) as DifferencesMap

  appliedModels.forEach((model) => {
    Object.keys(resolutions[model] ?? {}).forEach((ratioType) => {
      if (!next[model]?.[ratioType as RatioType]) return
      delete next[model][ratioType as RatioType]
      if (Object.keys(next[model]).length === 0) delete next[model]
    })
  })
  ignoredLockedModels.forEach((model) => delete next[model])
  return next
}

export function getConfiguredPricingModelNames(
  serializedMaps: readonly string[]
): Set<string> {
  const names = new Set<string>()
  serializedMaps.forEach((raw) => {
    try {
      const parsed = JSON.parse(raw) as unknown
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return
      Object.keys(parsed).forEach((name) => names.add(name))
    } catch {
      // 表单校验会阻止非法 JSON 保存，此处仅避免清理锁时扩大失败范围。
    }
  })
  return names
}
