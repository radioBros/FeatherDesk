// SPDX-License-Identifier: AGPL-3.0-only

import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { defineComponent, ref } from 'vue'
import PasswordField from './PasswordField.vue'
import { mountApp } from '@/test/mount'
import { validations } from '@/constants/validations'

afterEach(() => {
  document.body.innerHTML = ''
})

const Host = defineComponent({
  components: { PasswordField },
  props: { onSubmit: { type: Function, default: () => {} } },
  setup() {
    const v = ref('')
    const rules = [validations.required.any]
    return { v, rules }
  },
  template: `<form data-testid="f" @submit.prevent="onSubmit"><PasswordField v-model="v" label="Pw" :rules="rules" autocomplete="new-password" data-testid="pw"><template #details>DET</template></PasswordField></form>`,
})

describe('PasswordField', () => {
  it('toggles type, label and aria-pressed', async () => {
    const { wrapper } = mountApp(Host)
    const input = () => wrapper.find('input')
    const btn = () => wrapper.find('[data-testid="password-toggle"]')
    expect(input().attributes('type')).toBe('password')
    expect(btn().attributes('aria-pressed')).toBe('false')
    expect(btn().attributes('aria-label')).toBe('Show password')
    await btn().trigger('click')
    expect(input().attributes('type')).toBe('text')
    expect(btn().attributes('aria-pressed')).toBe('true')
    expect(btn().attributes('aria-label')).toBe('Hide password')
    await btn().trigger('click')
    expect(input().attributes('type')).toBe('password')
  })

  it('is a focusable native button that does not submit', async () => {
    const onSubmit = vi.fn()
    const { wrapper } = mountApp(Host, { props: { onSubmit } })
    const b = wrapper.find('[data-testid="password-toggle"]')
    expect(b.element.tagName).toBe('BUTTON')
    expect(b.attributes('type')).toBe('button')
    expect((b.element as HTMLButtonElement).tabIndex).toBe(0)
    await b.trigger('click')
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('hides again on blur', async () => {
    const { wrapper } = mountApp(Host)
    await wrapper.find('[data-testid="password-toggle"]').trigger('click')
    expect(wrapper.find('input').attributes('type')).toBe('text')
    await wrapper.find('input').trigger('blur')
    expect(wrapper.find('input').attributes('type')).toBe('password')
  })

  it('passes v-model, autocomplete, rules and slots through', async () => {
    const { wrapper } = mountApp(Host)
    const input = wrapper.find('input')
    expect(input.attributes('autocomplete')).toBe('new-password')
    await input.setValue('abc')
    expect((wrapper.find('input').element as HTMLInputElement).value).toBe('abc')
    expect(wrapper.text()).toContain('DET')
    await input.setValue('')
    await input.trigger('blur')
    await new Promise((r) => setTimeout(r, 0))
    expect(wrapper.text()).toContain('required')
  })
})

describe('no raw password inputs (CONVENTIONS §12)', () => {
  it('type="password" appears only in PasswordField.vue', () => {
    const bad: string[] = []
    const walk = (dir: string) => {
      for (const n of readdirSync(dir)) {
        const p = join(dir, n)
        if (statSync(p).isDirectory()) walk(p)
        else if (/\.vue$/.test(n) && !p.endsWith('components/PasswordField.vue') && /type=["']password["']|:type=["'][^"']*password/.test(readFileSync(p, 'utf8'))) bad.push(p)
      }
    }
    walk(join(__dirname, '..'))
    expect(bad).toEqual([])
  })
})
