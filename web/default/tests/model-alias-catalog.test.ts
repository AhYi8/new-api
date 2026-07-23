/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { describe, expect, test } from 'bun:test'

import {
  findProviderModelsMissingFromCatalog,
  normalizeProviderModelNames,
} from '../src/features/system-settings/models/model-alias-catalog'

describe('模型别名目录选择', () => {
  test('目录成员比较保持大小写敏感', () => {
    expect(
      findProviderModelsMissingFromCatalog(
        ['Provider/Model', 'provider/model'],
        ['Provider/Model'],
        true
      )
    ).toEqual(['provider/model'])
  })

  test('查询结果失效或失败时不产生缺失提醒', () => {
    expect(
      findProviderModelsMissingFromCatalog(['manual/provider-model'], [], false)
    ).toEqual([])
  })

  test('手工名称会被保留并按精确名称去重', () => {
    expect(
      normalizeProviderModelNames([
        ' manual/provider-model ',
        'manual/provider-model',
        'Manual/Provider-Model',
        '',
      ])
    ).toEqual(['manual/provider-model', 'Manual/Provider-Model'])
  })
})
