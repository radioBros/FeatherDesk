// SPDX-License-Identifier: AGPL-3.0-only

import { mount } from '@vue/test-utils'
import { createAppVuetify } from '@/plugins/vuetify'
import { i18n } from '@/i18n'
import ProjectChip from './ProjectChip.vue'
import TicketNumber from './TicketNumber.vue'

const g = () => ({ global: { plugins: [createAppVuetify(), i18n] } })

describe('ProjectChip', () => {
  it('shows the key with the project colour dot and the name as tooltip', () => {
    const w = mount(ProjectChip, { props: { project: { key: 'WEB', name: 'Web App', color: '#1976d2' } }, ...g() })
    expect(w.text()).toBe('WEB')
    expect(w.attributes('title')).toBe('Web App')
    expect(w.find('.st-project-dot').attributes('style')).toContain('rgb(25, 118, 210)')
  })
  it('adds the name on request and falls back to a neutral dot without colour', () => {
    const w = mount(ProjectChip, { props: { project: { key: 'IT', name: 'Internal IT', color: '' }, showName: true }, ...g() })
    expect(w.text()).toContain('Internal IT')
    expect(w.find('.st-project-dot').attributes('style')).toContain('background')
  })
  it('renders nothing without a project', () => {
    expect(mount(ProjectChip, { props: { project: null }, ...g() }).find('[data-testid="project-chip"]').exists()).toBe(false)
  })
})

describe('TicketNumber', () => {
  it('prefers the project ref and falls back to #number', () => {
    expect(mount(TicketNumber, { props: { number: 12, reftext: 'WEB-7' } }).text()).toBe('WEB-7')
    expect(mount(TicketNumber, { props: { number: 12 } }).text()).toBe('#12')
  })
})
