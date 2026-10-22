// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Per-project portal branding (D-20): the `/p/{key}` entry and the new-ticket
 * page for a project apply its colour, logo and title on top of the global
 * branding (null overrides fall back) and take it off again on leaving, so
 * nothing leaks into other pages.
 */
import { onBeforeUnmount } from 'vue'
import { useTheme } from 'vuetify'
import { useConfigStore, type PortalProject } from '@/stores/config'
import { buildThemes } from '@/plugins/branding'
import { normalizeHex } from '@/utils/color'

export function useProjectBranding() {
  const config = useConfigStore()
  const theme = useTheme()

  function paint(color: string | null) {
    const b = config.branding
    const primary = normalizeHex(color ?? '')
    const built = buildThemes(primary ? { ...b, primary_color: primary, portal: { ...b.portal, primary } } : b, 'portal')
    for (const name of ['light', 'dark'] as const) {
      const live = theme.themes.value[name]
      if (live) Object.assign(live.colors, built[name]!.colors)
    }
  }

  /** Loads the project by key and applies its overrides; null when it is unknown or hidden. */
  async function enter(key: string): Promise<PortalProject | null> {
    const p = await config.loadProject(key)
    paint(p?.primary_color ?? null)
    return p
  }
  function leave() {
    if (!config.project) return
    config.clearProject()
    paint(null)
  }
  onBeforeUnmount(leave)
  return { enter, leave }
}
