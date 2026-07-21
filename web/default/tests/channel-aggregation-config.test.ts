/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { describe, expect, test } from 'bun:test'

import {
  buildChannelAggregationPrefill,
  compareChannelAggregationConfigs,
} from '../src/features/channels/lib/channel-aggregation-config'
import type { ChannelAggregationPreparedData } from '../src/features/channels/types'

const prepared: ChannelAggregationPreparedData = {
  source_ids: [7, 2],
  sources: [
    { id: 2, name: 'second' },
    { id: 7, name: 'seventh' },
  ],
  source_configs: [
    {
      id: 7,
      name: 'seventh',
      openai_organization: null,
      test_model: 'gpt-4o',
      weight: null,
      group: 'premium,default',
      priority: 1,
      auto_ban: 1,
      tag: 'shared',
      remark: '',
      other: 'v1',
      proxy: 'http://proxy-seven.example.com',
      models: 'gpt-4o-mini,o1',
    },
    {
      id: 2,
      name: 'second',
      openai_organization: null,
      test_model: 'gpt-4o',
      weight: 0,
      group: 'default',
      priority: 1,
      auto_ban: null,
      tag: null,
      remark: '',
      other: 'v1',
      proxy: '',
      models: 'gpt-4o,gpt-4o-mini',
    },
  ],
  type: 1,
  base_url: 'https://api.example.com',
  key: 'key-a\nkey-b',
  key_count: 2,
  snapshot_token: 'snapshot',
}

describe('渠道聚合配置比较', () => {
  test('按来源 ID 和原模型顺序汇总去重，并保留空值候选', () => {
    const comparison = compareChannelAggregationConfigs(prepared)

    expect(comparison.models).toBe('gpt-4o,gpt-4o-mini,o1')
    expect(comparison.common.test_model).toBe('gpt-4o')
    expect(comparison.common.weight).toBe(0)
    expect(comparison.common.auto_ban).toBeUndefined()

    const autoBanConflict = comparison.conflicts.find(
      (conflict) => conflict.field === 'auto_ban'
    )
    expect(
      autoBanConflict?.candidates.map((candidate) => candidate.value)
    ).toEqual([0, 1])

    const tagConflict = comparison.conflicts.find(
      (conflict) => conflict.field === 'tag'
    )
    expect(tagConflict?.candidates.map((candidate) => candidate.value)).toEqual(
      ['', 'shared']
    )
    expect(tagConflict?.candidates[0].sources[0].id).toBe(2)
  })

  test('所有冲突均明确选择后才生成表单预填值', () => {
    const comparison = compareChannelAggregationConfigs(prepared)
    expect(buildChannelAggregationPrefill(comparison, {})).toBeNull()

    const selections = Object.fromEntries(
      comparison.conflicts.map((conflict) => [
        conflict.field,
        conflict.candidates[0].key,
      ])
    )
    const prefill = buildChannelAggregationPrefill(comparison, selections)

    expect(prefill).not.toBeNull()
    expect(prefill?.name).toBe('second')
    expect(prefill?.group).toEqual(['default'])
    expect(prefill?.auto_ban).toBe(0)
    expect(prefill?.proxy).toBe('')
    expect(prefill?.models).toBe('gpt-4o,gpt-4o-mini,o1')
  })

  test('分组按集合语义比较，忽略顺序和重复项', () => {
    const equivalentGroups: ChannelAggregationPreparedData = {
      ...prepared,
      source_configs: prepared.source_configs.map((source, index) => ({
        ...source,
        group: index === 0 ? 'premium,default,premium' : 'default,premium',
      })),
    }

    const comparison = compareChannelAggregationConfigs(equivalentGroups)

    expect(comparison.common.group).toEqual(['default', 'premium'])
    expect(
      comparison.conflicts.some((conflict) => conflict.field === 'group')
    ).toBe(false)
  })
})
