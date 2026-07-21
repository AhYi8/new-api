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
import { useQuery } from '@tanstack/react-query'
import { Combine, Loader2, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'

import {
  getChannelAggregationGroups,
  prepareChannelAggregation,
} from '../../api'
import { getChannelTypeLabel } from '../../lib'
import type { ChannelAggregationGroup } from '../../types'
import { useChannels } from '../channels-provider'

type ChannelAggregationDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

const MAX_AGGREGATION_SOURCES = 1000

function getDisabledReason(reason: string | undefined) {
  switch (reason) {
    case 'codex_not_supported':
      return 'Codex channels do not support multi-key aggregation'
    case 'vertex_api_key_not_supported':
      return 'Vertex AI API Key channels do not support multi-key aggregation'
    case 'not_enough_channels':
      return 'At least two compatible channels are required'
    default:
      return 'This channel cannot be aggregated'
  }
}

function getGroupKey(group: ChannelAggregationGroup) {
  return `${group.type}\u0000${group.base_url}`
}

export function ChannelAggregationDialog(props: ChannelAggregationDialogProps) {
  const { t } = useTranslation()
  const { setOpen, setAggregationPreparation } = useChannels()
  const [selectionByGroup, setSelectionByGroup] = useState<
    Record<string, number[]>
  >({})
  const [preparingGroupKey, setPreparingGroupKey] = useState('')
  const groupsQuery = useQuery({
    queryKey: ['channel-aggregation-groups'],
    queryFn: getChannelAggregationGroups,
    enabled: props.open,
    staleTime: 0,
    refetchOnMount: 'always',
  })
  const {
    open: verificationOpen,
    methods: verificationMethods,
    state: verificationState,
    executeVerification,
    withVerification,
    cancel: cancelVerification,
    setCode: setVerificationCode,
    switchMethod: switchVerificationMethod,
  } = useSecureVerification()

  const groups = groupsQuery.data?.data?.groups || []

  const handleOpenChange = (open: boolean) => {
    if (!open) {
      setSelectionByGroup({})
      setPreparingGroupKey('')
    }
    props.onOpenChange(open)
  }

  const setChannelSelected = (
    groupKey: string,
    channelId: number,
    selected: boolean
  ) => {
    setSelectionByGroup((current) => {
      const ids = current[groupKey] || []
      const nextIds = selected
        ? [...new Set([...ids, channelId])]
        : ids.filter((id) => id !== channelId)
      return { ...current, [groupKey]: nextIds }
    })
  }

  const setAllSelected = (
    group: ChannelAggregationGroup,
    selected: boolean
  ) => {
    const groupKey = getGroupKey(group)
    const eligibleIds = group.channels
      .filter((channel) => channel.eligible)
      .slice(0, MAX_AGGREGATION_SOURCES)
      .map((channel) => channel.id)
    setSelectionByGroup((current) => ({
      ...current,
      [groupKey]: selected ? eligibleIds : [],
    }))
  }

  const handleApply = async (group: ChannelAggregationGroup) => {
    const groupKey = getGroupKey(group)
    const sourceIds = selectionByGroup[groupKey] || []
    if (sourceIds.length < 2) {
      toast.error(t('Select at least two channels in this group'))
      return
    }

    setPreparingGroupKey(groupKey)
    try {
      await withVerification(
        async () => {
          const response = await prepareChannelAggregation(sourceIds)
          if (!response.success || !response.data) {
            throw new Error(
              response.message || t('Failed to prepare channel aggregation')
            )
          }
          setAggregationPreparation(response.data)
          handleOpenChange(false)
          setOpen('aggregate-channel-config')
        },
        {
          preferredMethod: 'passkey',
          title: t('Verify to aggregate channel keys'),
          description: t(
            'Use Passkey or 2FA to confirm your identity before loading the selected channel keys.'
          ),
        }
      )
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t('Failed to prepare channel aggregation')
      )
    } finally {
      setPreparingGroupKey('')
    }
  }

  let content = null
  if (groupsQuery.isLoading) {
    content = (
      <div className='text-muted-foreground flex min-h-48 items-center justify-center gap-2 text-sm'>
        <Loader2 className='size-4 animate-spin' />
        {t('Detecting channel groups...')}
      </div>
    )
  } else if (!groupsQuery.data?.success || groupsQuery.isError) {
    content = (
      <div className='flex min-h-48 flex-col items-center justify-center gap-3 text-center'>
        <p className='text-sm'>{t('Failed to load channel groups')}</p>
        <Button
          variant='outline'
          size='sm'
          onClick={() => groupsQuery.refetch()}
        >
          <RefreshCw className='size-4' />
          {t('Retry')}
        </Button>
      </div>
    )
  } else if (groups.length === 0) {
    content = (
      <div className='text-muted-foreground flex min-h-48 items-center justify-center text-sm'>
        {t('No channels available for aggregation')}
      </div>
    )
  } else {
    content = (
      <div className='space-y-3'>
        {groups.map((group) => {
          const groupKey = getGroupKey(group)
          const selectedIds = selectionByGroup[groupKey] || []
          const selectedIdSet = new Set(selectedIds)
          const eligibleChannels = group.channels.filter(
            (channel) => channel.eligible
          )
          const selectableChannels = eligibleChannels.slice(
            0,
            MAX_AGGREGATION_SOURCES
          )
          const allSelected =
            selectableChannels.length > 0 &&
            selectableChannels.every((channel) => selectedIdSet.has(channel.id))
          const isPreparing = preparingGroupKey === groupKey

          return (
            <section key={groupKey} className='rounded-md border'>
              <div className='bg-muted/30 flex flex-col gap-3 border-b px-4 py-3 sm:flex-row sm:items-center sm:justify-between'>
                <div className='min-w-0'>
                  <div className='flex flex-wrap items-center gap-2'>
                    <span className='font-medium'>
                      {t(getChannelTypeLabel(group.type))}
                    </span>
                    <Badge variant='outline'>
                      {t('{{count}} channels', {
                        count: group.channels.length,
                      })}
                    </Badge>
                  </div>
                  <p className='text-muted-foreground mt-1 truncate font-mono text-xs'>
                    {group.base_url || t('Default API address')}
                  </p>
                </div>
                <div className='flex items-center gap-2'>
                  <Button
                    variant='outline'
                    size='sm'
                    disabled={
                      !group.eligible || selectedIds.length < 2 || isPreparing
                    }
                    onClick={() => handleApply(group)}
                  >
                    {isPreparing ? (
                      <Loader2 className='size-4 animate-spin' />
                    ) : (
                      <Combine className='size-4' />
                    )}
                    {t('Apply')}
                  </Button>
                </div>
              </div>

              {!group.eligible && (
                <p className='text-muted-foreground border-b px-4 py-2 text-xs'>
                  {t(getDisabledReason(group.disabled_reason))}
                </p>
              )}

              <div className='divide-y'>
                {eligibleChannels.length > 0 && (
                  <label className='flex min-h-10 cursor-pointer items-center gap-3 px-4 py-2 text-sm'>
                    <Checkbox
                      checked={allSelected}
                      disabled={!group.eligible}
                      onCheckedChange={(checked) =>
                        setAllSelected(group, checked === true)
                      }
                    />
                    <span className='font-medium'>
                      {t('Select all compatible channels')}
                    </span>
                    <span className='text-muted-foreground ml-auto text-xs'>
                      {selectedIds.length}/{eligibleChannels.length}
                    </span>
                  </label>
                )}
                {group.channels.map((channel) => (
                  <label
                    key={channel.id}
                    className='flex min-h-12 items-center gap-3 px-4 py-2 text-sm'
                  >
                    <Checkbox
                      checked={selectedIdSet.has(channel.id)}
                      disabled={
                        !group.eligible ||
                        !channel.eligible ||
                        (selectedIds.length >= MAX_AGGREGATION_SOURCES &&
                          !selectedIdSet.has(channel.id))
                      }
                      onCheckedChange={(checked) =>
                        setChannelSelected(
                          groupKey,
                          channel.id,
                          checked === true
                        )
                      }
                    />
                    <span className='min-w-0 flex-1 truncate'>
                      #{channel.id} {channel.name}
                    </span>
                    <span className='text-muted-foreground shrink-0 text-xs'>
                      {t('{{count}} keys', { count: channel.key_count })}
                    </span>
                    {!channel.eligible && (
                      <Badge variant='secondary' className='max-w-52 truncate'>
                        {t(getDisabledReason(channel.disabled_reason))}
                      </Badge>
                    )}
                  </label>
                ))}
              </div>
            </section>
          )
        })}
      </div>
    )
  }

  return (
    <>
      <Dialog
        open={props.open}
        onOpenChange={handleOpenChange}
        title={t('Aggregate Channels')}
        description={t(
          'Channels are grouped by type and API address. Select channels within one group to combine their keys.'
        )}
        contentClassName='sm:max-w-3xl'
        contentHeight='min(65vh, 42rem)'
        showCloseButton
      >
        {content}
      </Dialog>

      <SecureVerificationDialog
        open={verificationOpen}
        onOpenChange={(open) => {
          if (!open) cancelVerification()
        }}
        methods={verificationMethods}
        state={verificationState}
        onVerify={async (method, code) => {
          await executeVerification(method, code)
        }}
        onCancel={cancelVerification}
        onCodeChange={setVerificationCode}
        onMethodChange={switchVerificationMethod}
      />
    </>
  )
}
