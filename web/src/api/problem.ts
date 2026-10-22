// SPDX-License-Identifier: AGPL-3.0-only

import { validations } from '@/constants/validations'
import { t } from '@/i18n'

export interface ProblemFieldError {
  field: string
  code: string
  message?: string
  params?: Record<string, unknown>
}

export interface ProblemBody {
  type?: string
  title?: string
  status?: number
  detail?: string
  instance?: string
  errors?: ProblemFieldError[]
  allowed?: string[]
  [k: string]: unknown
}

/** A parsed RFC 9457 problem (or a network failure, status 0). */
export class ApiProblem extends Error {
  readonly status: number
  readonly type: string
  readonly title: string
  readonly detail?: string
  readonly errors: ProblemFieldError[]
  readonly body: ProblemBody

  constructor(status: number, body: ProblemBody = {}) {
    super(body.title || body.detail || `HTTP ${status}`)
    this.name = 'ApiProblem'
    this.status = status
    this.body = body
    this.type = body.type ?? 'about:blank'
    this.title = body.title ?? ''
    this.detail = body.detail
    this.errors = Array.isArray(body.errors) ? body.errors : []
  }

  /** The stable slug at the end of the problem type URI (`invalid-transition`, …). */
  get slug(): string {
    return this.type.split('/').pop() ?? ''
  }

  /** A user-facing message in the current locale. */
  get userMessage(): string {
    switch (this.status) {
      case 0:
        return t('errors.network')
      case 401:
        return t('errors.unauthenticated')
      case 403:
        return this.title || t('errors.forbidden')
      case 404:
        return t('errors.notFound')
      case 409:
      case 412:
        return this.detail || this.title || t('errors.conflict')
      case 422:
        return t('errors.validation')
      case 429:
        return t('errors.rateLimited')
      default:
        return this.title || t('errors.generic')
    }
  }
}

export function isProblem(e: unknown): e is ApiProblem {
  return e instanceof ApiProblem
}

/**
 * Turns a 422 problem into `{field: [message]}` for `:error-messages`, using the
 * same translated messages as the client-side rules (validations.fromServer).
 */
export function fieldErrorsFrom(problem: ApiProblem): Record<string, string[]> {
  const out: Record<string, string[]> = {}
  for (const e of problem.errors) {
    const m = validations.fromServer(e.code, e.params)
    ;(out[e.field] ??= []).push(m)
  }
  return out
}

/** Writes server field errors into a reactive record (clearing previous ones). */
export function applyServerErrors(target: Record<string, string[]>, problem: ApiProblem): boolean {
  for (const k of Object.keys(target)) delete target[k]
  const errs = fieldErrorsFrom(problem)
  Object.assign(target, errs)
  return Object.keys(errs).length > 0
}
