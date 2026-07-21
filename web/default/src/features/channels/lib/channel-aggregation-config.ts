import type {
  ChannelAggregationPrefill,
  ChannelAggregationPreparedData,
  ChannelAggregationSourceConfig,
} from '../types'
import { parseGroups } from './channel-form'
import {
  formatModelsArray,
  parseModelsString,
} from './model-mapping-validation'

export const CHANNEL_AGGREGATION_CONFIG_FIELDS = [
  'name',
  'openai_organization',
  'test_model',
  'weight',
  'group',
  'priority',
  'auto_ban',
  'tag',
  'remark',
  'other',
  'proxy',
] as const

export type ChannelAggregationConfigField =
  (typeof CHANNEL_AGGREGATION_CONFIG_FIELDS)[number]

type ChannelAggregationConfigValue =
  ChannelAggregationPrefill[ChannelAggregationConfigField]

export interface ChannelAggregationCandidate {
  key: string
  value: ChannelAggregationConfigValue
  sources: Array<{ id: number; name: string }>
}

export interface ChannelAggregationFieldComparison {
  field: ChannelAggregationConfigField
  candidates: ChannelAggregationCandidate[]
}

export interface ChannelAggregationConfigComparison {
  common: Partial<
    Record<ChannelAggregationConfigField, ChannelAggregationConfigValue>
  >
  conflicts: ChannelAggregationFieldComparison[]
  models: string
}

function normalizeSourceConfig(
  source: ChannelAggregationSourceConfig
): Record<ChannelAggregationConfigField, ChannelAggregationConfigValue> {
  return {
    name: source.name || '',
    openai_organization: source.openai_organization || '',
    test_model: source.test_model || '',
    weight: source.weight ?? 0,
    group: [...new Set(parseGroups(source.group || 'default'))].sort(),
    priority: source.priority ?? 0,
    auto_ban: source.auto_ban === 1 ? 1 : 0,
    tag: source.tag || '',
    remark: source.remark || '',
    other: source.other || '',
    proxy: source.proxy || '',
  }
}

function serializeValue(value: ChannelAggregationConfigValue): string {
  return JSON.stringify(value)
}

function getSourceLabel(source: ChannelAggregationSourceConfig) {
  return { id: source.id, name: source.name }
}

export function compareChannelAggregationConfigs(
  prepared: ChannelAggregationPreparedData
): ChannelAggregationConfigComparison {
  const sources = [...prepared.source_configs].sort((a, b) => a.id - b.id)
  const normalizedSources = sources.map((source) => ({
    source,
    values: normalizeSourceConfig(source),
  }))
  const common: ChannelAggregationConfigComparison['common'] = {}
  const conflicts: ChannelAggregationFieldComparison[] = []

  for (const field of CHANNEL_AGGREGATION_CONFIG_FIELDS) {
    const candidatesByKey = new Map<string, ChannelAggregationCandidate>()
    for (const { source, values } of normalizedSources) {
      const value = values[field]
      const key = serializeValue(value)
      const existing = candidatesByKey.get(key)
      if (existing) {
        existing.sources.push(getSourceLabel(source))
      } else {
        candidatesByKey.set(key, {
          key: `${field}-${candidatesByKey.size}`,
          value,
          sources: [getSourceLabel(source)],
        })
      }
    }

    const candidates = [...candidatesByKey.values()]
    if (candidates.length === 1) {
      common[field] = candidates[0].value
    } else {
      conflicts.push({ field, candidates })
    }
  }

  const seenModels = new Set<string>()
  const models: string[] = []
  for (const source of sources) {
    for (const model of parseModelsString(source.models || '')) {
      if (seenModels.has(model)) continue
      seenModels.add(model)
      models.push(model)
    }
  }

  return {
    common,
    conflicts,
    models: formatModelsArray(models),
  }
}

export function buildChannelAggregationPrefill(
  comparison: ChannelAggregationConfigComparison,
  selections: Record<string, string>
): ChannelAggregationPrefill | null {
  const values: Partial<
    Record<ChannelAggregationConfigField, ChannelAggregationConfigValue>
  > = { ...comparison.common }

  for (const conflict of comparison.conflicts) {
    const selectedKey = selections[conflict.field]
    const selected = conflict.candidates.find(
      (candidate) => candidate.key === selectedKey
    )
    if (!selected) return null
    values[conflict.field] = selected.value
  }

  for (const field of CHANNEL_AGGREGATION_CONFIG_FIELDS) {
    if (!(field in values)) return null
  }

  return {
    ...(values as Pick<
      ChannelAggregationPrefill,
      ChannelAggregationConfigField
    >),
    models: comparison.models,
  }
}
