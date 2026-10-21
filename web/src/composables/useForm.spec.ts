// SPDX-License-Identifier: AGPL-3.0-only

import { useForm } from './useForm'
import { ApiProblem } from '@/api/problem'

function setup(valid: boolean) {
  const f = useForm()
  f.formRef.value = { validate: vi.fn(async () => ({ valid })) }
  return f
}

describe('useForm (CONVENTIONS §12)', () => {
  it('does not run the action when the form is invalid', async () => {
    const f = setup(false)
    const action = vi.fn()
    expect(await f.submit(action)()).toBe(false)
    expect(action).not.toHaveBeenCalled()
    expect(f.formRef.value!.validate).toHaveBeenCalledOnce()
  })
  it('does not run the action without a form ref', async () => {
    const f = useForm()
    const action = vi.fn()
    expect(await f.submit(action)()).toBe(false)
    expect(action).not.toHaveBeenCalled()
  })
  it('runs the action when valid and toggles submitting', async () => {
    const f = setup(true)
    let during = false
    const ok = await f.submit(async () => {
      during = f.submitting.value
    })()
    expect(ok).toBe(true)
    expect(during).toBe(true)
    expect(f.submitting.value).toBe(false)
  })
  it('maps a 422 problem onto field errors, other problems onto formError', async () => {
    const f = setup(true)
    await f.submit(async () => {
      throw new ApiProblem(422, { errors: [{ field: 'email', code: 'email' }] })
    })()
    expect(f.fieldErrors.email?.length).toBe(1)
    expect(f.formError.value).toBeNull()
    await f.submit(async () => {
      throw new ApiProblem(401, { title: 'Invalid credentials' })
    })()
    expect(f.fieldErrors.email).toBeUndefined()
    expect(f.formError.value).toBeTruthy()
  })
  it('prevents the native submit event', async () => {
    const f = setup(false)
    const ev = new Event('submit', { cancelable: true })
    await f.submit(() => {})(ev)
    expect(ev.defaultPrevented).toBe(true)
  })
})
