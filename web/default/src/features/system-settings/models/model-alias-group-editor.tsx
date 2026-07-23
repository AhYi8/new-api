/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'

import type { ModelAliasGroup } from '../types'

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
  const [modelsText, setModelsText] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    if (!open) return
    setAlias(group?.alias ?? '')
    setModelsText(group?.models.join('\n') ?? '')
    setError('')
  }, [group, open])

  const handleSubmit = async () => {
    const normalizedAlias = alias.trim()
    const models = [
      ...new Set(
        modelsText
          .split(/\r?\n/)
          .map((modelName) => modelName.trim())
          .filter(Boolean)
      ),
    ]

    if (!normalizedAlias) {
      setError(t('Unified model name is required'))
      return
    }
    if (models.length === 0) {
      setError(t('Enter at least one provider model name'))
      return
    }
    if (models.includes(normalizedAlias)) {
      setError(t('Unified model name must differ from provider model names'))
      return
    }

    if (await onSubmit({ alias: normalizedAlias, models })) {
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
      <div className='flex flex-col gap-4'>
        <div className='flex flex-col gap-2'>
          <Label htmlFor='model-alias'>{t('Unified model name')}</Label>
          <Input
            id='model-alias'
            value={alias}
            aria-invalid={error ? true : undefined}
            aria-describedby={error ? 'model-alias-editor-error' : undefined}
            disabled={isSubmitting}
            onChange={(event) => {
              setAlias(event.target.value)
              setError('')
            }}
            placeholder={t('e.g. deepseek-v4-pro')}
          />
        </div>
        <div className='flex flex-col gap-2'>
          <Label htmlFor='provider-models'>{t('Provider model names')}</Label>
          <Textarea
            id='provider-models'
            rows={8}
            value={modelsText}
            aria-invalid={error ? true : undefined}
            aria-describedby={error ? 'model-alias-editor-error' : undefined}
            disabled={isSubmitting}
            onChange={(event) => {
              setModelsText(event.target.value)
              setError('')
            }}
            placeholder={t('Enter one exact model name per line')}
          />
          <p className='text-muted-foreground text-xs'>
            {t(
              'Matching is exact and case-sensitive. Duplicate names are removed.'
            )}
          </p>
        </div>
        {error ? (
          <p
            id='model-alias-editor-error'
            className='text-destructive text-sm'
            role='alert'
          >
            {error}
          </p>
        ) : null}
      </div>
    </Dialog>
  )
}
