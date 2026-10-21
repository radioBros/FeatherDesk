// SPDX-License-Identifier: AGPL-3.0-only

import { shallowRef, watch, type Ref } from 'vue'
import { useRoute } from 'vue-router'
import type { SectionMeta } from './routeSections'

/**
 * The section and icon of the current page. The route table (about 45 icons) is a
 * dynamic import, so it never counts toward an area's initial JS (FE-031 budget);
 * the badge appears right after the first paint.
 */
export function useSectionMeta(): Ref<SectionMeta | undefined> {
  const route = useRoute()
  const meta = shallowRef<SectionMeta | undefined>()
  watch(
    () => route?.path ?? '',
    async (path) => {
      const { sectionForPath } = await import('./routeSections')
      meta.value = sectionForPath(path)
    },
    { immediate: true },
  )
  return meta
}
