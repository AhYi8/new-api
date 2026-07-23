/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Eye, Pencil, Plus, Trash2, WandSparkles } from 'lucide-react'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from '@/components/ui/empty'
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'

import {
  applyModelAliasGroup,
  getModelAliasGroups,
  previewModelAliasGroup,
  updateModelAliasGroups,
} from '../api'
import type {
  ModelAliasGroup,
  ModelAliasPreview as ModelAliasPreviewData,
} from '../types'
import { ModelAliasGroupEditor } from './model-alias-group-editor'
import { ModelAliasPreview } from './model-alias-preview'

const MODEL_ALIAS_QUERY_KEY = ['model-alias-groups'] as const
const MINIMUM_SCAN_INTERVAL_MINUTES = 10

function stripPendingCount(group: ModelAliasGroup): ModelAliasGroup {
  return { alias: group.alias, models: [...group.models] }
}

export function ModelAliasGroupsSection() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [draftScanEnabled, setDraftScanEnabled] = useState<boolean | null>(null)
  const [draftScanInterval, setDraftScanInterval] = useState<string | null>(
    null
  )
  const [editorOpen, setEditorOpen] = useState(false)
  const [editingGroup, setEditingGroup] = useState<ModelAliasGroup | null>(null)
  const [preview, setPreview] = useState<ModelAliasPreviewData | null>(null)
  const [applyAlias, setApplyAlias] = useState<string | null>(null)
  const [deleteAlias, setDeleteAlias] = useState<string | null>(null)
  const saveInFlightRef = useRef(false)

  const configurationQuery = useQuery({
    queryKey: MODEL_ALIAS_QUERY_KEY,
    queryFn: getModelAliasGroups,
    staleTime: 0,
    refetchOnWindowFocus: 'always',
  })
  const serverConfiguration = configurationQuery.data?.data
  const serverGroups = serverConfiguration?.groups ?? []
  const scanEnabled =
    draftScanEnabled ?? serverConfiguration?.scan_enabled ?? true
  const scanIntervalText =
    draftScanInterval ??
    String(serverConfiguration?.scan_interval_minutes ?? 30)
  const scanIntervalMinutes = Number(scanIntervalText)
  const scanIntervalInvalid =
    !Number.isInteger(scanIntervalMinutes) ||
    scanIntervalMinutes < MINIMUM_SCAN_INTERVAL_MINUTES
  const saveMutation = useMutation({
    mutationFn: updateModelAliasGroups,
    onSuccess: (response) => {
      queryClient.setQueryData(MODEL_ALIAS_QUERY_KEY, response)
      toast.success(t('Model alias groups saved'))
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const persistConfiguration = async (
    nextGroups: ModelAliasGroup[],
    nextScanEnabled = serverConfiguration?.scan_enabled ?? true,
    nextScanIntervalMinutes = serverConfiguration?.scan_interval_minutes ?? 30
  ) => {
    if (saveInFlightRef.current) return false

    saveInFlightRef.current = true
    try {
      await queryClient.cancelQueries({ queryKey: MODEL_ALIAS_QUERY_KEY })
      await saveMutation.mutateAsync({
        groups: nextGroups.map(stripPendingCount),
        scan_enabled: nextScanEnabled,
        scan_interval_minutes: nextScanIntervalMinutes,
      })
      return true
    } catch {
      return false
    } finally {
      saveInFlightRef.current = false
    }
  }

  const previewMutation = useMutation({
    mutationFn: previewModelAliasGroup,
    onSuccess: (response) => setPreview(response.data),
    onError: (error: Error) => toast.error(error.message),
  })

  const applyMutation = useMutation({
    mutationFn: applyModelAliasGroup,
    onSuccess: (response) => {
      setApplyAlias(null)
      toast.success(
        t('Applied model alias group to {{count}} channels', {
          count: response.data.applied,
        })
      )
      if (response.data.failed.length > 0) {
        toast.warning(
          t('{{count}} channels failed and were left unchanged', {
            count: response.data.failed.length,
          })
        )
      }
      if (preview?.alias) previewMutation.mutate(preview.alias)
      queryClient.invalidateQueries({ queryKey: MODEL_ALIAS_QUERY_KEY })
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const openAdd = () => {
    if (saveMutation.isPending) return
    setEditingGroup(null)
    setEditorOpen(true)
  }

  const openEdit = (group: ModelAliasGroup) => {
    if (saveMutation.isPending) return
    setEditingGroup(stripPendingCount(group))
    setEditorOpen(true)
  }

  const handleEditorSubmit = async (nextGroup: ModelAliasGroup) => {
    const existingIndex = serverGroups.findIndex(
      (group) => group.alias === editingGroup?.alias
    )
    const nextGroups =
      existingIndex < 0
        ? [...serverGroups, nextGroup]
        : serverGroups.map((group, index) =>
            index === existingIndex ? nextGroup : group
          )
    const saved = await persistConfiguration(nextGroups)
    if (saved) setPreview(null)
    return saved
  }

  const handleDelete = (alias: string) => {
    if (!saveMutation.isPending) setDeleteAlias(alias)
  }

  const handlePreview = (alias: string) => {
    previewMutation.mutate(alias)
  }

  const handleScanEnabledChange = async (checked: boolean) => {
    if (!serverConfiguration || saveMutation.isPending) return
    setDraftScanEnabled(checked)
    await persistConfiguration(
      serverGroups,
      checked,
      serverConfiguration.scan_interval_minutes
    )
    setDraftScanEnabled(null)
  }

  const persistScanInterval = async () => {
    if (!serverConfiguration || scanIntervalInvalid || saveMutation.isPending) {
      return
    }
    if (scanIntervalMinutes === serverConfiguration.scan_interval_minutes) {
      setDraftScanInterval(null)
      return
    }
    await persistConfiguration(
      serverGroups,
      serverConfiguration.scan_enabled,
      scanIntervalMinutes
    )
    setDraftScanInterval(null)
  }

  const handleDeleteConfirm = async () => {
    if (!deleteAlias || !serverConfiguration) return
    const alias = deleteAlias
    if (
      await persistConfiguration(
        serverGroups.filter((group) => group.alias !== alias),
        serverConfiguration.scan_enabled,
        serverConfiguration.scan_interval_minutes
      )
    ) {
      setDeleteAlias(null)
      if (preview?.alias === alias) setPreview(null)
    }
  }

  const columns = [
    {
      id: 'alias',
      header: t('Unified model name'),
      className: 'min-w-48',
      cell: (group: ModelAliasGroup) => (
        <code className='font-medium'>{group.alias}</code>
      ),
    },
    {
      id: 'models',
      header: t('Provider model names'),
      className: 'min-w-80',
      cell: (group: ModelAliasGroup) => (
        <div className='flex flex-wrap gap-1'>
          {group.models.map((modelName) => (
            <code
              key={modelName}
              className='bg-muted rounded px-1.5 py-0.5 text-xs'
            >
              {modelName}
            </code>
          ))}
        </div>
      ),
    },
    {
      id: 'pending-count',
      header: t('Pending items'),
      className: 'w-32',
      cellClassName: 'tabular-nums',
      cell: (group: ModelAliasGroup) => group.pending_count ?? '--',
    },
    {
      id: 'actions',
      header: t('Actions'),
      className: 'w-48',
      cell: (group: ModelAliasGroup) => {
        const pendingCount = group.pending_count
        const previewLabel =
          pendingCount && pendingCount > 0
            ? t('Preview, {{count}} pending items', { count: pendingCount })
            : t('Preview')
        return (
          <div className='flex items-center gap-1'>
            <Button
              variant='ghost'
              size='icon-sm'
              className='relative'
              aria-label={previewLabel}
              onClick={() => handlePreview(group.alias)}
              disabled={previewMutation.isPending || saveMutation.isPending}
            >
              <Eye />
              {pendingCount && pendingCount > 0 ? (
                <span
                  className='bg-destructive absolute top-1 right-1 size-2 rounded-full'
                  aria-hidden='true'
                />
              ) : null}
            </Button>
            <Button
              variant='ghost'
              size='icon-sm'
              aria-label={t('Edit')}
              onClick={() => openEdit(group)}
              disabled={saveMutation.isPending}
            >
              <Pencil />
            </Button>
            <Button
              variant='ghost'
              size='icon-sm'
              aria-label={t('Delete')}
              onClick={() => handleDelete(group.alias)}
              disabled={saveMutation.isPending}
            >
              <Trash2 />
            </Button>
          </div>
        )
      },
    },
  ]

  if (configurationQuery.isLoading && !serverConfiguration) {
    return (
      <div className='space-y-4'>
        <Skeleton className='h-20 w-full' />
        <Skeleton className='h-48 w-full' />
      </div>
    )
  }

  if (configurationQuery.isError && !serverConfiguration) {
    return (
      <Alert variant='destructive'>
        <AlertTitle>{t('Failed to load model alias groups')}</AlertTitle>
        <AlertDescription className='flex items-center justify-between gap-3'>
          <span>{t('Try again to load the saved alias configuration.')}</span>
          <Button
            variant='outline'
            size='sm'
            onClick={() => configurationQuery.refetch()}
          >
            {t('Retry')}
          </Button>
        </AlertDescription>
      </Alert>
    )
  }

  return (
    <div className='flex min-w-0 flex-col gap-6'>
      <div className='flex flex-col gap-1'>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Define one unified name for exact provider model names, then manually apply it to matching channels.'
          )}
        </p>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Scheduled detection only updates pending counts. Applying a group still uses the existing channel mapping and routing flow.'
          )}
        </p>
      </div>

      <FieldGroup className='border-y py-4 md:grid md:grid-cols-2 md:gap-8'>
        <Field orientation='horizontal'>
          <FieldContent>
            <FieldLabel htmlFor='model-alias-scan-enabled'>
              {t('Scheduled detection')}
            </FieldLabel>
            <FieldDescription>
              {t(
                'Periodically refresh pending counts for all model alias groups.'
              )}
            </FieldDescription>
          </FieldContent>
          <Switch
            id='model-alias-scan-enabled'
            checked={scanEnabled}
            disabled={saveMutation.isPending}
            onCheckedChange={handleScanEnabledChange}
          />
        </Field>
        <Field data-invalid={scanIntervalInvalid || undefined}>
          <FieldLabel htmlFor='model-alias-scan-interval'>
            {t('Detection interval (minutes)')}
          </FieldLabel>
          <Input
            id='model-alias-scan-interval'
            type='number'
            min={MINIMUM_SCAN_INTERVAL_MINUTES}
            step={1}
            value={scanIntervalText}
            aria-invalid={scanIntervalInvalid || undefined}
            disabled={saveMutation.isPending}
            onChange={(event) => {
              const value = event.target.value
              setDraftScanInterval(
                value === String(serverConfiguration?.scan_interval_minutes)
                  ? null
                  : value
              )
            }}
            onBlur={() => void persistScanInterval()}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault()
                void persistScanInterval()
              }
            }}
          />
          <FieldDescription>
            {t('The minimum detection interval is {{count}} minutes.', {
              count: MINIMUM_SCAN_INTERVAL_MINUTES,
            })}
          </FieldDescription>
          {scanIntervalInvalid ? (
            <FieldError>
              {t('Enter an integer of at least {{count}}.', {
                count: MINIMUM_SCAN_INTERVAL_MINUTES,
              })}
            </FieldError>
          ) : null}
        </Field>
      </FieldGroup>

      <div className='flex flex-wrap items-center gap-2'>
        <Button onClick={openAdd} disabled={saveMutation.isPending}>
          <Plus data-icon='inline-start' />
          {t('Add model alias group')}
        </Button>
      </div>

      {serverGroups.length === 0 ? (
        <Empty className='min-h-48'>
          <EmptyHeader>
            <EmptyTitle>{t('No model alias groups')}</EmptyTitle>
            <EmptyDescription>
              {t('Add a group to unify equivalent provider model names.')}
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <StaticDataTable
          columns={columns}
          data={serverGroups}
          getRowKey={(group) => group.alias}
          tableClassName='min-w-[880px]'
          className='overflow-auto'
        />
      )}

      {preview ? (
        <>
          <Separator />
          <div className='flex min-w-0 flex-col gap-4'>
            <div className='flex flex-wrap items-center justify-between gap-2'>
              <div>
                <h3 className='text-base font-semibold'>
                  {t('Preview for {{alias}}', { alias: preview.alias })}
                </h3>
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Conflicts are shown for manual handling and will not be overwritten.'
                  )}
                </p>
              </div>
              <Button
                onClick={() => setApplyAlias(preview.alias)}
                disabled={
                  (preview.counts.new ?? 0) + (preview.counts.updated ?? 0) ===
                    0 || applyMutation.isPending
                }
              >
                <WandSparkles data-icon='inline-start' />
                {t('Apply eligible changes')}
              </Button>
            </div>
            <ModelAliasPreview key={preview.alias} preview={preview} />
          </div>
        </>
      ) : null}

      <ModelAliasGroupEditor
        open={editorOpen}
        group={editingGroup}
        isSubmitting={saveMutation.isPending}
        onOpenChange={setEditorOpen}
        onSubmit={handleEditorSubmit}
      />
      <ConfirmDialog
        open={applyAlias !== null}
        onOpenChange={(open) => {
          if (!open && !applyMutation.isPending) setApplyAlias(null)
        }}
        title={t('Apply model alias group?')}
        desc={t(
          'Only channels classified as new or update will be changed. Conflicts and unmatched channels remain untouched.'
        )}
        confirmText={t('Apply changes')}
        isLoading={applyMutation.isPending}
        handleConfirm={() => applyAlias && applyMutation.mutate(applyAlias)}
      />
      <ConfirmDialog
        open={deleteAlias !== null}
        onOpenChange={(open) => {
          if (!open && !saveMutation.isPending) setDeleteAlias(null)
        }}
        title={
          deleteAlias
            ? t(
                'Are you sure you want to delete group "{{name}}"? This action cannot be undone.',
                { name: deleteAlias }
              )
            : ''
        }
        desc={t('This action cannot be undone.')}
        confirmText={t('Delete')}
        destructive
        isLoading={saveMutation.isPending}
        handleConfirm={() => void handleDeleteConfirm()}
      />
    </div>
  )
}
