/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/

const MULTI_SELECT_SEPARATOR_REGEX = /[,，\r\n]/

export function parseMultiSelectPaste(value: string): string[] | null {
  if (!MULTI_SELECT_SEPARATOR_REGEX.test(value)) return null
  return value
    .replaceAll('，', ',')
    .split(/[,\r\n]+/)
    .map((item) => item.trim())
    .filter(Boolean)
}
