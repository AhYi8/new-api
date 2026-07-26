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
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { BadgeListCell } from '@/components/data-table'
import { GroupBadge } from '@/components/group-badge'
import type { Option } from '@/components/multi-select'

import { ERROR_MESSAGES } from '../constants'
import {
  channelGroupUpdateMutationKey,
  formatGroupsString,
  handleUpdateChannelGroups,
  isTagAggregateRow,
  normalizeChannelGroups,
  parseGroupsList,
  prepareChannelGroupUpdate,
} from '../lib'
import type { Channel } from '../types'
import { ChannelGroupsControl } from './channel-form-controls'

const SENSITIVE_MASK = '••••'

type ChannelGroupsCellProps = {
  channel: Channel
  options: Option[]
  optionsReady: boolean
  sensitiveVisible: boolean
}

function ChannelGroupsSummary(props: {
  groups: string[]
  sensitiveVisible: boolean
}) {
  if (props.groups.length === 0) {
    return <span className='text-muted-foreground text-xs'>-</span>
  }

  return (
    <BadgeListCell
      items={props.groups.map((group) => (
        <GroupBadge
          key={group}
          group={group}
          label={props.sensitiveVisible ? undefined : SENSITIVE_MASK}
          size='sm'
        />
      ))}
    />
  )
}

export function ChannelGroupsCell(props: ChannelGroupsCellProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const updateGroupsMutation = useMutation({
    mutationKey: channelGroupUpdateMutationKey,
    mutationFn: (update: { id: number; group: string }) =>
      handleUpdateChannelGroups(update.id, update.group, queryClient),
  })
  const serverGroups = useMemo(
    () => normalizeChannelGroups(parseGroupsList(props.channel.group ?? '')),
    [props.channel.group]
  )
  const serverGroupValue = formatGroupsString(serverGroups)
  const [draftGroups, setDraftGroups] = useState(() => serverGroups)
  const [isDirty, setIsDirty] = useState(false)
  const draftGroupsRef = useRef(serverGroups)
  const confirmedGroupRef = useRef(serverGroupValue)
  const lastServerGroupRef = useRef(serverGroupValue)
  const isDirtyRef = useRef(false)
  const isSavingRef = useRef(false)

  useEffect(() => {
    if (lastServerGroupRef.current === serverGroupValue) return

    lastServerGroupRef.current = serverGroupValue
    confirmedGroupRef.current = serverGroupValue
    if (!isDirtyRef.current && !isSavingRef.current) {
      draftGroupsRef.current = serverGroups
      setDraftGroups(serverGroups)
    }
  }, [serverGroupValue, serverGroups])

  const updateDirtyState = (dirty: boolean) => {
    isDirtyRef.current = dirty
    setIsDirty(dirty)
  }

  const handleChange = (groups: string[]) => {
    const preparation = prepareChannelGroupUpdate(
      confirmedGroupRef.current,
      groups
    )
    if (!preparation.isValid) {
      toast.error(t(ERROR_MESSAGES.REQUIRED_GROUP))
      setDraftGroups((currentGroups) => [...currentGroups])
      return
    }

    draftGroupsRef.current = preparation.groups
    setDraftGroups(preparation.groups)
    updateDirtyState(preparation.hasChanges)
  }

  const handleCancel = () => {
    const confirmedGroups = normalizeChannelGroups(
      parseGroupsList(confirmedGroupRef.current)
    )
    draftGroupsRef.current = confirmedGroups
    setDraftGroups(confirmedGroups)
    updateDirtyState(false)
  }

  const handleCommit = async (groups: string[]) => {
    if (isSavingRef.current) return

    const preparation = prepareChannelGroupUpdate(
      confirmedGroupRef.current,
      groups
    )
    if (!preparation.isValid) {
      toast.error(t(ERROR_MESSAGES.REQUIRED_GROUP))
      return
    }
    if (!preparation.hasChanges) {
      draftGroupsRef.current = preparation.groups
      setDraftGroups(preparation.groups)
      updateDirtyState(false)
      return
    }

    isSavingRef.current = true
    const updated = await updateGroupsMutation.mutateAsync({
      id: props.channel.id,
      group: preparation.value,
    })
    isSavingRef.current = false

    if (!updated) return

    confirmedGroupRef.current = preparation.value
    const latestPreparation = prepareChannelGroupUpdate(
      preparation.value,
      draftGroupsRef.current
    )
    draftGroupsRef.current = latestPreparation.groups
    setDraftGroups(latestPreparation.groups)
    updateDirtyState(latestPreparation.hasChanges)
  }

  const groups = serverGroups
  if (isTagAggregateRow(props.channel) || !props.sensitiveVisible) {
    return (
      <ChannelGroupsSummary
        groups={groups}
        sensitiveVisible={props.sensitiveVisible}
      />
    )
  }

  return (
    <div
      className='min-w-0'
      data-dirty={isDirty || undefined}
      onClick={(event) => event.stopPropagation()}
    >
      <ChannelGroupsControl
        options={props.options}
        selected={draftGroups}
        onChange={handleChange}
        onCommit={(values) => void handleCommit(values)}
        onCancel={handleCancel}
        disabled={!props.optionsReady || updateGroupsMutation.isPending}
        className='min-w-64 flex-nowrap overflow-x-auto overflow-y-hidden [&_[data-slot=combobox-chip]]:shrink-0 [&_[data-slot=combobox-chip]>span]:max-w-none'
      />
    </div>
  )
}
