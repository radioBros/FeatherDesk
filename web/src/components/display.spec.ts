// SPDX-License-Identifier: AGPL-3.0-only

import { mountApp, flush } from '@/test/mount'
import StatusChip from './StatusChip.vue'
import PriorityChip from './PriorityChip.vue'
import TierChip from './TierChip.vue'
import UserAvatar from './UserAvatar.vue'
import EmptyState from './EmptyState.vue'
import PageHeader from './PageHeader.vue'
import TicketNumber from './TicketNumber.vue'
import RelativeTime from './RelativeTime.vue'
import ConfirmDialog from './ConfirmDialog.vue'
import { useConfirm } from '@/composables/useConfirm'
import { useConfigStore } from '@/stores/config'
import type { TicketStatus } from '@/types'

const statuses: TicketStatus[] = ['new', 'open', 'pending', 'on_hold', 'resolved', 'closed']

describe('display components (FE-009)', () => {
  it('StatusChip maps all 6 statuses; portal variant uses customer labels', () => {
    for (const s of statuses) {
      const { wrapper } = mountApp(StatusChip, { props: { status: s } })
      expect(wrapper.text().length).toBeGreaterThan(0)
      wrapper.unmount()
    }
    const agent = mountApp(StatusChip, { props: { status: 'pending' } }).wrapper
    expect(agent.text()).toBe('Pending')
    const portal = mountApp(StatusChip, { props: { status: 'pending', area: 'portal' } }).wrapper
    expect(portal.text()).toBe('Awaiting your reply')
  })
  it('PriorityChip shows the translated priority', () => {
    expect(mountApp(PriorityChip, { props: { priority: 'urgent' } }).wrapper.text()).toContain('Urgent')
  })
  it('TierChip uses instance tier labels', async () => {
    const { wrapper } = mountApp(TierChip, { props: { tier: 2 } })
    expect(wrapper.text()).toBe('L2')
    useConfigStore().config.tier_labels['2'] = 'Specialist'
    await flush()
    expect(wrapper.text()).toBe('Specialist')
  })
  it('UserAvatar renders initials', () => {
    expect(mountApp(UserAvatar, { props: { name: 'Ada Byron Lovelace' } }).wrapper.text()).toBe('AL')
    expect(mountApp(UserAvatar, { props: { name: '' } }).wrapper.text()).toBe('?')
  })
  it('EmptyState defaults and slot', () => {
    const { wrapper } = mountApp(EmptyState, { slots: { default: '<button>go</button>' } })
    expect(wrapper.text()).toContain('Nothing here yet')
    expect(wrapper.find('button').exists()).toBe(true)
  })
  it('PageHeader and TicketNumber', () => {
    expect(mountApp(PageHeader, { props: { title: 'Tickets', subtitle: 'All' } }).wrapper.text()).toContain('Tickets')
    expect(mountApp(TicketNumber, { props: { number: 1042 } }).wrapper.text()).toBe('#1042')
  })
  it('RelativeTime shows relative text with an absolute tooltip', () => {
    const past = new Date(Date.now() - 3 * 60_000).toISOString()
    const { wrapper } = mountApp(RelativeTime, { props: { value: past } })
    expect(wrapper.text()).toContain('3 minutes ago')
    expect(wrapper.find('time').attributes('title')).toBeTruthy()
    expect(mountApp(RelativeTime, { props: { value: new Date() } }).wrapper.text()).toBe('just now')
  })
  it('ConfirmDialog resolves the promise API', async () => {
    mountApp(ConfirmDialog)
    const { confirm } = useConfirm()
    const p = confirm({ body: 'Delete it?' })
    await flush()
    const ok = document.querySelector<HTMLButtonElement>('[data-testid="confirm-ok"]')
    expect(document.body.textContent).toContain('Delete it?')
    ok!.click()
    expect(await p).toBe(true)
    const p2 = confirm()
    await flush()
    document.querySelector<HTMLButtonElement>('[data-testid="confirm-cancel"]')!.click()
    expect(await p2).toBe(false)
  })
})
