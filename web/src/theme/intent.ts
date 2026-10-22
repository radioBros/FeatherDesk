// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Buttons by meaning. `IntentBtn` (registered as `v-btn`) passes every `v-btn`
 * through `resolveBtn`, so call sites keep writing `<v-btn>` and still get the
 * right colour:
 *  - primary actions: brand `primary` (unchanged);
 *  - positive (save, publish, resolve, approve): `success`;
 *  - escalate: `warning`; destructive (delete, erase, revoke, disable): `error`;
 *  - everything that was a plain text button becomes a tonal button;
 *  - icon-only buttons get a coloured icon.
 * The intent comes from `data-intent="positive|warning|danger|secondary|neutral|primary|plain"`
 * or, when absent, from the `data-testid` words below. Explicit `color` and
 * `variant` on the call site always win (except brand `primary` on a positive
 * action); `data-intent="plain"` opts out.
 */
import { INTENT_STYLE, type Intent } from './palette'

const word = (names: string) => new RegExp(`(^|[-_.])(${names})([-_.]|$)`)
const POSITIVE = word('save|publish|resolve|approve|apply|activate|enable|restore|accept')
const WARNING = word('escalate|escalation')
const DANGER = word('delete|erase|revoke|disable|remove|purge|wipe|reject|deny')
const NEUTRAL = word('cancel|close|dismiss|back|skip|later')

export type BtnAttrs = Record<string, unknown>

export function intentOf(attrs: BtnAttrs): Intent | undefined {
  const explicit = attrs['data-intent']
  if (typeof explicit === 'string') return explicit as Intent
  const id = typeof attrs['data-testid'] === 'string' ? (attrs['data-testid'] as string) : ''
  if (WARNING.test(id)) return 'warning'
  if (DANGER.test(id)) return 'danger'
  if (POSITIVE.test(id)) return 'positive'
  if (NEUTRAL.test(id)) return 'neutral'
  return undefined
}

const isIcon = (a: BtnAttrs) => 'icon' in a && a.icon !== false && a.icon !== undefined

export function resolveBtn(attrs: BtnAttrs): BtnAttrs {
  const intent = intentOf(attrs)
  // Toggle-group children and opt-outs keep Vuetify's own look.
  if (intent === 'plain' || 'value' in attrs) return attrs
  const a = { ...attrs }
  const hasColor = a.color !== undefined && a.color !== ''
  const variant = a.variant as string | undefined

  if (isIcon(a)) {
    // Icon buttons: a coloured icon, no fill unless the author asked for one.
    if (!hasColor) a.color = intent && intent !== 'primary' ? INTENT_STYLE[intent].color : 'primary'
    // Tonal is the global default (plugins/branding.ts); a text icon button turns tonal too.
    if (variant === 'text') a.variant = 'tonal'
    return a
  }
  // Brand primary (or no colour) on a meaningful action takes the meaning's colour.
  if (intent && intent !== 'primary' && (!hasColor || a.color === 'primary')) {
    const st = INTENT_STYLE[intent]
    a.color = st.color
    // Destructive actions are solid only as a form's submit; in rows and bars they stay soft.
    const soft = intent === 'danger' && a.type !== 'submit'
    if (variant === undefined) a.variant = soft ? 'tonal' : st.variant
    else if (variant === 'text') a.variant = 'tonal'
    return a
  }
  // A plain text button (or one with no styling at all) becomes tonal.
  if (variant === 'text' || (variant === undefined && !hasColor)) {
    a.variant = 'tonal'
    if (!hasColor) a.color = 'primary'
  } else if (variant === undefined) {
    // A coloured button with no variant is a primary CTA or submit: solid, never elevated.
    a.variant = 'flat'
  }
  return a
}
