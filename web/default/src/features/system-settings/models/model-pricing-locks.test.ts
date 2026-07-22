import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import type { DifferencesMap } from '../types'
import {
  applyPricingSyncResult,
  getConfiguredPricingModelNames,
  isPriceLockActionDisabled,
  normalizeLockedModels,
} from './model-pricing-locks'

describe('模型价格锁辅助逻辑', () => {
  test('规范化锁定模型集合', () => {
    assert.deepEqual(normalizeLockedModels([' b ', 'a', 'a', '', 1]), [
      'a',
      'b',
    ])
    assert.deepEqual(normalizeLockedModels(null), [])
  })

  test('有未保存修改时只禁止新增锁定', () => {
    assert.equal(isPriceLockActionDisabled(false, true, false), true)
    assert.equal(isPriceLockActionDisabled(true, true, false), false)
    assert.equal(isPriceLockActionDisabled(true, false, true), true)
    assert.equal(isPriceLockActionDisabled(true, false, false, true), true)
  })

  test('按同步响应清理已应用字段和并发锁定模型', () => {
    const differences: DifferencesMap = {
      applied: {
        model_ratio: { current: 1, upstreams: {}, confidence: {} },
        cache_ratio: { current: 1, upstreams: {}, confidence: {} },
      },
      locked: {
        model_price: { current: 1, upstreams: {}, confidence: {} },
      },
      untouched: {
        model_price: { current: 1, upstreams: {}, confidence: {} },
      },
    }

    const result = applyPricingSyncResult(
      differences,
      { applied: { model_ratio: 2 } },
      ['applied'],
      ['locked']
    )

    assert.ok(result.applied.cache_ratio)
    assert.equal(result.applied.model_ratio, undefined)
    assert.equal(result.locked, undefined)
    assert.ok(result.untouched)
    assert.ok(differences.applied.model_ratio)
  })

  test('汇总全部计费配置中的模型名', () => {
    assert.deepEqual(
      [...getConfiguredPricingModelNames(['{"a":1}', '{"b":"expr"}'])].sort(),
      ['a', 'b']
    )
  })
})
