// SPDX-License-Identifier: AGPL-3.0-only

import { validations, errorCodes, ruleCodes, isEmail } from './validations'
import { i18n } from '@/i18n'
import enAccount from '@/locales/account/en.json'

// The MFA code messages (`otp_code`, `recovery_code`) live in the lazy `account` pack (FE-031).
beforeAll(() => i18n.global.mergeLocaleMessage('en', enAccount as never))

const en = (key: string, named?: Record<string, unknown>) => i18n.global.t(key, named ?? {})

describe('validations (FE-004)', () => {
  it('required.any', () => {
    const r = validations.required.any
    expect(r('')).toBe(en('validation.required'))
    expect(r('  ')).toBe(en('validation.required'))
    expect(r(null)).toBe(en('validation.required'))
    expect(r([])).toBe(en('validation.required'))
    expect(r(false)).toBe(en('validation.required'))
    expect(r('x')).toBe(true)
    expect(r(0)).toBe(true)
  })
  it('required.email', () => {
    expect(validations.required.email('')).toBe(en('validation.required'))
    expect(validations.required.email('nope')).toBe(en('validation.email'))
    expect(validations.required.email('a@b.co')).toBe(true)
  })
  it('required.select and required.array', () => {
    expect(validations.required.select(null)).toBe(en('validation.select'))
    expect(validations.required.select('a')).toBe(true)
    expect(validations.required.array([])).toBe(en('validation.required'))
    expect(validations.required.array(['a'])).toBe(true)
  })
  it('format rules accept empty values (pair with required)', () => {
    for (const r of [validations.email, validations.url, validations.hostname, validations.port, validations.hexColor,
      validations.password, validations.number, validations.date, validations.emailList, validations.domain,
      validations.slug, validations.setupToken]) {
      expect(r('')).toBe(true)
      expect(r(null)).toBe(true)
    }
  })
  it('email', () => {
    expect(isEmail('user@example.com')).toBe(true)
    expect(isEmail('user.name+tag@sub.example.it')).toBe(true)
    expect(validations.email('a@b')).toBe(en('validation.email'))
    expect(validations.email('a b@c.de')).toBe(en('validation.email'))
  })
  it('url', () => {
    expect(validations.url('https://example.com/x')).toBe(true)
    expect(validations.url('http://localhost:8080')).toBe(true)
    expect(validations.url('javascript:alert(1)')).toBe(en('validation.url'))
    expect(validations.url('example.com')).toBe(en('validation.url'))
  })
  it('hostname', () => {
    expect(validations.hostname('smtp.example.com')).toBe(true)
    expect(validations.hostname('localhost')).toBe(true)
    expect(validations.hostname('10.0.0.1')).toBe(true)
    expect(validations.hostname('-bad-.com')).toBe(en('validation.hostname'))
    expect(validations.hostname('a b')).toBe(en('validation.hostname'))
  })
  it('port', () => {
    expect(validations.port(587)).toBe(true)
    expect(validations.port('465')).toBe(true)
    expect(validations.port(0)).toBe(en('validation.port'))
    expect(validations.port(70000)).toBe(en('validation.port'))
    expect(validations.port('2.5')).toBe(en('validation.port'))
  })
  it('hexColor', () => {
    expect(validations.hexColor('#1A73E8')).toBe(true)
    expect(validations.hexColor('#abc')).toBe(true)
    expect(validations.hexColor('1A73E8')).toBe(en('validation.hexColor'))
    expect(validations.hexColor('#ggg')).toBe(en('validation.hexColor'))
  })
  it('minLength / maxLength', () => {
    expect(validations.minLength(3)('ab')).toBe(en('validation.minLength', { n: 3 }))
    expect(validations.minLength(3)('abc')).toBe(true)
    expect(validations.maxLength(3)('abcd')).toBe(en('validation.maxLength', { n: 3 }))
    expect(validations.maxLength(3)('abc')).toBe(true)
  })
  it('password 10..128', () => {
    expect(validations.password('short')).toBe(en('validation.password'))
    expect(validations.password('x'.repeat(10))).toBe(true)
    expect(validations.password('x'.repeat(129))).toBe(en('validation.password'))
  })
  it('number and range', () => {
    expect(validations.number('12.5')).toBe(true)
    expect(validations.number('abc')).toBe(en('validation.number'))
    expect(validations.range(1, 5)(3)).toBe(true)
    expect(validations.range(1, 5)(6)).toBe(en('validation.range', { min: 1, max: 5 }))
  })
  it('date', () => {
    expect(validations.date('2026-10-20')).toBe(true)
    expect(validations.date('2026-02-30')).toBe(en('validation.date'))
    expect(validations.date('20/10/2026')).toBe(en('validation.date'))
  })
  it('fileType / fileSize', () => {
    const png = new File(['x'.repeat(10)], 'a.png', { type: 'image/png' })
    const exe = new File(['x'], 'a.exe', { type: 'application/x-msdownload' })
    expect(validations.fileType(['image/*'])(png)).toBe(true)
    expect(validations.fileType(['image/*'])(exe)).toBe(en('validation.fileType'))
    expect(validations.fileType(['.png'])([png])).toBe(true)
    expect(validations.fileSize(5)(png)).toBe(en('validation.fileSize', { max: '5 B' }))
    expect(validations.fileSize(100)([png])).toBe(true)
  })
  it('emailList, domain, slug, setupToken', () => {
    expect(validations.emailList('a@b.co, c@d.io')).toBe(true)
    expect(validations.emailList('a@b.co, nope')).toBe(en('validation.emailList'))
    expect(validations.domain('example.com')).toBe(true)
    expect(validations.domain('localhost')).toBe(en('validation.domain'))
    expect(validations.slug('reset-password-2')).toBe(true)
    expect(validations.slug('Reset Password')).toBe(en('validation.slug'))
    expect(validations.setupToken('abcdefghijklmnop1234')).toBe(true)
    expect(validations.setupToken('short')).toBe(en('validation.setupToken'))
  })
  it('projectKey accepts 2-10 upper-case characters (after upper-casing)', () => {
    expect(validations.projectKey('WEB')).toBe(true)
    expect(validations.projectKey('web2')).toBe(true)
    expect(validations.projectKey('W')).toBe(en('validation.projectKey'))
    expect(validations.projectKey('1AB')).toBe(en('validation.projectKey'))
    expect(validations.projectKey('A'.repeat(11))).toBe(en('validation.projectKey'))
  })
  it('slackClientId accepts <digits>.<digits> (D-21)', () => {
    expect(validations.slackClientId('1234567890.1234567890')).toBe(true)
    expect(validations.slackClientId(' 123.456 ')).toBe(true)
    expect(validations.slackClientId('')).toBe(true)
    expect(validations.slackClientId('abc.123')).toBe(en('validation.pattern'))
    expect(validations.slackClientId('1234567890')).toBe(en('validation.pattern'))
  })
  it('forField builds rules from a custom field definition', () => {
    const num = validations.forField({ key: 'n', type: 'number', required: true, min: 1, max: 10 })
    const run = (rules: ReturnType<typeof validations.forField>, v: unknown) => rules.map((r) => r(v)).find((x) => x !== true) ?? true
    expect(run(num, '')).toBe(en('validation.required'))
    expect(run(num, '11')).toBe(en('validation.range', { min: 1, max: 10 }))
    expect(run(num, '5')).toBe(true)
    const sel = validations.forField({ key: 's', type: 'select', options: [{ value: 'a' }, { value: 'b' }] })
    expect(run(sel, 'a')).toBe(true)
    expect(run(sel, 'z')).toBe(en('validation.invalid'))
    const multi = validations.forField({ key: 'm', type: 'multiselect', required: true, options: [{ value: 'a' }] })
    expect(run(multi, [])).toBe(en('validation.required'))
    expect(run(multi, ['a'])).toBe(true)
    expect(run(validations.forField({ key: 'd', type: 'date' }), '2026-13-01')).toBe(en('validation.date'))
  })
  it('integer, httpsUrl, kbSlug, timeOfDay, ianaTimeZone (V1 additions)', () => {
    expect(validations.integer('3')).toBe(true)
    expect(validations.integer('3.5')).toBe(en('validation.integer'))
    expect(validations.integer('abc')).toBe(en('validation.integer'))
    expect(validations.httpsUrl('https://hooks.example.com/x')).toBe(true)
    expect(validations.httpsUrl('http://hooks.example.com/x')).toBe(en('validation.httpsUrl'))
    expect(validations.kbSlug('reset-password')).toBe(true)
    expect(validations.kbSlug('Reset Password')).toBe(en('validation.slug'))
    expect(validations.kbSlug('a'.repeat(101))).toBe(en('validation.slug'))
    expect(validations.customFieldKey('order_id2')).toBe(true)
    expect(validations.customFieldKey('order-id')).toBe(en('validation.fieldKey'))
    expect(validations.customFieldKey('2nd')).toBe(en('validation.fieldKey'))
    expect(validations.customFieldKey('a'.repeat(64))).toBe(en('validation.fieldKey'))
    expect(validations.timeOfDay('09:30')).toBe(true)
    expect(validations.timeOfDay('24:00')).toBe(true)
    expect(validations.timeOfDay('9:30')).toBe(en('validation.timeOfDay'))
    expect(validations.timeOfDay('25:00')).toBe(en('validation.timeOfDay'))
    expect(validations.ianaTimeZone('Europe/Rome')).toBe(true)
    expect(validations.ianaTimeZone('Mars/Olympus')).toBe(en('validation.timeZone'))
  })
  it('timeRangeOrder: end after start, no overlap within a day', () => {
    const day = [
      { start: '09:00', end: '12:00' },
      { start: '11:00', end: '14:00' },
    ]
    const msg = en('validation.timeRangeOrder')
    expect(validations.timeRangeOrder(() => day, 1)(day[1]!.end)).toBe(msg)
    expect(validations.timeRangeOrder(() => day, 0)(day[0]!.end)).toBe(msg)
    const ok = [
      { start: '09:00', end: '12:00' },
      { start: '13:00', end: '17:00' },
    ]
    expect(validations.timeRangeOrder(() => ok, 1)('17:00')).toBe(true)
    const backwards = [{ start: '12:00', end: '09:00' }]
    expect(validations.timeRangeOrder(() => backwards, 0)('09:00')).toBe(msg)
  })
  it('uniqueValues, dateRangeOrder, noInternalLinks', () => {
    const values = ['a', 'b', 'a']
    expect(validations.uniqueValues(() => values)('a')).toBe(en('validation.unique'))
    expect(validations.uniqueValues(() => values)('b')).toBe(true)
    expect(validations.dateRangeOrder(() => '2026-10-10')('2026-10-01')).toBe(en('validation.dateOrder'))
    expect(validations.dateRangeOrder(() => '2026-10-10')('2026-10-10')).toBe(true)
    const links = ['/kb/a/internal-runbook']
    expect(validations.noInternalLinks(() => links, () => true)('see [x](/kb/a/internal-runbook)')).toBe(en('validation.internalLink'))
    expect(validations.noInternalLinks(() => links, () => false)('see [x](/kb/a/internal-runbook)')).toBe(true)
    expect(validations.noInternalLinks(() => links, () => true)('no links here')).toBe(true)
  })
  it('fromServer maps server field codes to the same messages', () => {
    expect(validations.fromServer('required')).toBe(en('validation.required'))
    expect(validations.fromServer('max_length', { n: 5 })).toBe(en('validation.maxLength', { n: 5 }))
    expect(validations.fromServer('unknown_code')).toBe(en('validation.invalid'))
    for (const key of [...Object.values(errorCodes), ...Object.values(ruleCodes)]) expect(i18n.global.te(key, 'en')).toBe(true)
  })
})
