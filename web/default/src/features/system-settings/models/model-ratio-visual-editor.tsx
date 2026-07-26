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
import { PowerIcon, PowerOffIcon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import type {
  ColumnFiltersState,
  OnChangeFn,
  PaginationState,
  RowSelectionState,
  VisibilityState,
  SortingState,
} from '@tanstack/react-table'
import { Copy, Plus } from 'lucide-react'
import {
  useState,
  useMemo,
  memo,
  useCallback,
  useEffect,
  forwardRef,
  useImperativeHandle,
  useRef,
} from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  DataTableBulkActions,
  DataTableToolbar,
  DataTablePagination,
  DataTableRow,
  DataTableView,
  useDataTable,
} from '@/components/data-table'
import { Button } from '@/components/ui/button'
import { Spinner } from '@/components/ui/spinner'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { combineBillingExpr } from '@/features/pricing/lib/billing-expr'
import { useMediaQuery } from '@/hooks'

import {
  getModelPricingLocks,
  updateModelPricingLock,
  updateModelPricingLocks,
} from '../api'
import { safeJsonParse } from '../utils/json-parser'
import type { PricingMode } from './model-pricing-core'
import { normalizeLockedModels } from './model-pricing-locks'
import {
  ModelPricingEditorPanel,
  type ModelPricingEditorPanelHandle,
  ModelPricingSheet,
  type ModelRatioData,
} from './model-pricing-sheet'
import {
  buildModelSnapshots,
  getSnapshotSignature,
  isBasePricingUnset,
  type ModelRow,
} from './model-pricing-snapshots'
import { buildModelRatioColumns } from './model-ratio-table-columns'

type ModelRatioVisualEditorProps = {
  savedModelPrice: string
  savedModelRatio: string
  savedCacheRatio: string
  savedCreateCacheRatio: string
  savedCompletionRatio: string
  savedImageRatio: string
  savedAudioRatio: string
  savedAudioCompletionRatio: string
  savedBillingMode: string
  savedBillingExpr: string
  modelPrice: string
  modelRatio: string
  cacheRatio: string
  createCacheRatio: string
  completionRatio: string
  imageRatio: string
  audioRatio: string
  audioCompletionRatio: string
  billingMode: string
  billingExpr: string
  candidateModelNames?: string[]
  candidateModelsLoading?: boolean
  filterMode?: 'all' | 'unset'
  onChange: (field: string, value: string) => void
  onSave: () => void | Promise<void>
  isSaving: boolean
}

export type ModelRatioVisualEditorHandle = {
  commitOpenEditor: () => Promise<boolean>
}

const STORAGE_KEY = 'model-ratio-column-visibility'
const MODEL_PRICING_LOCKS_QUERY_KEY = ['model-pricing-locks'] as const

const ModelRatioVisualEditorComponent = forwardRef<
  ModelRatioVisualEditorHandle,
  ModelRatioVisualEditorProps
