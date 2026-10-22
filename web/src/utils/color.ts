// SPDX-License-Identifier: AGPL-3.0-only

/** Colour helpers for runtime theming and the WCAG contrast warning (FE-002, FE-047). */
export function normalizeHex(hex: string): string | null {
  const m = /^#?([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(hex.trim())
  if (!m) return null
  let h = m[1]!.toLowerCase()
  if (h.length === 3) h = h.split('').map((c) => c + c).join('')
  return '#' + h
}

export function hexToRgb(hex: string): [number, number, number] {
  const h = normalizeHex(hex) ?? '#000000'
  return [parseInt(h.slice(1, 3), 16), parseInt(h.slice(3, 5), 16), parseInt(h.slice(5, 7), 16)]
}

export function rgbToHex(r: number, g: number, b: number): string {
  const c = (n: number) => Math.round(Math.min(255, Math.max(0, n))).toString(16).padStart(2, '0')
  return `#${c(r)}${c(g)}${c(b)}`
}

export function luminance(hex: string): number {
  const [r, g, b] = hexToRgb(hex).map((v) => {
    const s = v / 255
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
  }) as [number, number, number]
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

export function contrastRatio(a: string, b: string): number {
  const [l1, l2] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number]
  return (l1 + 0.05) / (l2 + 0.05)
}

/** Mixes `hex` toward white (amount > 0) or black (amount < 0). */
export function shade(hex: string, amount: number): string {
  const [r, g, b] = hexToRgb(hex)
  const t = amount < 0 ? 0 : 255
  const p = Math.abs(amount)
  return rgbToHex(r + (t - r) * p, g + (t - g) * p, b + (t - b) * p)
}

/** Text colour (white/black) that reads best on `bg`. */
export function onColor(bg: string): string {
  return contrastRatio(bg, '#ffffff') >= contrastRatio(bg, '#000000') ? '#ffffff' : '#000000'
}

/** A dark-theme variant: lighten colours too dark to read on a dark surface. */
export function forDark(hex: string): string {
  let c = normalizeHex(hex) ?? hex
  for (let i = 0; i < 6 && contrastRatio(c, '#121212') < 4.5; i++) c = shade(c, 0.2)
  return c
}
