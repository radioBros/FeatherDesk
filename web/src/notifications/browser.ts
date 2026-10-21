// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Browser notifications through the Notification API (D-19 §D, FE-094):
 * no Web Push, no service worker, nothing to host. A pop-up is raised by the
 * open tab that received `notification.created`, so nothing arrives while
 * every tab is closed (documented limitation). The tag is the notification
 * id, so several open tabs collapse into one OS notification. The optional
 * sound is a short WebAudio tone (no media file, CSP-friendly), remembered
 * per browser.
 */

export type Permission = NotificationPermission | 'unsupported'

const SOUND_KEY = 'st.notifications.sound'

export function supported(): boolean {
  return typeof window !== 'undefined' && 'Notification' in window
}

export function permission(): Permission {
  return supported() ? window.Notification.permission : 'unsupported'
}

/** Shows the browser's permission prompt (must run from a user gesture). */
export async function requestPermission(): Promise<Permission> {
  if (!supported()) return 'unsupported'
  try {
    return await window.Notification.requestPermission()
  } catch {
    return window.Notification.permission
  }
}

export function soundEnabled(): boolean {
  try {
    return localStorage.getItem(SOUND_KEY) === '1'
  } catch {
    return false
  }
}

export function setSoundEnabled(on: boolean) {
  try {
    if (on) localStorage.setItem(SOUND_KEY, '1')
    else localStorage.removeItem(SOUND_KEY)
  } catch {
    /* private mode: the setting simply is not remembered */
  }
}

export interface PopUp {
  /** Collapses duplicates across tabs (the notification id). */
  tag: string
  title: string
  body: string
  onClick: () => void
}

/** Raises one OS notification; returns null when not allowed or unsupported. */
export function show(p: PopUp): Notification | null {
  if (permission() !== 'granted') return null
  let n: Notification
  try {
    n = new window.Notification(p.title, { body: p.body, tag: p.tag })
  } catch {
    return null // e.g. Android Chrome without a service worker
  }
  n.onclick = (ev: Event) => {
    ev.preventDefault?.()
    try {
      window.focus()
    } catch {
      /* focus may be refused; the click still opens the ticket */
    }
    p.onClick()
    n.close()
  }
  if (soundEnabled()) beep()
  return n
}

type AudioCtor = typeof AudioContext

/** A 150 ms tone; silent when audio is unavailable or blocked. */
export function beep() {
  try {
    const Ctx = (window.AudioContext ?? (window as unknown as { webkitAudioContext?: AudioCtor }).webkitAudioContext) as AudioCtor | undefined
    if (!Ctx) return
    const ctx = new Ctx()
    const osc = ctx.createOscillator()
    const gain = ctx.createGain()
    osc.frequency.value = 880
    gain.gain.value = 0.05
    osc.connect(gain)
    gain.connect(ctx.destination)
    osc.onended = () => void ctx.close()
    osc.start()
    osc.stop(ctx.currentTime + 0.15)
  } catch {
    /* no sound */
  }
}
