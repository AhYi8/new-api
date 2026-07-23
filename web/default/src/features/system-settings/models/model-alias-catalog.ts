/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/

export function normalizeProviderModelNames(models: readonly string[]) {
  return [
    ...new Set(models.map((modelName) => modelName.trim()).filter(Boolean)),
  ]
}

export function findProviderModelsMissingFromCatalog(
  selectedModels: readonly string[],
  catalogModels: readonly string[],
  catalogIsCurrent: boolean
) {
  if (!catalogIsCurrent) return []
  const catalogSet = new Set(catalogModels)
  return selectedModels.filter((modelName) => !catalogSet.has(modelName))
}
