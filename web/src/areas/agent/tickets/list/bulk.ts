// SPDX-License-Identifier: AGPL-3.0-only

/** Per-ticket outcome of a bulk action (FE-038). */
export interface BulkResult {
  id: string
  number: number
  ok: boolean
  error?: string
}
