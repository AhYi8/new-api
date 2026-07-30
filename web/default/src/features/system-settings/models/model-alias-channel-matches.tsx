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
import { Delete02Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table'
import { StatusBadge } from '@/components/status-badge'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'

import {
  getModelAliasGroupChannels,
  removeModelAliasChannelModel,
} from '../api'
import type {
  ModelAliasChannelMatch,
  ModelAliasChannelMatchedModel,
  ModelAliasChannelMatchSource,
  RemoveModelAliasChannelModelRequest,
} from '../types'

type ModelAliasChannelMatchesProps = {
  alias: string
}

type MatchRow = {
  channel: ModelAliasChannelMatch
  model: ModelAliasChannelMatchedModel
}

type PendingRemoval = MatchRow

const MODEL_ALIAS_QUERY_KEY = ['model-alias-groups'] as const

export function ModelAliasChannelMatches(props: ModelAliasChannelMatchesProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [pendingRemoval, setPendingRemoval] = useState<PendingRemoval | null>(
    null
  )
  const queryKey = [...MODEL_ALIAS_QUERY_KEY, 'channels', props.alias] as const
  const matchesQuery = useQuery({
    queryKey,
    queryFn: () => getModelAliasGroupChannels(props.alias),
    staleTime: 0,
  })
  const rows = useMemo(
    () =>
      (matchesQuery.data?.data.items ?? []).flatMap((channel) =>
        channel.matched_models.map((model) => ({ channel, model }))
      ),
    [matchesQuery.data]
  )
  const invalidMappingCount = useMemo(
    () =>
      (matchesQuery.data?.data.items ?? []).filter(
        (channel) => channel.mapping_error
      ).length,
    [matchesQuery.data]
  )
  const sourceLabels: Record<ModelAliasChannelMatchSource, string> = {
    models: t('Model list'),
    mapping_key: t('Mapping key'),
    mapping_value: t('Mapping value'),
  }

  const removeMutation = useMutation({
    mutationFn: (variables: {
      channelId: number
      request: RemoveModelAliasChannelModelRequest
    }) => removeModelAliasChannelModel(variables.channelId, variables.request),
    onSuccess: async (response) => {
      setPendingRemoval(null)
      toast.success(
        t('Removed model {{model}} from channel {{channel}}', {
          model: response.data.requested_model,
          channel: response.data.channel_name,
        })
      )
      await queryClient.invalidateQueries({ queryKey: MODEL_ALIAS_QUERY_KEY })
    },
    onError: async (error: Error) => {
      setPendingRemoval(null)
      toast.error(error.message)
      await queryClient.invalidateQueries({ queryKey })
    },
  })

  const removeModel = (row: MatchRow, allowCascade: boolean) => {
    removeMutation.mutate({
      channelId: row.channel.channel_id,
      request: {
        alias: props.alias,
        model_name: row.model.name,
        revision: row.channel.revision,
        allow_cascade: allowCascade,
      },
    })
  }

  const handleRemove = (row: MatchRow) => {
    if (row.channel.mapping_error || removeMutation.isPending) return
    if (row.model.removal_plan.requires_confirm) {
      setPendingRemoval(row)
      return
    }
    removeModel(row, false)
  }

  const channelStatus = (status: number) => {
    if (status === 1) {
      return { label: t('Enabled'), variant: 'success' as const }
    }
    if (status === 3) {
      return { label: t('Auto Disabled'), variant: 'warning' as const }
    }
    return { label: t('Disabled'), variant: 'danger' as const }
  }

  const columns = [
    {
      id: 'channel',
      header: t('Channel'),
      className: 'min-w-52',
      cell: (row: MatchRow) => {
        const status = channelStatus(row.channel.channel_status)
        return (
          <div className='flex min-w-0 flex-col gap-1'>
            <div className='flex min-w-0 items-center gap-2'>
              <div className='truncate font-medium'>
                {row.channel.channel_name}
              </div>
              <StatusBadge
                label={status.label}
                variant={status.variant}
                size='sm'
                copyable={false}
              />
            </div>
            <div className='text-muted-foreground text-xs'>
              #{row.channel.channel_id}
            </div>
            {row.channel.mapping_error ? (
              <Badge variant='destructive'>{t('Invalid model mapping')}</Badge>
            ) : null}
          </div>
        )
      },
    },
    {
      id: 'model',
      header: t('Matched model name'),
      className: 'min-w-64',
      cell: (row: MatchRow) => (
        <code className='bg-muted rounded px-1.5 py-0.5 text-xs'>
          {row.model.name}
        </code>
      ),
    },
    {
      id: 'sources',
      header: t('Matched in'),
      className: 'min-w-64',
      cell: (row: MatchRow) => (
        <div className='flex flex-wrap gap-1'>
          {row.model.sources.map((source) => (
            <Badge key={source} variant='outline'>
              {sourceLabels[source]}
            </Badge>
          ))}
        </div>
      ),
    },
    {
      id: 'actions',
      header: t('Actions'),
      className: 'w-20',
      cell: (row: MatchRow) => {
        const disabled =
          Boolean(row.channel.mapping_error) || removeMutation.isPending
        const label = row.channel.mapping_error
          ? t('Fix the invalid model mapping before removing models')
          : t('Remove model from this channel')
        return (
          <Button
            variant='ghost'
            size='icon-sm'
            aria-label={label}
            title={label}
            disabled={disabled}
            onClick={() => handleRemove(row)}
          >
            <HugeiconsIcon icon={Delete02Icon} strokeWidth={2} />
          </Button>
        )
      },
    },
  ]

  if (matchesQuery.isLoading) {
    return <Skeleton className='h-48 w-full' />
  }

  if (matchesQuery.isError) {
    return (
      <Alert variant='destructive'>
        <AlertTitle>{t('Failed to load matching channel models')}</AlertTitle>
        <AlertDescription className='flex items-center justify-between gap-3'>
          <span>{t('Try again to load matching channel models.')}</span>
          <Button
            variant='outline'
            size='sm'
            onClick={() => matchesQuery.refetch()}
          >
            {t('Retry')}
          </Button>
        </AlertDescription>
      </Alert>
    )
  }

  const removalPlan = pendingRemoval?.model.removal_plan
  return (
    <div className='flex min-w-0 flex-col gap-3'>
      {invalidMappingCount > 0 ? (
        <Alert>
          <AlertTitle>{t('Some channel mappings are invalid')}</AlertTitle>
          <AlertDescription>
            {t(
              '{{count}} channels have invalid model mappings. Only direct model-list matches can be shown, and quick removal is disabled for those channels.',
              { count: invalidMappingCount }
            )}
          </AlertDescription>
        </Alert>
      ) : null}
      <StaticDataTable
        columns={columns}
        data={rows}
        getRowKey={(row) => `${row.channel.channel_id}:${row.model.name}`}
        emptyContent={
          <Empty className='min-h-40 p-4'>
            <EmptyHeader>
              <EmptyTitle>{t('No channels found')}</EmptyTitle>
              <EmptyDescription>
                {t(
                  'No channels exactly contain the unified or provider model names.'
                )}
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        }
        emptyClassName='h-48 p-0 whitespace-normal'
        tableClassName='min-w-[820px]'
        className='max-h-[32rem] overflow-auto'
      />
      <ConfirmDialog
        open={pendingRemoval !== null}
        onOpenChange={(open) => {
          if (!open && !removeMutation.isPending) setPendingRemoval(null)
        }}
        title={t('Remove model and related mappings?')}
        desc={
          pendingRemoval && removalPlan ? (
            <div className='flex flex-col gap-3'>
              <p>
                {t(
                  'Removing {{model}} from channel {{channel}} also affects the following entries:',
                  {
                    model: pendingRemoval.model.name,
                    channel: pendingRemoval.channel.channel_name,
                  }
                )}
              </p>
              <div>
                <div className='text-foreground font-medium'>
                  {t('Models removed')}
                </div>
                <div className='mt-1 flex flex-wrap gap-1'>
                  {removalPlan.removed_models.length > 0
                    ? removalPlan.removed_models.map((modelName) => (
                        <code
                          key={modelName}
                          className='bg-muted rounded px-1.5 py-0.5 text-xs'
                        >
                          {modelName}
                        </code>
                      ))
                    : t('None')}
                </div>
              </div>
              <div>
                <div className='text-foreground font-medium'>
                  {t('Mapping keys removed')}
                </div>
                <div className='mt-1 flex flex-wrap gap-1'>
                  {removalPlan.removed_mapping_keys.length > 0
                    ? removalPlan.removed_mapping_keys.map((mappingKey) => (
                        <code
                          key={mappingKey}
                          className='bg-muted rounded px-1.5 py-0.5 text-xs'
                        >
                          {mappingKey}
                        </code>
                      ))
                    : t('None')}
                </div>
              </div>
              <p>
                {t(
                  'Only this channel is changed. The model alias group and other channels remain unchanged.'
                )}
              </p>
            </div>
          ) : (
            ''
          )
        }
        confirmText={t('Remove')}
        destructive
        isLoading={removeMutation.isPending}
        handleConfirm={() =>
          pendingRemoval && removeModel(pendingRemoval, true)
        }
      />
    </div>
  )
}
