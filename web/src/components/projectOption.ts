// SPDX-License-Identifier: AGPL-3.0-only

import type { ProjectOption } from './ProjectSelect.vue'

/** The project behind a Vuetify select slot item (typed by the raw items, but a list item at runtime). */
export const optionOf = (item: unknown): ProjectOption => ((item as { raw?: ProjectOption }).raw ?? item) as ProjectOption
