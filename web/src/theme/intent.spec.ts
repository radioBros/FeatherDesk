// SPDX-License-Identifier: AGPL-3.0-only

import { resolveBtn } from './intent'
import { mountApp } from '@/test/mount'
import { defineComponent } from 'vue'

describe('buttons by meaning', () => {
  it('keeps primary actions on the brand colour', () => {
    expect(resolveBtn({ color: 'primary', 'data-testid': 'nav-new-ticket' })).toMatchObject({ color: 'primary' })
    expect(resolveBtn({ color: 'primary', variant: 'flat' })).toMatchObject({ color: 'primary', variant: 'flat' })
  })
  it('turns save, publish, resolve and approve green', () => {
    for (const id of ['profile-save', 'kb-publish', 'ticket-resolve', 'x-approve']) {
      expect(resolveBtn({ color: 'primary', 'data-testid': id })).toMatchObject({ color: 'success', variant: 'flat' })
    }
  })
  it('turns escalate amber and destructive actions red', () => {
    expect(resolveBtn({ 'data-testid': 'ticket-escalate' })).toMatchObject({ color: 'warning', variant: 'flat' })
    expect(resolveBtn({ 'data-testid': 'token-revoke' })).toMatchObject({ color: 'error', variant: 'tonal' })
    expect(resolveBtn({ 'data-testid': 'erase-submit', type: 'submit' })).toMatchObject({ color: 'error', variant: 'flat' })
  })
  it('an explicit colour wins (a deliberately red approve stays red)', () => {
    expect(resolveBtn({ color: 'error', 'data-testid': 'privacy-approve', icon: 'x', variant: 'text' })).toMatchObject({ color: 'error', variant: 'tonal' })
    expect(resolveBtn({ color: 'error', type: 'submit' })).toMatchObject({ color: 'error' })
    expect(resolveBtn({ color: 'error', type: 'submit' }).variant).toBe('flat')
  })
  it('secondary text buttons become tonal; cancel is neutral', () => {
    expect(resolveBtn({ variant: 'text' })).toMatchObject({ variant: 'tonal', color: 'primary' })
    expect(resolveBtn({})).toMatchObject({ variant: 'tonal', color: 'primary' })
    expect(resolveBtn({ variant: 'text', 'data-testid': 'confirm-cancel' })).toMatchObject({ variant: 'tonal', color: 'secondary' })
  })
  it('icon buttons get a coloured icon and stay unfilled', () => {
    expect(resolveBtn({ icon: 'm0', variant: 'text' })).toMatchObject({ color: 'primary', variant: 'tonal' })
    expect(resolveBtn({ icon: '', color: 'nav-notifications' })).toMatchObject({ color: 'nav-notifications' })
  })
  it('opt-outs and toggle children are untouched', () => {
    const plain = { variant: 'text', 'data-intent': 'plain' }
    expect(resolveBtn(plain)).toBe(plain)
    const toggled = { value: 'a', variant: 'text' }
    expect(resolveBtn(toggled)).toBe(toggled)
  })
  it('v-btn in a template resolves to the intent button', () => {
    const { wrapper } = mountApp(defineComponent({ template: '<v-btn data-testid="x-save" color="primary">Go</v-btn>' }))
    expect(wrapper.find('button').classes()).toContain('bg-success')
  })
})
