// SPDX-License-Identifier: AGPL-3.0-only

/**
 * First-run wizard state (FE-049). Kept in memory and mirrored to
 * sessionStorage (try/catch: private mode, blocked storage) so a refresh does
 * not lose the typed values. Passwords are NEVER persisted: `persist()` writes
 * only the whitelisted fields below.
 */
export interface SetupState {
  step: number
  token: string
  admin: { name: string; email: string }
  branding: { product_name: string; company_name: string; primary_color: string }
  smtp: { host: string; port: number | string; security: 'none' | 'starttls' | 'tls'; username: string; from_name: string; from_address: string }
  /** POST /setup/complete succeeded: the admin session exists, steps 1–3 are locked. */
  completed: boolean
}

export const STORAGE_KEY = 'fd.setup.v1'

export function initialState(): SetupState {
  return {
    step: 1,
    token: '',
    admin: { name: '', email: '' },
    branding: { product_name: 'FeatherDesk', company_name: '', primary_color: '#1A73E8' },
    smtp: { host: '', port: 587, security: 'starttls', username: '', from_name: '', from_address: '' },
    completed: false,
  }
}

/** Only these fields are stored; anything else (passwords, files) stays in memory. */
function snapshot(s: SetupState): SetupState {
  return {
    step: s.step,
    token: s.completed ? '' : s.token,
    admin: { name: s.admin.name, email: s.admin.email },
    branding: { ...s.branding },
    smtp: {
      host: s.smtp.host,
      port: s.smtp.port,
      security: s.smtp.security,
      username: s.smtp.username,
      from_name: s.smtp.from_name,
      from_address: s.smtp.from_address,
    },
    completed: s.completed,
  }
}

export function persist(s: SetupState) {
  try {
    sessionStorage.setItem(STORAGE_KEY, JSON.stringify(snapshot(s)))
  } catch {
    /* storage unavailable: the wizard keeps working from memory */
  }
}

export function restore(): SetupState {
  const base = initialState()
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY)
    if (!raw) return base
    const v = JSON.parse(raw) as Partial<SetupState>
    return {
      ...base,
      ...v,
      admin: { ...base.admin, ...v.admin },
      branding: { ...base.branding, ...v.branding },
      smtp: { ...base.smtp, ...v.smtp },
      // Without the password the admin step must be re-entered after a refresh.
      step: Math.min(Math.max(1, Number(v.step) || 1), 6),
    }
  } catch {
    return base
  }
}

export function forget() {
  try {
    sessionStorage.removeItem(STORAGE_KEY)
  } catch {
    /* ignore */
  }
}
