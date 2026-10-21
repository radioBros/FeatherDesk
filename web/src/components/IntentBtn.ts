// SPDX-License-Identifier: AGPL-3.0-only

import { defineComponent, h } from 'vue'
import { VBtn } from 'vuetify/components/VBtn'
import { resolveBtn } from '@/theme/intent'

/** Registered as `VBtn` (see `plugins/vuetify.ts`): a `v-btn` that applies `theme/intent.ts`. */
export default defineComponent({
  name: 'VBtn',
  inheritAttrs: false,
  setup(_props, { attrs, slots }) {
    return () => h(VBtn, resolveBtn(attrs), slots)
  },
})
