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
import { afterEach, describe, expect, mock, spyOn, test } from 'bun:test'

import { QueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'

import * as channelApi from '../src/features/channels/api'
import {
  channelsQueryKeys,
  handleUpdateChannelGroups,
} from '../src/features/channels/lib/channel-actions'
import {
  normalizeChannelGroups,
  prepareChannelGroupUpdate,
} from '../src/features/channels/lib/channel-utils'

afterEach(() => {
  mock.restore()
})

describe('渠道分组内联更新', () => {
  test('去除空白和重复项并保持 default 优先', () => {
    expect(
      normalizeChannelGroups([' premium ', 'default', 'premium', '', 'admin'])
    ).toEqual(['default', 'admin', 'premium'])
  })

  test('仅顺序和重复项不同不会产生更新', () => {
    const preparation = prepareChannelGroupUpdate('premium,default', [
      'default',
      'premium',
      'premium',
    ])

    expect(preparation.isValid).toBe(true)
    expect(preparation.hasChanges).toBe(false)
    expect(preparation.value).toBe('default,premium')
  })

  test('删除最后一个分组会被判定为无效', () => {
    const preparation = prepareChannelGroupUpdate('default', [' ', ''])

    expect(preparation.isValid).toBe(false)
    expect(preparation.hasChanges).toBe(true)
    expect(preparation.value).toBe('')
  })

  test('有效变更按规范化顺序序列化', () => {
    const preparation = prepareChannelGroupUpdate('default', [
      'premium',
      'default',
      'admin',
    ])

    expect(preparation.isValid).toBe(true)
    expect(preparation.hasChanges).toBe(true)
    expect(preparation.groups).toEqual(['default', 'admin', 'premium'])
    expect(preparation.value).toBe('default,admin,premium')
  })

  test('空分组字符串与空草稿保持无效且不产生变化', () => {
    const preparation = prepareChannelGroupUpdate('', [])

    expect(preparation.isValid).toBe(false)
    expect(preparation.hasChanges).toBe(false)
    expect(preparation.groups).toEqual([])
    expect(preparation.value).toBe('')
  })

  test('纯空白与重复的当前分组按集合规范化', () => {
    const whitespace = prepareChannelGroupUpdate('   ', [])
    const duplicated = prepareChannelGroupUpdate('default,default', ['default'])

    expect(whitespace.isValid).toBe(false)
    expect(whitespace.hasChanges).toBe(false)
    expect(duplicated.isValid).toBe(true)
    expect(duplicated.hasChanges).toBe(false)
    expect(duplicated.value).toBe('default')
  })
})

describe('渠道分组更新请求', () => {
  test('更新成功后刷新渠道列表缓存', async () => {
    const queryClient = new QueryClient()
    const updateChannel = spyOn(channelApi, 'updateChannel').mockResolvedValue({
      success: true,
    })
    const invalidateQueries = spyOn(
      queryClient,
      'invalidateQueries'
    ).mockResolvedValue()
    const successToast = spyOn(toast, 'success').mockImplementation(() => '')

    const updated = await handleUpdateChannelGroups(
      12,
      'default,premium',
      queryClient
    )

    expect(updated).toBe(true)
    expect(updateChannel).toHaveBeenCalledWith(12, {
      group: 'default,premium',
    })
    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: channelsQueryKeys.lists(),
    })
    expect(successToast).toHaveBeenCalledTimes(1)
  })

  test('服务端拒绝更新时保留返回消息且不刷新缓存', async () => {
    const queryClient = new QueryClient()
    spyOn(channelApi, 'updateChannel').mockResolvedValue({
      success: false,
      message: '分组不可用',
    })
    const invalidateQueries = spyOn(queryClient, 'invalidateQueries')
    const errorToast = spyOn(toast, 'error').mockImplementation(() => '')

    const updated = await handleUpdateChannelGroups(
      12,
      'default,premium',
      queryClient
    )

    expect(updated).toBe(false)
    expect(errorToast).toHaveBeenCalledWith('分组不可用')
    expect(invalidateQueries).not.toHaveBeenCalled()
  })

  test('请求异常时返回失败', async () => {
    spyOn(channelApi, 'updateChannel').mockRejectedValue(new Error('network'))
    const errorToast = spyOn(toast, 'error').mockImplementation(() => '')

    const updated = await handleUpdateChannelGroups(12, 'default')

    expect(updated).toBe(false)
    expect(errorToast).toHaveBeenCalledTimes(1)
  })

  test('服务端已更新时缓存刷新异常不反转保存结果', async () => {
    const queryClient = new QueryClient()
    spyOn(channelApi, 'updateChannel').mockResolvedValue({ success: true })
    spyOn(queryClient, 'invalidateQueries').mockRejectedValue(
      new Error('refresh failed')
    )
    spyOn(toast, 'success').mockImplementation(() => '')

    const updated = await handleUpdateChannelGroups(
      12,
      'default,premium',
      queryClient
    )

    expect(updated).toBe(true)
  })
})