>(function ModelRatioVisualEditor(
  {
    savedModelPrice,
    savedModelRatio,
    savedCacheRatio,
    savedCreateCacheRatio,
    savedCompletionRatio,
    savedImageRatio,
    savedAudioRatio,
    savedAudioCompletionRatio,
    savedBillingMode,
    savedBillingExpr,
    modelPrice,
    modelRatio,
    cacheRatio,
    createCacheRatio,
    completionRatio,
    imageRatio,
    audioRatio,
    audioCompletionRatio,
    billingMode,
    billingExpr,
    candidateModelNames,
    candidateModelsLoading,
    filterMode = 'all',
    onChange,
    onSave,
    isSaving,
  },
  ref
) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const isMobile = useMediaQuery('(max-width: 767px)')
  const [sheetOpen, setSheetOpen] = useState(false)
  const [editorOpen, setEditorOpen] = useState(false)
  const [editData, setEditData] = useState<ModelRatioData | null>(null)
  const [editorDirty, setEditorDirty] = useState(false)
  const [pendingLockModel, setPendingLockModel] = useState<string>()
  const [pendingBatchLock, setPendingBatchLock] = useState<boolean>()
  const [sorting, setSorting] = useState<SortingState>([])
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([])
  const [globalFilter, setGlobalFilter] = useState('')
  const [rowSelection, setRowSelection] = useState<RowSelectionState>({})
  const editorPanelRef = useRef<ModelPricingEditorPanelHandle>(null)
  const lockOperationInFlightRef = useRef(false)
  const locksQuery = useQuery({
    queryKey: MODEL_PRICING_LOCKS_QUERY_KEY,
    queryFn: getModelPricingLocks,
  })
  const lockedModels = useMemo(
    () => new Set(normalizeLockedModels(locksQuery.data?.data.locked_models)),
    [locksQuery.data?.data.locked_models]
  )
  const lockStateUnavailable = locksQuery.isPending || locksQuery.isError
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: 20,
  })
  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>(
    () => {
      const saved = localStorage.getItem(STORAGE_KEY)
      if (saved) {
        try {
          return safeJsonParse<VisibilityState>(saved, {
            fallback: {
              cacheRatio: false,
              createCacheRatio: false,
              imageRatio: false,
              audioRatio: false,
              audioCompletionRatio: false,
            },
            silent: true,
          })
        } catch {
          return {
            cacheRatio: false,
            createCacheRatio: false,
            imageRatio: false,
            audioRatio: false,
            audioCompletionRatio: false,
          }
        }
      }
      return {
        cacheRatio: false,
        createCacheRatio: false,
        imageRatio: false,
        audioRatio: false,
        audioCompletionRatio: false,
      }
    }
  )

  useEffect(() => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(columnVisibility))
  }, [columnVisibility])

  const models = useMemo(() => {
    const savedRows = buildModelSnapshots({
      modelPrice: savedModelPrice,
      modelRatio: savedModelRatio,
      cacheRatio: savedCacheRatio,
      createCacheRatio: savedCreateCacheRatio,
      completionRatio: savedCompletionRatio,
      imageRatio: savedImageRatio,
      audioRatio: savedAudioRatio,
      audioCompletionRatio: savedAudioCompletionRatio,
      billingMode: savedBillingMode,
      billingExpr: savedBillingExpr,
    })
    const draftRows = buildModelSnapshots({
      modelPrice,
      modelRatio,
      cacheRatio,
      createCacheRatio,
      completionRatio,
      imageRatio,
      audioRatio,
      audioCompletionRatio,
      billingMode,
      billingExpr,
    })

    const savedByName = new Map(savedRows.map((row) => [row.name, row]))
    const draftByName = new Map(draftRows.map((row) => [row.name, row]))
    const modelNames =
      filterMode === 'unset'
        ? new Set(candidateModelNames ?? [])
        : new Set([...savedByName.keys(), ...draftByName.keys()])

    return [...modelNames]
      .map((name) => {
        const saved = savedByName.get(name)
        const draft = draftByName.get(name)
        const displayed = saved ??
          draft ?? { name, billingMode: 'per-token', hasConflict: false }
        const savedSignature = getSnapshotSignature(saved)
        const draftSignature = getSnapshotSignature(draft)

        return {
          ...displayed,
          saved,
          draft,
          isDraftChanged: savedSignature !== draftSignature,
          isDraftDeleted: Boolean(saved && !draft),
          isDraftNew: Boolean(!saved && draft),
        }
      })
      .filter((row) => !row.isDraftDeleted)
      .filter((row) => filterMode !== 'unset' || isBasePricingUnset(row.saved))
      .sort((a, b) => a.name.localeCompare(b.name))
  }, [
    candidateModelNames,
    filterMode,
    savedModelPrice,
    savedModelRatio,
    savedCacheRatio,
    savedCreateCacheRatio,
    savedCompletionRatio,
    savedImageRatio,
    savedAudioRatio,
    savedAudioCompletionRatio,
    savedBillingMode,
    savedBillingExpr,
    modelPrice,
    modelRatio,
    cacheRatio,
    createCacheRatio,
    completionRatio,
    imageRatio,
    audioRatio,
    audioCompletionRatio,
    billingMode,
    billingExpr,
  ])

  const modeCounts = useMemo(
    () =>
      models.reduce(
        (acc, model) => {
          const mode =
            model.billingMode === 'per-request' ||
            model.billingMode === 'tiered_expr'
              ? model.billingMode
              : 'per-token'
          acc[mode] += 1
          return acc
        },
        {
          'per-token': 0,
          'per-request': 0,
          tiered_expr: 0,
        } as Record<'per-token' | 'per-request' | 'tiered_expr', number>
      ),
    [models]
  )

  const handleEdit = useCallback(
    (model: ModelRow) => {
      const editableModel = model.draft ?? model.saved ?? model
      let editBillingMode: PricingMode = 'per-token'
      if (editableModel.billingMode === 'tiered_expr') {
        editBillingMode = 'tiered_expr'
      } else if (editableModel.price && editableModel.price !== '') {
        editBillingMode = 'per-request'
      }
      setEditData({
        name: editableModel.name,
        price: editableModel.price,
        ratio: editableModel.ratio,
        cacheRatio: editableModel.cacheRatio,
        createCacheRatio: editableModel.createCacheRatio,
        completionRatio: editableModel.completionRatio,
        imageRatio: editableModel.imageRatio,
        audioRatio: editableModel.audioRatio,
        audioCompletionRatio: editableModel.audioCompletionRatio,
        billingMode: editBillingMode,
        billingExpr: editableModel.billingExpr,
        requestRuleExpr: editableModel.requestRuleExpr,
      })
      setEditorDirty(false)
      setEditorOpen(true)
      if (isMobile) setSheetOpen(true)
    },
    [isMobile]
  )

  const handleAdd = useCallback(() => {
    setEditData(null)
    setEditorDirty(false)
    setEditorOpen(true)
    if (isMobile) setSheetOpen(true)
  }, [isMobile])

  const handleGlobalFilterChange = useCallback<OnChangeFn<string>>(
    (updater) => {
      setGlobalFilter((previous) => {
        const next = typeof updater === 'function' ? updater(previous) : updater
        if (next !== previous) {
          setEditData(null)
          setEditorDirty(false)
          setEditorOpen(false)
          setSheetOpen(false)
        }
        return next
      })
    },
    []
  )

  const removeModel = useCallback(
    (name: string) => {
      const priceMap = safeJsonParse<Record<string, number>>(modelPrice, {
        fallback: {},
        silent: true,
      })
      const ratioMap = safeJsonParse<Record<string, number>>(modelRatio, {
        fallback: {},
        silent: true,
      })
      const cacheMap = safeJsonParse<Record<string, number>>(cacheRatio, {
        fallback: {},
        silent: true,
      })
      const createCacheMap = safeJsonParse<Record<string, number>>(
        createCacheRatio,
        { fallback: {}, silent: true }
      )
      const completionMap = safeJsonParse<Record<string, number>>(
        completionRatio,
        { fallback: {}, silent: true }
      )
      const imageMap = safeJsonParse<Record<string, number>>(imageRatio, {
        fallback: {},
        silent: true,
      })
      const audioMap = safeJsonParse<Record<string, number>>(audioRatio, {
        fallback: {},
        silent: true,
      })
      const audioCompletionMap = safeJsonParse<Record<string, number>>(
        audioCompletionRatio,
        { fallback: {}, silent: true }
      )
      const billingModeMap = safeJsonParse<Record<string, string>>(
        billingMode,
        { fallback: {}, silent: true }
      )
      const billingExprMap = safeJsonParse<Record<string, string>>(
        billingExpr,
        { fallback: {}, silent: true }
      )

      delete priceMap[name]
      delete ratioMap[name]
      delete cacheMap[name]
      delete createCacheMap[name]
      delete completionMap[name]
      delete imageMap[name]
      delete audioMap[name]
      delete audioCompletionMap[name]
      delete billingModeMap[name]
      delete billingExprMap[name]

      onChange('ModelPrice', JSON.stringify(priceMap, null, 2))
      onChange('ModelRatio', JSON.stringify(ratioMap, null, 2))
      onChange('CacheRatio', JSON.stringify(cacheMap, null, 2))
      onChange('CreateCacheRatio', JSON.stringify(createCacheMap, null, 2))
      onChange('CompletionRatio', JSON.stringify(completionMap, null, 2))
      onChange('ImageRatio', JSON.stringify(imageMap, null, 2))
      onChange('AudioRatio', JSON.stringify(audioMap, null, 2))
      onChange(
        'AudioCompletionRatio',
        JSON.stringify(audioCompletionMap, null, 2)
      )
      onChange(
        'billing_setting.billing_mode',
        JSON.stringify(billingModeMap, null, 2)
      )
      onChange(
        'billing_setting.billing_expr',
        JSON.stringify(billingExprMap, null, 2)
      )

      if (editData?.name === name) {
        setEditData(null)
        setEditorDirty(false)
        setEditorOpen(false)
        setSheetOpen(false)
      }
    },
    [
      modelPrice,
      modelRatio,
      cacheRatio,
      createCacheRatio,
      completionRatio,
      imageRatio,
      audioRatio,
      audioCompletionRatio,
      billingMode,
      billingExpr,
      onChange,
      editData,
    ]
  )

  const hasUnsavedPricingChanges =
    editorDirty || models.some((model) => model.isDraftChanged)
  const lockOperationPending =
    pendingLockModel !== undefined || pendingBatchLock !== undefined

  const handleToggleLock = useCallback(
    async (name: string, locked: boolean) => {
      if (lockStateUnavailable || lockOperationInFlightRef.current) return
      if (locked && hasUnsavedPricingChanges) {
        toast.warning(t('Save price changes before locking'))
        return
      }
      lockOperationInFlightRef.current = true
      setPendingLockModel(name)
      try {
        const response = await updateModelPricingLock({
          model_name: name,
          locked,
        })
        if (!response.success) {
          throw new Error(response.message || t('Failed to update price lock'))
        }
        await queryClient.cancelQueries({
          queryKey: MODEL_PRICING_LOCKS_QUERY_KEY,
        })
        queryClient.setQueryData(MODEL_PRICING_LOCKS_QUERY_KEY, response)
        queryClient.invalidateQueries({
          queryKey: MODEL_PRICING_LOCKS_QUERY_KEY,
        })
        toast.success(
          locked
            ? t('Price locked successfully')
            : t('Price unlocked successfully')
        )
      } catch (error) {
        toast.error(
          error instanceof Error
            ? error.message
            : t('Failed to update price lock')
        )
      } finally {
        lockOperationInFlightRef.current = false
        setPendingLockModel(undefined)
      }
    },
    [hasUnsavedPricingChanges, lockStateUnavailable, queryClient, t]
  )

  const handleDelete = useCallback(
    async (name: string) => {
      if (lockStateUnavailable || lockOperationInFlightRef.current) return
      if (lockedModels.has(name)) {
        lockOperationInFlightRef.current = true
        setPendingLockModel(name)
        try {
          const response = await updateModelPricingLock({
            model_name: name,
            locked: false,
          })
          if (!response.success) {
            throw new Error(response.message || t('Failed to unlock price'))
          }
          await queryClient.cancelQueries({
            queryKey: MODEL_PRICING_LOCKS_QUERY_KEY,
          })
          queryClient.setQueryData(MODEL_PRICING_LOCKS_QUERY_KEY, response)
          queryClient.invalidateQueries({
            queryKey: MODEL_PRICING_LOCKS_QUERY_KEY,
          })
        } catch (error) {
          toast.error(
            error instanceof Error ? error.message : t('Failed to unlock price')
          )
          return
        } finally {
          lockOperationInFlightRef.current = false
          setPendingLockModel(undefined)
        }
      }
      removeModel(name)
    },
    [lockedModels, lockStateUnavailable, queryClient, removeModel, t]
  )

  const columns = useMemo(
    () =>
      buildModelRatioColumns({
        onDelete: handleDelete,
        onEdit: handleEdit,
        onToggleLock: handleToggleLock,
        lockedModels,
        pendingLockModel,
        lockPending: lockOperationPending,
        lockDisabled: hasUnsavedPricingChanges,
        lockStateUnavailable,
        lockStateLoading: locksQuery.isPending,
        deleteDisabled: filterMode === 'unset',
        t,
      }),
    [
      handleEdit,
      handleDelete,
      handleToggleLock,
      hasUnsavedPricingChanges,
      lockedModels,
      pendingLockModel,
      lockOperationPending,
      lockStateUnavailable,
      locksQuery.isPending,
      filterMode,
      t,
    ]
  )

  const ensurePageInRange = useCallback((pageCount: number) => {
    setPagination((prev) =>
      pageCount > 0 && prev.pageIndex >= pageCount
        ? { ...prev, pageIndex: pageCount - 1 }
        : prev
    )
  }, [])

  const { table } = useDataTable({
    data: models,
    columns,
    getRowId: (row) => row.name,
    ensurePageInRange,
    sorting,
    columnFilters,
    globalFilter,
    columnVisibility,
    pagination,
    rowSelection,
    enableRowSelection: true,
    onSortingChange: setSorting,
    onColumnFiltersChange: setColumnFilters,
    onGlobalFilterChange: handleGlobalFilterChange,
    onColumnVisibilityChange: setColumnVisibility,
    onPaginationChange: setPagination,
    onRowSelectionChange: setRowSelection,
    autoResetPageIndex: false,
    globalFilterFn: (row, _columnId, filterValue) => {
      const searchValue = String(filterValue).toLowerCase()
      return row.original.name.toLowerCase().includes(searchValue)
    },
  })

  const persistPricingData = useCallback(
    (data: ModelRatioData, targetNames: string[] = [data.name]) => {
      const priceMap = safeJsonParse<Record<string, number>>(modelPrice, {
        fallback: {},
        silent: true,
      })
      const ratioMap = safeJsonParse<Record<string, number>>(modelRatio, {
        fallback: {},
        silent: true,
      })
      const cacheMap = safeJsonParse<Record<string, number>>(cacheRatio, {
        fallback: {},
        silent: true,
      })
      const createCacheMap = safeJsonParse<Record<string, number>>(
        createCacheRatio,
        { fallback: {}, silent: true }
      )
      const completionMap = safeJsonParse<Record<string, number>>(
        completionRatio,
        { fallback: {}, silent: true }
      )
      const imageMap = safeJsonParse<Record<string, number>>(imageRatio, {
        fallback: {},
        silent: true,
      })
      const audioMap = safeJsonParse<Record<string, number>>(audioRatio, {
        fallback: {},
        silent: true,
      })
      const audioCompletionMap = safeJsonParse<Record<string, number>>(
        audioCompletionRatio,
        { fallback: {}, silent: true }
      )
      const billingModeMap = safeJsonParse<Record<string, string>>(
        billingMode,
        { fallback: {}, silent: true }
      )
      const billingExprMap = safeJsonParse<Record<string, string>>(
        billingExpr,
        { fallback: {}, silent: true }
      )

      const setIfPresent = (
        target: Record<string, number>,
        name: string,
        value: string | undefined
      ) => {
        if (!value || value === '') return
        const parsed = parseFloat(value)
        if (Number.isFinite(parsed)) target[name] = parsed
      }

      targetNames.forEach((name) => {
        delete priceMap[name]
        delete ratioMap[name]
        delete cacheMap[name]
        delete createCacheMap[name]
        delete completionMap[name]
        delete imageMap[name]
        delete audioMap[name]
        delete audioCompletionMap[name]
        delete billingModeMap[name]
        delete billingExprMap[name]

        if (data.billingMode === 'tiered_expr') {
          const combined = combineBillingExpr(
            data.billingExpr || '',
            data.requestRuleExpr || ''
          )
          if (combined) {
            billingModeMap[name] = 'tiered_expr'
            billingExprMap[name] = combined
          }
          // Always serialize ratio/price values for tiered_expr models so they
          // serve as fallback during multi-instance sync delays. The backend's
          // ModelPriceHelper checks billing_mode first, so these values are
          // only consulted when billing_setting hasn't propagated yet.
          setIfPresent(priceMap, name, data.price)
          setIfPresent(ratioMap, name, data.ratio)
          setIfPresent(cacheMap, name, data.cacheRatio)
          setIfPresent(createCacheMap, name, data.createCacheRatio)
          setIfPresent(completionMap, name, data.completionRatio)
          setIfPresent(imageMap, name, data.imageRatio)
          setIfPresent(audioMap, name, data.audioRatio)
          setIfPresent(audioCompletionMap, name, data.audioCompletionRatio)
        } else if (data.price && data.price !== '') {
          setIfPresent(priceMap, name, data.price)
        } else {
          setIfPresent(ratioMap, name, data.ratio)
          setIfPresent(cacheMap, name, data.cacheRatio)
          setIfPresent(createCacheMap, name, data.createCacheRatio)
          setIfPresent(completionMap, name, data.completionRatio)
          setIfPresent(imageMap, name, data.imageRatio)
          setIfPresent(audioMap, name, data.audioRatio)
          setIfPresent(audioCompletionMap, name, data.audioCompletionRatio)
        }
      })

      onChange('ModelPrice', JSON.stringify(priceMap, null, 2))
      onChange('ModelRatio', JSON.stringify(ratioMap, null, 2))
      onChange('CacheRatio', JSON.stringify(cacheMap, null, 2))
      onChange('CreateCacheRatio', JSON.stringify(createCacheMap, null, 2))
      onChange('CompletionRatio', JSON.stringify(completionMap, null, 2))
      onChange('ImageRatio', JSON.stringify(imageMap, null, 2))
      onChange('AudioRatio', JSON.stringify(audioMap, null, 2))
      onChange(
        'AudioCompletionRatio',
        JSON.stringify(audioCompletionMap, null, 2)
      )
      onChange(
        'billing_setting.billing_mode',
        JSON.stringify(billingModeMap, null, 2)
      )
      onChange(
        'billing_setting.billing_expr',
        JSON.stringify(billingExprMap, null, 2)
      )
    },
    [
      modelPrice,
      modelRatio,
      cacheRatio,
      createCacheRatio,
      completionRatio,
      imageRatio,
      audioRatio,
      audioCompletionRatio,
      billingMode,
      billingExpr,
      onChange,
    ]
  )

  const handleBatchCopy = useCallback(async () => {
    if (!editData) {
      toast.error(t('Open a source model first'))
      return
    }

    let sourceData = editData
    if (editorOpen && editorPanelRef.current) {
      const committed = await editorPanelRef.current.commitDraft()
      if (!committed) return
      sourceData = committed
      setEditData(committed)
    }

    const targetNames = table
      .getFilteredSelectedRowModel()
      .rows.map((row) => row.original.name)

    if (targetNames.length === 0) {
      toast.error(t('Select at least one target model'))
      return
    }

    // Persist to the source model too, so targets never carry pricing the
    // source itself would lose if the editor draft were abandoned.
    persistPricingData(sourceData, [
      ...new Set([sourceData.name, ...targetNames]),
    ])
    table.resetRowSelection()
    toast.success(
      t('Applied {{name}} pricing to {{count}} models', {
        name: sourceData.name,
        count: targetNames.length,
      })
    )
  }, [editData, editorOpen, persistPricingData, t, table])

  const handleBatchLockChange = useCallback(
    async (locked: boolean) => {
      if (lockStateUnavailable || lockOperationInFlightRef.current) return
      if (locked && hasUnsavedPricingChanges) {
        toast.warning(t('Save price changes before locking'))
        return
      }

      const modelNames = table
        .getFilteredSelectedRowModel()
        .rows.map((row) => row.original.name)
      if (modelNames.length === 0) return

      lockOperationInFlightRef.current = true
      setPendingBatchLock(locked)
      try {
        const response = await updateModelPricingLocks({
          model_names: modelNames,
          locked,
        })
        await queryClient.cancelQueries({
          queryKey: MODEL_PRICING_LOCKS_QUERY_KEY,
        })
        queryClient.setQueryData(MODEL_PRICING_LOCKS_QUERY_KEY, response)
        queryClient.invalidateQueries({
          queryKey: MODEL_PRICING_LOCKS_QUERY_KEY,
        })
        const message = locked
          ? t('Locked {{changed}} of {{total}} selected model prices', {
              changed: response.data.changed_models.length,
              total: modelNames.length,
            })
          : t('Unlocked {{changed}} of {{total}} selected model prices', {
              changed: response.data.changed_models.length,
              total: modelNames.length,
            })
        toast.success(message)
        table.resetRowSelection()
      } catch (error) {
        toast.error(
          error instanceof Error
            ? error.message
            : t('Failed to update price lock')
        )
      } finally {
        lockOperationInFlightRef.current = false
        setPendingBatchLock(undefined)
      }
    },
    [hasUnsavedPricingChanges, lockStateUnavailable, queryClient, t, table]
  )

  let batchLockTooltip = t('Lock selected prices')
  let batchUnlockTooltip = t('Unlock selected prices')
  if (locksQuery.isPending || lockOperationPending) {
    batchLockTooltip = t('Loading...')
    batchUnlockTooltip = t('Loading...')
  } else if (locksQuery.isError) {
    batchLockTooltip = t('Failed to load price locks')
    batchUnlockTooltip = t('Failed to load price locks')
  } else if (hasUnsavedPricingChanges) {
    batchLockTooltip = t('Save price changes before locking')
  }

  useImperativeHandle(
    ref,
    () => ({
      commitOpenEditor: async () => {
        if (!editorOpen || !editorPanelRef.current) return true
        const data = await editorPanelRef.current.commitDraft()
        if (!data) return false
        persistPricingData(data)
        setEditData(data)
        return true
      },
    }),
    [editorOpen, persistPricingData]
  )

  const hasRows = table.getRowModel().rows.length > 0

  let emptyStateText = t('No models configured. Use Add model to get started.')
  if (table.getState().globalFilter) {
    emptyStateText = t('No models match your search')
  } else if (filterMode === 'unset') {
    emptyStateText = candidateModelsLoading
      ? t('Loading...')
      : t('No models with unset prices')
  }

  return (
    <div className='flex flex-col gap-4'>
      <div className='grid h-[clamp(720px,calc(100vh-12rem),900px)] min-h-0 gap-4 md:grid-cols-[minmax(300px,0.72fr)_minmax(520px,1.28fr)] xl:grid-cols-[minmax(320px,1fr)_minmax(640px,1fr)]'>
        <div className='flex min-h-0 min-w-0 flex-col gap-3'>
          <DataTableToolbar
            table={table}
            searchPlaceholder={t('Search models...')}
            className={
              table.getState().globalFilter ||
              table.getState().columnFilters.length > 0
                ? '[&>input]:min-w-40 [&>input]:flex-1'
                : undefined
            }
            filters={[
              {
                columnId: 'billingMode',
                title: t('Mode'),
                options: [
                  {
                    label: 'Per-token',
                    value: 'per-token',
                    count: modeCounts['per-token'],
                  },
                  {
                    label: 'Per-request',
                    value: 'per-request',
                    count: modeCounts['per-request'],
                  },
                  {
                    label: 'Expression',
                    value: 'tiered_expr',
                    count: modeCounts.tiered_expr,
                  },
                ],
              },
            ]}
            preActions={
              filterMode === 'unset' ? undefined : (
                <Button onClick={handleAdd}>
                  <Plus data-icon='inline-start' />
                  {t('Add model')}
                </Button>
              )
            }
          />

          {!hasRows ? (
            <div className='text-muted-foreground rounded-lg border border-dashed p-8 text-center'>
              {emptyStateText}
            </div>
          ) : (
            <DataTableView
              table={table}
              containerClassName='min-h-0 flex-1 rounded-md'
              tableContainerClassName='h-full'
              tableClassName='min-w-[852px] table-fixed'
              tableHeaderClassName='[&_tr]:border-b-0'
              splitHeaderScrollClassName='h-full'
              bodyContainerClassName='[scrollbar-gutter:stable]'
              splitHeader
              pinnedColumns={[
                {
                  columnId: 'actions',
                  side: 'right',
                },
              ]}
              colgroup={
                <colgroup>
                  <col className='w-9' />
                  <col className='w-[300px]' />
                  <col className='w-[120px]' />
                  <col className='w-[300px]' />
                  <col className='w-auto' />
                </colgroup>
              }
              renderRow={(row, { getCellClassName }) => (
                <DataTableRow
                  key={row.id}
                  row={row}
                  className={
                    editData?.name === row.original.name
                      ? 'bg-muted/45 hover:bg-muted/50 data-[state=selected]:bg-muted group'
                      : 'group'
                  }
                  getColumnClassName={(columnId) =>
                    columnId === 'actions' &&
                    editData?.name === row.original.name
                      ? getCellClassName(columnId, 'bg-muted')
                      : getCellClassName(columnId)
                  }
                  onClick={(event) => {
                    const target = event.target as HTMLElement
                    if (target.closest('button, [role="checkbox"]')) return
                    handleEdit(row.original)
                  }}
                />
              )}
            />
          )}

          {hasRows && <DataTablePagination table={table} />}
        </div>

        <div className='hidden min-h-0 min-w-0 md:block'>
          {editorOpen ? (
            <ModelPricingEditorPanel
              ref={editorPanelRef}
              editData={editData}
              onSave={onSave}
              isSaving={isSaving}
              onDirtyChange={setEditorDirty}
              className='h-full min-h-0'
            />
          ) : (
            <div className='bg-card text-muted-foreground flex h-full min-h-0 flex-col items-center justify-center gap-3 rounded-xl border border-dashed p-6 text-center'>
              <div className='text-foreground text-base font-medium'>
                {t('Select a model to edit pricing')}
              </div>
              <p className='max-w-sm text-sm'>
                {t(
                  'Use the full-width table to scan prices, then select a row to edit it here.'
                )}
              </p>
              {filterMode !== 'unset' && (
                <Button variant='outline' onClick={handleAdd}>
                  <Plus data-icon='inline-start' />
                  {t('Add model')}
                </Button>
              )}
            </div>
          )}
        </div>
      </div>

      <DataTableBulkActions table={table} entityName={t('model')}>
        <Tooltip>
          <TooltipTrigger render={<span className='inline-flex' />}>
            <Button
              variant='outline'
              size='sm'
              disabled={
                lockStateUnavailable ||
                lockOperationPending ||
                hasUnsavedPricingChanges
              }
              onClick={() => handleBatchLockChange(true)}
              title={batchLockTooltip}
              aria-label={t('Lock selected prices')}
              aria-busy={pendingBatchLock === true}
            >
              {pendingBatchLock === true ? (
                <Spinner data-icon='inline-start' aria-hidden='true' />
              ) : (
                <HugeiconsIcon
                  icon={PowerIcon}
                  strokeWidth={2}
                  data-icon='inline-start'
                  aria-hidden='true'
                />
              )}
              <span className='hidden sm:inline'>
                {t('Lock selected prices')}
              </span>
            </Button>
          </TooltipTrigger>
          <TooltipContent>{batchLockTooltip}</TooltipContent>
        </Tooltip>
        <Tooltip>
          <TooltipTrigger render={<span className='inline-flex' />}>
            <Button
              variant='outline'
              size='sm'
              disabled={lockStateUnavailable || lockOperationPending}
              onClick={() => handleBatchLockChange(false)}
              title={batchUnlockTooltip}
              aria-label={t('Unlock selected prices')}
              aria-busy={pendingBatchLock === false}
            >
              {pendingBatchLock === false ? (
                <Spinner data-icon='inline-start' aria-hidden='true' />
              ) : (
                <HugeiconsIcon
                  icon={PowerOffIcon}
                  strokeWidth={2}
                  data-icon='inline-start'
                  aria-hidden='true'
                />
              )}
              <span className='hidden sm:inline'>
                {t('Unlock selected prices')}
              </span>
            </Button>
          </TooltipTrigger>
          <TooltipContent>{batchUnlockTooltip}</TooltipContent>
        </Tooltip>
        <Button size='sm' disabled={!editData} onClick={handleBatchCopy}>
          <Copy data-icon='inline-start' />
          {editData
            ? t('Copy {{name}} pricing', { name: editData.name })
            : t('Open a source model first')}
        </Button>
      </DataTableBulkActions>

      {isMobile && (
        <ModelPricingSheet
          ref={editorPanelRef}
          open={sheetOpen}
          onOpenChange={setSheetOpen}
          editData={editData}
          onSave={onSave}
          isSaving={isSaving}
          onDirtyChange={setEditorDirty}
        />
      )}
    </div>
  )
})

