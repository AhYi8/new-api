/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { type ReactNode, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { MultiSelect } from '@/components/multi-select'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'

import { searchModelAliasCatalog } from '../api'
import type { ModelAliasGroup } from '../types'
import {
  findProviderModelsMissingFromCatalog,
  normalizeProviderModelNames,
} from './model-alias-catalog'

const EMPTY_CATALOG_MODELS: string[] = []

type EditorError = {
  field: 'alias' | 'models'
  message: string
}

type ModelAliasGroupEditorProps = {
  open: boolean
  group: ModelAliasGroup | null
  isSubmitting?: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (group: ModelAliasGroup) => Promise<boolean>
}

export function ModelAliasGroupEditor({
  open,
  group,
  isSubmitting = false,
  onOpenChange,
  onSubmit,
}: ModelAliasGroupEditorProps) {
  const { t } = useTranslation()
  const [alias, setAlias] = useState('')
  const [models, setModels] = useState<string[]>([])
  const [catalogKeyword, setCatalogKeyword] = useState('')
  const [catalogRequestId, setCatalogRequestId] = useState(0)
  const [error, setError] = useState<EditorError | null>(null)

  useEffect(() => {
    if (!open) return
    const initialAlias = group?.alias ?? ''
    setAlias(initialAlias)
    setModels(group?.models ?? [])
    setCatalogKeyword(initialAlias.trim())
    setCatalogRequestId((requestId) => requestId + 1)
    setError(null)
  }, [group, open])

  const catalogQuery = useQuery({
    queryKey: ['model-alias-catalog', catalogKeyword, catalogRequestId],
    queryFn: () => searchModelAliasCatalog(catalogKeyword),
    enabled: open && catalogKeyword.length > 0,
    retry: 1,
  })

  const normalizedAlias = alias.trim()
  const catalogMatchesAlias =
    normalizedAlias.length > 0 && normalizedAlias === catalogKeyword
  const catalogIsCurrent =
    catalogMatchesAlias &&
    !catalogQuery.isError &&
    catalogQuery.data !== undefined
  const catalogModels = catalogIsCurrent
    ? catalogQuery.data.data.models
    : EMPTY_CATALOG_MODELS
  const catalogOptions = useMemo(
    () =>
      catalogModels.map((modelName) => ({
        value: modelName,
        label: modelName,
      })),
    [catalogModels]
  )
  const missingModels = useMemo(
    () =>
      findProviderModelsMissingFromCatalog(
        models,
        catalogModels,
        catalogIsCurrent
      ),
    [catalogIsCurrent, catalogModels, models]
  )
  const warningValues = useMemo(() => new Set(missingModels), [missingModels])
  const catalogStatusVisible =
    catalogMatchesAlias &&
    (catalogQuery.isFetching || catalogQuery.isError || catalogIsCurrent)
  const providerDescriptionIds = [
    'provider-models-description',
    catalogStatusVisible ? 'provider-models-catalog-status' : '',
    error?.field === 'models' ? 'model-alias-editor-error' : '',
  ]
    .filter(Boolean)
    .join(' ')

  const requestCatalogSearch = () => {
    if (!normalizedAlias) return
    if (normalizedAlias === catalogKeyword && catalogQuery.isFetching) return
    setCatalogKeyword(normalizedAlias)
    setCatalogRequestId((requestId) => requestId + 1)
  }

  let catalogEmptyText = t(
    'Enter a unified model name, then press Enter or leave the field to search.'
  )
  if (catalogMatchesAlias && catalogQuery.isFetching) {
    catalogEmptyText = t('Searching model catalog...')
  } else if (catalogMatchesAlias && catalogQuery.isError) {
    catalogEmptyText = t('Failed to search model catalog')
  } else if (catalogIsCurrent) {
    catalogEmptyText = t('No catalog models match this unified name')
  }

  let catalogStatus: ReactNode = null
  if (catalogMatchesAlias && catalogQuery.isFetching) {
    catalogStatus = (
      <FieldDescription
        id='provider-models-catalog-status'
        className='flex items-center gap-2'
        role='status'
      >
        <Spinner aria-hidden='true' />
        {t('Searching model catalog...')}
      </FieldDescription>
    )
  } else if (catalogMatchesAlias && catalogQuery.isError) {
    catalogStatus = (
      <div
        id='provider-models-catalog-status'
        className='text-destructive flex items-center gap-2 text-sm'
        role='alert'
      >
        <span>{t('Failed to search model catalog')}</span>
        <Button
          type='button'
          variant='ghost'
          size='icon-sm'
          onClick={() => void catalogQuery.refetch()}
          aria-label={t('Retry model catalog search')}
          title={t('Retry model catalog search')}
        >
          <RefreshCw aria-hidden='true' />
        </Button>
      </div>
    )
  } else if (catalogIsCurrent && missingModels.length > 0) {
    catalogStatus = (
      <FieldDescription
        id='provider-models-catalog-status'
        className='text-amber-700 dark:text-amber-400'
        role='status'
      >
        {t(
          'Selected provider model names not found in this catalog search: {{count}}. They can still be saved.',
          { count: missingModels.length }
        )}
      </FieldDescription>
    )
  } else if (catalogIsCurrent) {
    catalogStatus = (
      <FieldDescription id='provider-models-catalog-status' role='status'>
        {t('Found {{count}} matching catalog models', {
          count: catalogModels.length,
        })}
      </FieldDescription>
    )
  }

  const handleSubmit = async () => {
    const normalizedModels = normalizeProviderModelNames(models)

    if (!normalizedAlias) {
      setError({ field: 'alias', message: t('Unified model name is required') })
      return
    }
    if (normalizedModels.length === 0) {
      setError({
        field: 'models',
        message: t('Enter at least one provider model name'),
      })
      return
    }
    if (normalizedModels.includes(normalizedAlias)) {
      setError({
        field: 'models',
        message: t('Unified model name must differ from provider model names'),
      })
      return
    }

    if (await onSubmit({ alias: normalizedAlias, models: normalizedModels })) {
      onOpenChange(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (!isSubmitting) onOpenChange(nextOpen)
      }}
      title={group ? t('Edit model alias group') : t('Add model alias group')}
      description={t(
        'A unified name maps exact provider model names without changing request routing.'
      )}
      contentClassName='sm:max-w-xl'
      footer={
        <>
          <Button
            variant='outline'
            onClick={() => onOpenChange(false)}
            disabled={isSubmitting}
          >
            {t('Cancel')}
          </Button>
          <Button onClick={handleSubmit} disabled={isSubmitting}>
            {isSubmitting ? t('Saving...') : t('Save group')}
          </Button>
        </>
      }
    >
      <FieldGroup>
        <Field
          data-invalid={error?.field === 'alias' || undefined}
          data-disabled={isSubmitting || undefined}
        >
          <FieldLabel htmlFor='model-alias'>
            {t('Unified model name')}
          </FieldLabel>
          <Input
            id='model-alias'
            value={alias}
            aria-invalid={error?.field === 'alias' || undefined}
            aria-describedby={
              error?.field === 'alias' ? 'model-alias-editor-error' : undefined
            }
            disabled={isSubmitting}
            onChange={(event) => {
              setAlias(event.target.value)
              setCatalogKeyword('')
              setError(null)
            }}
            onBlur={requestCatalogSearch}
            onKeyDown={(event) => {
              if (event.key !== 'Enter') return
              event.preventDefault()
              requestCatalogSearch()
            }}
            placeholder={t('e.g. deepseek-v4-pro')}
          />
          {error?.field === 'alias' && (
            <FieldError id='model-alias-editor-error'>
              {error.message}
            </FieldError>
          )}
        </Field>
        <Field
          data-invalid={error?.field === 'models' || undefined}
          data-disabled={isSubmitting || undefined}
        >
          <FieldLabel htmlFor='provider-models'>
            {t('Provider model names')}
          </FieldLabel>
          <MultiSelect
            key={`${group?.alias ?? 'new'}-${open ? 'open' : 'closed'}`}
            id='provider-models'
            options={catalogOptions}
            selected={models}
            onChange={(nextModels) => {
              setModels(normalizeProviderModelNames(nextModels))
              setError(null)
            }}
            allowCreate
            createLabel='Add custom model "{{value}}"'
            maxVisibleChips={8}
            warningValues={warningValues}
            warningText={t('Not found in the current model catalog search')}
            ariaDescribedBy={providerDescriptionIds}
            ariaInvalid={error?.field === 'models'}
            disabled={isSubmitting}
            placeholder={t('Select or enter provider model names')}
            emptyText={catalogEmptyText}
          />
          <FieldDescription id='provider-models-description'>
            {t(
              'Matching is exact and case-sensitive. Duplicate names are removed.'
            )}
          </FieldDescription>
          {catalogStatus}
          {error?.field === 'models' && (
            <FieldError id='model-alias-editor-error'>
              {error.message}
            </FieldError>
          )}
        </Field>
      </FieldGroup>
    </Dialog>
  )
}
