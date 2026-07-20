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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { isVerificationRequiredError } from '@/lib/secure-verification'

import { aggregateChannels } from '../api'
import { ERROR_MESSAGES } from '../constants'
import {
  channelsQueryKeys,
  transformFormDataToCreatePayload,
  type ChannelFormValues,
} from '../lib'
import type { ChannelAggregationDraft } from '../types'

type UseChannelAggregationFormParams = {
  draft: ChannelAggregationDraft | null
  onSuccess: (channelId: number, deletedCount: number) => void
}

export function useChannelAggregationForm(
  props: UseChannelAggregationFormParams
) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (data: ChannelFormValues) => {
      if (!props.draft) {
        throw new Error(t('Channel aggregation draft is no longer available'))
      }
      const payload = transformFormDataToCreatePayload(data)
      const response = await aggregateChannels({
        source_ids: props.draft.source_ids,
        snapshot_token: props.draft.snapshot_token,
        multi_key_mode: data.multi_key_type || 'random',
        channel: payload.channel,
      })
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Failed to aggregate channels'))
      }
      return response.data
    },
    onSuccess: (result) => {
      queryClient.invalidateQueries({ queryKey: channelsQueryKeys.all })
      toast.success(
        t(
          'Created channel #{{channelId}} and deleted {{count}} source channels',
          {
            channelId: result.channel_id,
            count: result.deleted_count,
          }
        )
      )
      props.onSuccess(result.channel_id, result.deleted_count)
    },
    onError: (error: unknown) => {
      if (isVerificationRequiredError(error)) return
      toast.error(
        error instanceof Error ? error.message : t(ERROR_MESSAGES.CREATE_FAILED)
      )
    },
  })
}
