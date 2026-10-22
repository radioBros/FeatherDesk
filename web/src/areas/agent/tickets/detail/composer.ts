// SPDX-License-Identifier: AGPL-3.0-only

/** Composer helpers (FE-039): canned-response variables and @mentions. */

/** Replaces `{{customer.name}}`-style variables (alias `{{requester.name}}`); unknown ones stay as typed. */
export function fillVars(body: string, vars: Record<string, string>): string {
  return body.replace(/\{\{\s*([\w.]+)\s*\}\}/g, (m, k: string) => (k in vars ? vars[k]! : m))
}

/** The `@partial` being typed right before the caret, if any. */
export function mentionAt(text: string, caret: number): string | null {
  const before = text.slice(0, caret)
  const m = /(?:^|\s)@([\p{L}\p{N}._-]{1,30})$/u.exec(before)
  return m ? m[1]! : null
}
