// SPDX-License-Identifier: AGPL-3.0-only

import { createRouter, createWebHistory, type Router } from 'vue-router'
import { routes } from './routes'
import { decide, loginPathFor } from './guards'
import { useSessionStore } from '@/stores/session'
import { useConfigStore } from '@/stores/config'
import { setUnauthorizedHandler } from '@/api/client'

export function createAppRouter(): Router {
  const router = createRouter({
    history: createWebHistory(),
    routes,
    scrollBehavior: (_to, _from, saved) => saved ?? { top: 0 },
  })

  router.beforeEach(async (to) => {
    const session = useSessionStore()
    const config = useConfigStore()
    if (!session.checked && !config.config.setup_pending) await session.refresh()
    return decide(to, { me: session.me, setupPending: config.config.setup_pending, portalEnabled: config.config.portal_enabled !== false })
  })

  // An expired session on any API call sends the user to the right login page.
  setUnauthorizedHandler(() => {
    const session = useSessionStore()
    session.clear()
    const current = router.currentRoute.value
    const login = loginPathFor(current.meta.area, useConfigStore().config.portal_enabled !== false)
    if (current.path !== login) void router.push({ path: login, query: { redirect: current.fullPath } })
  })

  return router
}
