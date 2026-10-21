// SPDX-License-Identifier: AGPL-3.0-only

/**
 * State of the open ticket (FE-022…FE-024, FE-045): the ticket, its ETag, and
 * the outcome of every mutation (lost visibility, 409 invalid transition,
 * 412 changed by someone else).
 */
import { ref, shallowRef } from 'vue'
import { ApiProblem } from '@/api/problem'
import { useUiStore } from '@/stores/ui'
import { t } from '@/i18n'
import * as ticketApi from './ticketApi'
import type { components } from '@/api/schema'

type S = components['schemas']
export type Outcome = 'ok' | 'lost' | 'failed'

export interface TransitionConflict {
  allowed: S['TicketStatus'][]
  /** Called with the status the agent picked from the allowed ones (or null to skip the status change). */
  pick: (status: S['TicketStatus'] | null) => void
  /** Composer send only: "send without changing the status". */
  skip?: () => void
}

export function useTicketDetail() {
  const ui = useUiStore()
  const ticket = ref<S['Ticket'] | null>(null)
  const etag = ref<string | null>(null)
  const loading = ref(false)
  const notFound = ref(false)
  /** 409 invalid-transition: the dialog with the allowed statuses. */
  const transition = shallowRef<TransitionConflict | null>(null)
  /** 412: the "changed by someone else" dialog (FE-045). */
  const stale = ref(false)
  let refetchTimer: ReturnType<typeof setTimeout> | null = null

  /** Loads by global number, or by project ref (`WEB-12`) when given a string. */
  async function load(key: number | string) {
    loading.value = true
    notFound.value = false
    try {
      const res = typeof key === 'string' ? await ticketApi.getByRef(key) : await ticketApi.getByNumber(key)
      ticket.value = res.ticket
      etag.value = res.etag
      // `by-number` carries no ETag; fetch it by id so the first PATCH can send If-Match.
      if (!res.etag) void refreshEtag()
    } catch (e) {
      ticket.value = null
      if (e instanceof ApiProblem && e.status === 404) notFound.value = true
      else ui.notify(e instanceof ApiProblem ? e.userMessage : t('errors.generic'), 'error')
    } finally {
      loading.value = false
    }
  }

  /** Fresh copy by id (also refreshes the ETag). Returns false when the ticket is gone or hidden. */
  async function refetch(): Promise<boolean> {
    if (!ticket.value) return false
    try {
      const res = await ticketApi.getById(ticket.value.id)
      ticket.value = res.ticket
      etag.value = res.etag
      stale.value = false
      return true
    } catch (e) {
      if (e instanceof ApiProblem && e.status === 404) return false
      return true
    }
  }

  /**
   * Picks up the ETag (and fresh data) from `GET /agent/tickets/{id}` after a
   * response that carried none (by-number, PATCH, escalate). Failures are
   * silent: without an ETag the next PATCH is last-write-wins.
   */
  async function refreshEtag() {
    const id = ticket.value?.id
    if (!id) return
    try {
      const res = await ticketApi.getById(id)
      if (ticket.value?.id === id && res.etag) {
        ticket.value = res.ticket
        etag.value = res.etag
      }
    } catch {
      /* keep the current copy */
    }
  }

  /** Debounced refetch for bursts of realtime events (≤ 1 per second). */
  function scheduleRefetch(onGone?: () => void) {
    if (refetchTimer) return
    refetchTimer = setTimeout(async () => {
      refetchTimer = null
      if (!(await refetch())) onGone?.()
    }, 1000)
  }

  function dispose() {
    if (refetchTimer) clearTimeout(refetchTimer)
    refetchTimer = null
  }

  /** Applies a `TicketUpdateResult`; 'lost' when the caller can no longer see the ticket. */
  function apply(result: S['TicketUpdateResult'], newEtag?: string | null): Outcome {
    if (!result.visible) return 'lost'
    ticket.value = result.ticket
    etag.value = newEtag ?? null
    if (!newEtag) void refreshEtag()
    return 'ok'
  }

  function handleError(e: unknown, retry?: (status: S['TicketStatus']) => Promise<Outcome>): Outcome {
    if (ticketApi.isPrecondition(e)) {
      stale.value = true
      return 'failed'
    }
    if (ticketApi.isInvalidTransition(e) && retry) {
      transition.value = {
        allowed: ticketApi.allowedFrom(e),
        pick: (s) => {
          transition.value = null
          if (s) void retry(s)
        },
      }
      return 'failed'
    }
    ui.notify(e instanceof ApiProblem ? e.userMessage : t('errors.generic'), 'error')
    return 'failed'
  }

  /** PATCH with If-Match; the bound controls revert by themselves because they read `ticket`. */
  async function patch(body: S['TicketPatch']): Promise<Outcome> {
    if (!ticket.value) return 'failed'
    try {
      const { result, etag: next } = await ticketApi.patchTicket(ticket.value.id, body, etag.value)
      return apply(result, next)
    } catch (e) {
      return handleError(e, body.status ? (s) => patch({ ...body, status: s }) : undefined)
    }
  }

  /** Adds a message once (the POST response and the WS frame can both deliver it). */
  function appendMessage(m: S['Message']) {
    const tk = ticket.value
    if (!tk || tk.messages.some((x) => x.id === m.id)) return
    tk.messages = [...tk.messages, m]
  }

  function replaceMessage(m: S['Message']) {
    const tk = ticket.value
    if (!tk) return
    tk.messages = tk.messages.map((x) => (x.id === m.id ? m : x))
  }

  return { ticket, etag, loading, notFound, transition, stale, load, refetch, refreshEtag, scheduleRefetch, dispose, apply, patch, handleError, appendMessage, replaceMessage }
}
