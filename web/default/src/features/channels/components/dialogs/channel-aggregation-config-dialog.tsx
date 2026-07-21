import { ArrowRight, Check } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { FieldDescription, FieldLegend, FieldSet } from '@/components/ui/field'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

import { FIELD_DESCRIPTIONS } from '../../constants'
import {
  buildChannelAggregationPrefill,
  CHANNEL_AGGREGATION_CONFIG_FIELDS,
  compareChannelAggregationConfigs,
  type ChannelAggregationConfigField,
} from '../../lib/channel-aggregation-config'
import { parseModelsString } from '../../lib/model-mapping-validation'
import type {
  ChannelAggregationPrefill,
  ChannelAggregationPreparedData,
} from '../../types'
import { ChannelModelsControl } from '../channel-form-controls'

type ChannelAggregationConfigDialogProps = {
  open: boolean
  prepared: ChannelAggregationPreparedData | null
  onCancel: () => void
  onContinue: (prefill: ChannelAggregationPrefill) => void
}

const FIELD_LABELS: Record<ChannelAggregationConfigField, string> = {
  name: 'Name *',
  openai_organization: 'OpenAI Organization',
  test_model: 'Test Model',
  weight: 'Weight',
  group: 'Groups *',
  priority: 'Priority',
  auto_ban: 'Auto Ban',
  tag: 'Tag',
  remark: 'Remark',
  other: 'Other',
  proxy: 'Proxy Address',
}

const FIELD_DESCRIPTIONS_BY_NAME: Partial<
  Record<ChannelAggregationConfigField, string>
> = {
  openai_organization: FIELD_DESCRIPTIONS.OPENAI_ORG,
  test_model: FIELD_DESCRIPTIONS.TEST_MODEL,
  weight: FIELD_DESCRIPTIONS.WEIGHT,
  group: FIELD_DESCRIPTIONS.GROUP,
  priority: FIELD_DESCRIPTIONS.PRIORITY,
  auto_ban: FIELD_DESCRIPTIONS.AUTO_BAN,
  tag: FIELD_DESCRIPTIONS.TAG,
  remark: FIELD_DESCRIPTIONS.REMARK,
  proxy: 'Network proxy for this channel (supports socks5 protocol)',
}

function formatCandidateValue(
  field: ChannelAggregationConfigField,
  value: ChannelAggregationPrefill[ChannelAggregationConfigField],
  translate: (key: string) => string
) {
  if (field === 'group') {
    const groups = value as string[]
    return groups.length > 0 ? groups.join(', ') : translate('Not set')
  }

  if (field === 'auto_ban') {
    return translate(value === 1 ? 'Enabled' : 'Disabled')
  }

  const text = String(value)
  return text || translate('Not set')
}

