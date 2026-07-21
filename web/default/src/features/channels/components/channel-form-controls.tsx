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
import { useTranslation } from 'react-i18next'

import { MultiSelect, type Option } from '@/components/multi-select'

import { FIELD_PLACEHOLDERS } from '../constants'

type ChannelMultiSelectControlProps = {
  options: Option[]
  selected: string[]
  onChange?: (values: string[]) => void
  disabled?: boolean
  readOnly?: boolean
}

const ignoreChanges = () => undefined

export function ChannelModelsControl({
  options,
  selected,
  onChange = ignoreChanges,
  disabled,
  readOnly,
}: ChannelMultiSelectControlProps) {
  const { t } = useTranslation()

  return (
    <MultiSelect
      options={options}
      selected={selected}
      onChange={onChange}
      placeholder={t('Select models or add custom ones')}
      allowCreate={!readOnly}
      createLabel='Add custom model "{{value}}"'
      maxVisibleChips={8}
      copyChipOnClick={!readOnly}
      disabled={disabled}
      readOnly={readOnly}
    />
  )
}

export function ChannelGroupsControl({
  options,
  selected,
  onChange = ignoreChanges,
  disabled,
  readOnly,
}: ChannelMultiSelectControlProps) {
  const { t } = useTranslation()

  return (
    <MultiSelect
      options={options}
      selected={selected}
      onChange={onChange}
      placeholder={t(FIELD_PLACEHOLDERS.GROUP)}
      disabled={disabled}
      readOnly={readOnly}
    />
  )
}
