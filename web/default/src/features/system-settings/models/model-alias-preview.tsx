/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { StatusBadge } from '@/components/status-badge'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'

import type {
  ModelAliasChannelPreview,
  ModelAliasPreview as ModelAliasPreviewData,
  ModelAliasPreviewStatus,
} from '../types'

type ModelAliasPreviewProps = {
  preview: ModelAliasPreviewData
}

const statusVariant: Record<
  ModelAliasPreviewStatus,
  'default' | 'secondary' | 'destructive' | 'outline'
> = {
  new: 'default',
  unchanged: 'secondary',
  updated: 'default',
  conflict: 'destructive',
  multiple_matches: 'destructive',
  unmatched: 'outline',
}

const previewStatuses: ModelAliasPreviewStatus[] = [
  'new',
  'unchanged',
  'updated',
  'conflict',
  'multiple_matches',
  'unmatched',
]

export function ModelAliasPreview({ preview }: ModelAliasPreviewProps) {
  const { t } = useTranslation()
  const [selectedStatus, setSelectedStatus] =
    useState<ModelAliasPreviewStatus | null>(null)
  const filteredItems = useMemo(
    () =>
      selectedStatus
        ? preview.items.filter((item) => item.status === selectedStatus)
        : preview.items,
    [preview.items, selectedStatus]
  )
  const statusLabels: Record<ModelAliasPreviewStatus, string> = {
    new: t('Will add mapping'),
    unchanged: t('Already consistent'),
    updated: t('Will update mapping'),
    conflict: t('Conflict'),
    multiple_matches: t('Multiple matches'),
    unmatched: t('No match'),
  }
  const reasonLabels: Record<string, string> = {
    invalid_mapping: t('The channel model mapping is invalid JSON'),
    no_matching_model: t('No provider model name matched'),
    multiple_matching_models: t('More than one provider model name matched'),
    alias_already_in_models: t(
      'The unified name already exists as a direct channel model'
    ),
    empty_mapping_target: t('The existing mapping target is empty'),
    mapping_target_conflict: t(
      'The existing mapping target does not belong to this alias group'
    ),
  }
  const enabledChannelStatus = {
    label: t('Enabled'),
    variant: 'success' as const,
  }
  const autoDisabledChannelStatus = {
    label: t('Auto Disabled'),
    variant: 'warning' as const,
  }
  const disabledChannelStatus = {
    label: t('Disabled'),
    variant: 'danger' as const,
  }

  const channelStatus = (status: number) => {
    if (status === 1) {
      return enabledChannelStatus
    }
    if (status === 3) {
      return autoDisabledChannelStatus
    }
    return disabledChannelStatus
  }

  const columns = [
    {
      id: 'channel',
      header: t('Channel'),
      className: 'min-w-44',
      cell: (item: ModelAliasChannelPreview) => {
        const status = channelStatus(item.channel_status)
        return (
          <div className='min-w-0'>
            <div className='flex min-w-0 items-center gap-2'>
              <div className='truncate font-medium'>{item.channel_name}</div>
              <StatusBadge
                label={status.label}
                variant={status.variant}
                size='sm'
                copyable={false}
              />
            </div>
            <div className='text-muted-foreground text-xs'>
              #{item.channel_id}
            </div>
          </div>
        )
      },
    },
    {
      id: 'matched',
      header: t('Matched provider names'),
      className: 'min-w-56',
      cell: (item: ModelAliasChannelPreview) => (
        <div className='flex max-w-md flex-wrap gap-1'>
          {item.matched_models.length > 0
            ? item.matched_models.map((modelName) => (
                <code
                  key={modelName}
                  className='bg-muted rounded px-1.5 py-0.5 text-xs'
                >
                  {modelName}
                </code>
              ))
            : '-'}
        </div>
      ),
    },
    {
      id: 'mapping',
      header: t('Mapping'),
      className: 'min-w-52',
      cell: (item: ModelAliasChannelPreview) => (
        <div className='text-xs'>
          {item.current_target ? (
            <div>
              <span className='text-muted-foreground'>{t('Current')}: </span>
              <code>{item.current_target}</code>
            </div>
          ) : null}
          {item.proposed_target ? (
            <div>
              <span className='text-muted-foreground'>{t('Target')}: </span>
              <code>{item.proposed_target}</code>
            </div>
          ) : null}
          {!item.current_target && !item.proposed_target ? '-' : null}
        </div>
      ),
    },
    {
      id: 'status',
      header: t('Status'),
      className: 'w-40',
      cell: (item: ModelAliasChannelPreview) => (
        <div className='flex flex-col items-start gap-1'>
          <Badge variant={statusVariant[item.status]}>
            {statusLabels[item.status]}
          </Badge>
          {item.reason ? (
            <span className='text-muted-foreground max-w-52 text-xs whitespace-normal'>
              {reasonLabels[item.reason] ?? item.reason}
            </span>
          ) : null}
        </div>
      ),
    },
  ]

  return (
    <div className='flex flex-col gap-3'>
      <div className='flex flex-wrap gap-2'>
        {previewStatuses.map((status) => {
          const isSelected = selectedStatus === status
          return (
            <Badge
              key={status}
              render={<button type='button' />}
              variant={statusVariant[status]}
              aria-pressed={isSelected}
              onClick={() =>
                setSelectedStatus((current) =>
                  current === status ? null : status
                )
              }
              className={cn(
                'cursor-pointer select-none hover:opacity-100',
                isSelected
                  ? 'ring-ring ring-2 ring-offset-2 ring-offset-background'
                  : 'opacity-75'
              )}
            >
              {statusLabels[status]}: {preview.counts[status] ?? 0}
            </Badge>
          )
        })}
      </div>
      <StaticDataTable
        columns={columns}
        data={filteredItems}
        getRowKey={(item) => item.channel_id}
        emptyContent={t('No channels found')}
        tableClassName='min-w-[900px]'
        className='max-h-[32rem] overflow-auto'
      />
    </div>
  )
}