export function ChannelAggregationConfigDialog({
  open,
  prepared,
  onCancel,
  onContinue,
}: ChannelAggregationConfigDialogProps) {
  const { t } = useTranslation()
  const [selections, setSelections] = useState<Record<string, string>>({})
  const comparison = useMemo(
    () => (prepared ? compareChannelAggregationConfigs(prepared) : null),
    [prepared]
  )
  const modelValues = useMemo(
    () => parseModelsString(comparison?.models || ''),
    [comparison?.models]
  )
  const modelOptions = useMemo(
    () => modelValues.map((model) => ({ value: model, label: model })),
    [modelValues]
  )

  useEffect(() => {
    setSelections({})
  }, [prepared?.snapshot_token])

  const prefill = useMemo(
    () =>
      comparison
        ? buildChannelAggregationPrefill(comparison, selections)
        : null,
    [comparison, selections]
  )

  if (!prepared || !comparison) return null

  const handleContinue = () => {
    if (!prefill) return
    onContinue(prefill)
  }

  const commonCount =
    CHANNEL_AGGREGATION_CONFIG_FIELDS.length - comparison.conflicts.length

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (!nextOpen) onCancel()
      }}
      title={t('Resolve aggregation settings')}
      description={t(
        'Choose a source value for each setting that differs between the selected channels.'
      )}
      contentClassName='sm:max-w-3xl'
      footer={
        <>
          <Button variant='outline' onClick={onCancel}>
            {t('Cancel')}
          </Button>
          <Button disabled={!prefill} onClick={handleContinue}>
            {prefill ? <Check data-icon='inline-start' /> : null}
            {t('Continue')}
            <ArrowRight data-icon='inline-end' />
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        <div className='bg-muted/40 flex flex-wrap items-center gap-2 rounded-lg border px-3 py-2 text-sm'>
          <Badge variant='secondary'>
            {t('{{count}} source channels', { count: prepared.sources.length })}
          </Badge>
          <span className='text-muted-foreground'>
            {t('{{count}} settings will be copied automatically', {
              count: commonCount,
            })}
          </span>
          {comparison.models ? (
            <Badge variant='outline'>
              {t('{{count}} models', {
                count: modelValues.length,
              })}
            </Badge>
          ) : null}
        </div>

        {comparison.conflicts.length > 0 ? (
          <div className='space-y-5'>
            {comparison.conflicts.map((conflict) => {
              const description = FIELD_DESCRIPTIONS_BY_NAME[conflict.field]
              const candidateItems = conflict.candidates.map((candidate) => ({
                candidate,
                label: formatCandidateValue(conflict.field, candidate.value, t),
              }))
              const items = [
                { label: t('Needs selection'), value: null },
                ...candidateItems.map(({ candidate, label }) => ({
                  label,
                  value: candidate.key,
                })),
              ]
              const selectedKey = selections[conflict.field]
              const selectedLabel =
                candidateItems.find(
                  ({ candidate }) => candidate.key === selectedKey
                )?.label ?? t('Needs selection')
              return (
                <FieldSet key={conflict.field}>
                  <FieldLegend
                    variant='label'
                    className='flex items-center gap-2'
                  >
                    {t(FIELD_LABELS[conflict.field])}
                    {!selectedKey ? (
                      <Badge variant='outline'>{t('Needs selection')}</Badge>
                    ) : null}
                  </FieldLegend>
                  {description ? (
                    <FieldDescription>{t(description)}</FieldDescription>
                  ) : null}
                  <Select
                    items={items}
                    value={selections[conflict.field] ?? null}
                    onValueChange={(value) => {
                      if (value === null) return
                      setSelections((current) => ({
                        ...current,
                        [conflict.field]: value,
                      }))
                    }}
                  >
                    <SelectTrigger
                      className='h-10 w-full min-w-0'
                      title={selectedLabel}
                    >
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent
                      align='start'
                      alignItemWithTrigger={false}
                      className='max-h-72'
                    >
                      <SelectGroup>
                        {candidateItems.map(({ candidate, label }) => {
                          const sourceLabel = candidate.sources
                            .map((source) => `#${source.id} ${source.name}`)
                            .join(', ')
                          return (
                            <SelectItem
                              key={candidate.key}
                              value={candidate.key}
                              className='py-2'
                            >
                              <span className='min-w-0 flex-1'>
                                <span
                                  className='block break-words whitespace-normal'
                                  title={label}
                                >
                                  {label}
                                </span>
                                <span
                                  className='text-muted-foreground block truncate text-xs'
                                  title={sourceLabel}
                                >
                                  {sourceLabel}
                                </span>
                              </span>
                            </SelectItem>
                          )
                        })}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                </FieldSet>
              )
            })}
          </div>
        ) : (
          <div className='text-muted-foreground rounded-lg border border-dashed px-3 py-4 text-sm'>
            {t('All supported settings match across the selected channels.')}
          </div>
        )}

        <div className='border-border/60 space-y-3 rounded-lg border p-4'>
          <div className='flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between'>
            <div className='space-y-1'>
              <p className='text-sm font-medium'>{t('Models *')}</p>
              <p className='text-muted-foreground text-sm'>
                {t(FIELD_DESCRIPTIONS.MODELS)}
              </p>
            </div>
            <Badge variant='outline' className='w-fit'>
              {t('Selected {{count}}', { count: modelValues.length })}
            </Badge>
          </div>
          <ChannelModelsControl
            options={modelOptions}
            selected={modelValues}
            readOnly
          />
        </div>
      </div>
    </Dialog>
  )
}
