/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { describe, expect, test } from 'bun:test'

import { parseMultiSelectPaste } from '../src/components/multi-select-values'

describe('多选框批量粘贴', () => {
  test('提交逗号和换行分隔文本中的全部值', () => {
    expect(
      parseMultiSelectPaste('provider/a, provider/b\r\nprovider/c，provider/d')
    ).toEqual(['provider/a', 'provider/b', 'provider/c', 'provider/d'])
  })

  test('单个普通文本继续使用默认粘贴行为', () => {
    expect(parseMultiSelectPaste('provider/a')).toBeNull()
  })
})
