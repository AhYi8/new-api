import i18next from 'i18next'

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
import { api } from '@/lib/api'

import type {
  ApplyModelPricingSyncRequest,
  ApplyModelPricingSyncResponse,
  ConfirmPaymentComplianceResponse,
  FetchUpstreamRatiosRequest,
  LogCleanupTask,
  ModelAliasApplyResponse,
  ModelAliasCatalogResponse,
  ModelAliasGroup,
  ModelAliasGroupsResponse,
  ModelAliasPreviewResponse,
  ModelPricingLocksResponse,
  SystemOptionsResponse,
  SystemTaskListResponse,
  SystemTaskResponse,
  UpdateOptionRequest,
  UpdateOptionResponse,
  UpstreamChannelsResponse,
  UpstreamRatiosResponse,
  UpdateModelPricingLockRequest,
  UpdateModelPricingLockResponse,
} from './types'

export async function getSystemOptions() {
  const res = await api.get<SystemOptionsResponse>('/api/option/')
  return res.data
}

export async function updateSystemOption(request: UpdateOptionRequest) {
  const res = await api.put<UpdateOptionResponse>('/api/option/', request)
  return res.data
}

function assertSuccessfulResponse<
  T extends { success: boolean; message: string },
>(response: T, fallbackMessage: string) {
  if (!response.success) {
    throw new Error(response.message || fallbackMessage)
  }
  return response
}

export async function getModelAliasGroups() {
  const res = await api.get<ModelAliasGroupsResponse>(
    '/api/option/model-alias-groups'
  )
  return assertSuccessfulResponse(
    res.data,
    i18next.t('Failed to load model alias groups')
  )
}

export async function searchModelAliasCatalog(keyword: string) {
  const res = await api.get<ModelAliasCatalogResponse>(
    '/api/option/model-alias-groups/catalog',
    { params: { model_name: keyword } }
  )
  return assertSuccessfulResponse(
    res.data,
    i18next.t('Failed to search model catalog')
  )
}

export async function updateModelAliasGroups(configuration: {
  groups: ModelAliasGroup[]
  scan_enabled: boolean
  scan_interval_minutes: number
}) {
  const res = await api.put<ModelAliasGroupsResponse>(
    '/api/option/model-alias-groups',
    configuration
  )
  return assertSuccessfulResponse(
    res.data,
    i18next.t('Failed to save model alias groups')
  )
}

export async function previewModelAliasGroup(alias: string) {
  const res = await api.post<ModelAliasPreviewResponse>(
    '/api/option/model-alias-groups/preview',
    { alias }
  )
  return assertSuccessfulResponse(
    res.data,
    i18next.t('Failed to preview model alias group')
  )
}

export async function applyModelAliasGroup(alias: string) {
  const res = await api.post<ModelAliasApplyResponse>(
    '/api/option/model-alias-groups/apply',
    { alias }
  )
  return assertSuccessfulResponse(
    res.data,
    i18next.t('Failed to apply model alias group')
  )
}

export async function confirmPaymentCompliance() {
  const res = await api.post<ConfirmPaymentComplianceResponse>(
    '/api/option/payment_compliance',
    { confirmed: true }
  )
  return res.data
}

export async function startLogCleanupTask(targetTimestamp: number) {
  const res = await api.post<SystemTaskResponse<LogCleanupTask>>(
    '/api/system-task/log-cleanup',
    null,
    {
      params: { target_timestamp: targetTimestamp },
    }
  )
  return res.data
}

export async function getCurrentLogCleanupTask() {
  const res = await api.get<SystemTaskResponse<LogCleanupTask | null>>(
    '/api/system-task/current',
    {
      params: { type: 'log_cleanup' },
    }
  )
  return res.data
}

export async function getSystemTask(taskId: string) {
  const res = await api.get<SystemTaskResponse<LogCleanupTask>>(
    `/api/system-task/${taskId}`
  )
  return res.data
}

export async function listSystemTasks(limit = 20) {
  const res = await api.get<SystemTaskListResponse>('/api/system-task/list', {
    params: { limit },
  })
  return res.data
}

export async function resetModelRatios() {
  const res = await api.post<UpdateOptionResponse>(
    '/api/option/rest_model_ratio'
  )
  return res.data
}

export async function getUpstreamChannels() {
  const res = await api.get<UpstreamChannelsResponse>(
    '/api/ratio_sync/channels'
  )
  return res.data
}

export async function fetchUpstreamRatios(request: FetchUpstreamRatiosRequest) {
  const res = await api.post<UpstreamRatiosResponse>(
    '/api/ratio_sync/fetch',
    request
  )
  return res.data
}

export async function getModelPricingLocks() {
  const res = await api.get<ModelPricingLocksResponse>('/api/ratio_sync/locks')
  if (!res.data.success) {
    throw new Error(res.data.message || i18next.t('Failed to load price locks'))
  }
  return res.data
}

export async function updateModelPricingLock(
  request: UpdateModelPricingLockRequest
) {
  const res = await api.put<UpdateModelPricingLockResponse>(
    '/api/ratio_sync/lock',
    request
  )
  if (!res.data.success) {
    throw new Error(
      res.data.message || i18next.t('Failed to update price lock')
    )
  }
  return res.data
}

export async function applyModelPricingSync(
  request: ApplyModelPricingSyncRequest
) {
  const res = await api.post<ApplyModelPricingSyncResponse>(
    '/api/ratio_sync/apply',
    request
  )
  if (!res.data.success) {
    throw new Error(res.data.message || i18next.t('Failed to sync prices'))
  }
  return res.data
}