export const ModelRatioVisualEditor = memo(
  ModelRatioVisualEditorComponent,
  // Custom equality check - only re-render if JSON props actually changed
  (prevProps, nextProps) => {
    return (
      prevProps.savedModelPrice === nextProps.savedModelPrice &&
      prevProps.savedModelRatio === nextProps.savedModelRatio &&
      prevProps.savedCacheRatio === nextProps.savedCacheRatio &&
      prevProps.savedCreateCacheRatio === nextProps.savedCreateCacheRatio &&
      prevProps.savedCompletionRatio === nextProps.savedCompletionRatio &&
      prevProps.savedImageRatio === nextProps.savedImageRatio &&
      prevProps.savedAudioRatio === nextProps.savedAudioRatio &&
      prevProps.savedAudioCompletionRatio ===
        nextProps.savedAudioCompletionRatio &&
      prevProps.savedBillingMode === nextProps.savedBillingMode &&
      prevProps.savedBillingExpr === nextProps.savedBillingExpr &&
      prevProps.modelPrice === nextProps.modelPrice &&
      prevProps.modelRatio === nextProps.modelRatio &&
      prevProps.cacheRatio === nextProps.cacheRatio &&
      prevProps.createCacheRatio === nextProps.createCacheRatio &&
      prevProps.completionRatio === nextProps.completionRatio &&
      prevProps.imageRatio === nextProps.imageRatio &&
      prevProps.audioRatio === nextProps.audioRatio &&
      prevProps.audioCompletionRatio === nextProps.audioCompletionRatio &&
      prevProps.billingMode === nextProps.billingMode &&
      prevProps.billingExpr === nextProps.billingExpr &&
      prevProps.candidateModelNames === nextProps.candidateModelNames &&
      prevProps.candidateModelsLoading === nextProps.candidateModelsLoading &&
      prevProps.filterMode === nextProps.filterMode &&
      prevProps.onChange === nextProps.onChange &&
      prevProps.onSave === nextProps.onSave &&
      prevProps.isSaving === nextProps.isSaving
    )
  }
)
