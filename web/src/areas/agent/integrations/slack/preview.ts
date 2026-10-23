// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Sample posts for the rule preview (D-21, INT-014). They mirror what the
 * server renders (internal/integrations/slack/message.go), which is English
 * on purpose: a channel is shared by people with different UI languages.
 * `*bold*` is Slack mrkdwn; the preview turns it into <strong> segments.
 */
import type { Trigger } from './triggers'

export const SAMPLE = {
  ref: 'HD-1042',
  subject: 'VPN drops every 10 minutes',
  requester: 'Ann Lee',
  agent: 'Marco Rossi',
  body: 'Since this morning the VPN disconnects every ten minutes, and I lose my remote desktop session each time.',
  note: 'Same symptom as last week: check the firewall idle timeout first.',
}

const PRIORITY: Record<string, { emoji: string; label: string }> = {
  urgent: { emoji: '🔴', label: 'Urgent' },
  high: { emoji: '🟠', label: 'High' },
  normal: { emoji: '🔵', label: 'Normal' },
  low: { emoji: '⚪', label: 'Low' },
}
export const priorityMeta = (p: string) => PRIORITY[p] ?? PRIORITY.normal!

/** The headline and optional quoted text of one sample event. */
export function sampleLine(trig: Trigger, includeBody: boolean): { line: string; quote?: string } {
  const s = SAMPLE
  switch (trig) {
    case 'ticket.created':
      return { line: `🎫 New ticket from *${s.requester}*`, quote: includeBody ? s.body : undefined }
    case 'ticket.status_changed':
      return { line: `🔄 Status *New* → *Open* by ${s.agent}` }
    case 'ticket.assigned':
      return { line: `👤 Assigned to *${s.agent}* by ${s.agent}` }
    case 'ticket.priority_changed':
      return { line: `🔴 Priority *High* → *Urgent* by ${s.agent}` }
    case 'ticket.escalated':
      return { line: `⏫ Escalated *L1* → *L2* by ${s.agent}` }
    case 'message.customer':
      return { line: `💬 *${s.requester}* replied`, quote: includeBody ? 'It happened again at 11:40.' : undefined }
    case 'message.staff':
      return { line: `📤 *${s.agent}* replied to the customer`, quote: includeBody ? 'Thanks Ann, we are looking into it now.' : undefined }
    case 'note.created':
      return { line: `🔒 *${s.agent}* added an internal note`, quote: includeBody ? s.note : undefined }
    case 'sla.warning':
      return { line: '⏳ First response SLA is about to breach (due today 14:00)' }
    case 'sla.breached':
      return { line: '🚨 Resolution SLA *breached* (due today 18:00)' }
    case 'ticket.csat':
      return { line: `📊 Satisfaction rating ⭐⭐⭐⭐ (4/5) from *${s.requester}*` }
  }
}

/** Splits `*bold*` mrkdwn into segments for safe rendering (no v-html). */
export function segments(text: string): { text: string; bold: boolean }[] {
  return text.split('*').map((t, i) => ({ text: t, bold: i % 2 === 1 })).filter((s) => s.text !== '')
}
