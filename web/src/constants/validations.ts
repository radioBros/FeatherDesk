// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Central validation rules (CONVENTIONS §12, FE-004).
 *
 * Every Vuetify input that is required or has a format takes `:rules` built
 * from this module — never ad-hoc inline lambdas (enforced by the
 * `local/rules-from-validations` ESLint rule, FE-005). Messages are i18n keys
 * present in every locale. `code` values match the server's problem field
 * codes so server errors and client rules share one vocabulary.
 */
import { t } from '@/i18n'

export type Rule = (value: unknown) => true | string

/**
 * Server field error codes (`FieldErrorCode` in api/openapi.yaml, exactly that
 * enum: `codes.spec.ts`) ↔ i18n keys (CON-006). Several codes share the
 * message of the matching client rule. `FieldError` carries no params, so the
 * server-only messages have no placeholders.
 */
export const errorCodes = {
  admin_only: 'validation.adminOnly',
  boolean: 'validation.boolean',
  color: 'validation.hexColor',
  contrast: 'validation.contrast',
  customer: 'validation.customer',
  date: 'validation.date',
  date_time: 'validation.dateTime',
  default_missing: 'validation.defaultMissing',
  domain: 'validation.domain',
  email: 'validation.email',
  enum: 'validation.select',
  file_type: 'validation.fileType',
  filter: 'validation.filter',
  immutable: 'validation.immutable',
  inactive: 'validation.inactive',
  insecure_scheme: 'validation.httpsUrl',
  invalid: 'validation.invalid',
  invalid_image: 'validation.invalidImage',
  invalid_json: 'validation.invalidJson',
  invalid_svg: 'validation.invalidSvg',
  invalid_url: 'validation.url',
  kb_translation_required: 'validation.kbTranslationRequired',
  kb_visibility_wider: 'validation.kbVisibilityWider',
  length: 'validation.length',
  locale: 'validation.locale',
  locked_by_env: 'validation.lockedByEnv',
  max_depth: 'validation.maxDepth',
  max_items: 'validation.maxItems',
  max_length: 'validation.maxLength',
  mention_not_visible: 'validation.mentionNotVisible',
  min_properties: 'validation.minProperties',
  not_allowed: 'validation.notAllowed',
  not_applicable: 'validation.notApplicable',
  not_customer: 'validation.notCustomer',
  not_found: 'validation.notFound',
  not_optional: 'validation.notOptional',
  not_owner: 'validation.notOwner',
  number: 'validation.number',
  option: 'validation.select',
  order: 'validation.order',
  org_required: 'validation.orgRequired',
  other_org: 'validation.otherOrg',
  overlap: 'validation.timeRangeOrder',
  password_too_common: 'validation.passwordTooCommon',
  password_too_long: 'validation.passwordTooLong',
  password_too_short: 'validation.passwordTooShort',
  past: 'validation.past',
  pattern: 'validation.pattern',
  private_address: 'validation.privateAddress',
  range: 'validation.range',
  read_only: 'validation.readOnly',
  required: 'validation.required',
  requires_visible: 'validation.requiresVisible',
  self: 'validation.self',
  slug: 'validation.slug',
  staff: 'validation.staff',
  svg_not_allowed: 'validation.svgNotAllowed',
  team: 'validation.team',
  tier: 'validation.tier',
  time: 'validation.timeOfDay',
  timezone: 'validation.timeZone',
  too_many_tokens: 'validation.tooManyTokens',
  type: 'validation.type',
  unique: 'validation.unique',
  unknown: 'validation.unknown',
  unknown_team: 'validation.unknownTeam',
  unresolvable: 'validation.unresolvable',
  unsupported_type: 'validation.fileType',
  url: 'validation.url',
  uuid: 'validation.invalid',
  webmail: 'validation.webmail',
  wrong_project: 'validation.wrongProject',
} as const
export type ErrorCode = keyof typeof errorCodes

/** Codes of client-only rules (no server counterpart) ↔ i18n keys. */
export const ruleCodes = {
  hostname: 'validation.hostname',
  port: 'validation.port',
  hex_color: 'validation.hexColor',
  min_length: 'validation.minLength',
  password: 'validation.password',
  file_size: 'validation.fileSize',
  email_list: 'validation.emailList',
  setup_token: 'validation.setupToken',
  select: 'validation.select',
  integer: 'validation.integer',
  https_url: 'validation.httpsUrl',
  project_key: 'validation.projectKey',
  time_of_day: 'validation.timeOfDay',
  time_range_order: 'validation.timeRangeOrder',
  time_zone: 'validation.timeZone',
  date_order: 'validation.dateOrder',
  internal_link: 'validation.internalLink',
  otp_code: 'mfa.validation.otpCode',
  recovery_code: 'mfa.validation.recoveryCode',
} as const
export type RuleCode = keyof typeof ruleCodes
const messageKeys: Record<ErrorCode | RuleCode, string> = { ...errorCodes, ...ruleCodes }

const EMAIL = /^[^\s@<>()[\]\\,;:"]+@[^\s@<>()[\]\\,;:"]+\.[^\s@<>()[\]\\,;:"]{2,}$/
const HOSTNAME = /^(?=.{1,253}$)(?!-)[a-zA-Z0-9-]{1,63}(?<!-)(\.(?!-)[a-zA-Z0-9-]{1,63}(?<!-))*$/
const IPV4 = /^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}$/
const DOMAIN = /^(?=.{1,253}$)((?!-)[a-zA-Z0-9-]{1,63}(?<!-)\.)+[a-zA-Z]{2,63}$/
const HEX = /^#([0-9a-fA-F]{6}|[0-9a-fA-F]{3})$/
/** Project keys (D-20): 2-10 characters, an upper-case letter, then upper-case letters or digits. The form upper-cases first, like the server. */
const PROJECT_KEY = /^[A-Z][A-Z0-9]{1,9}$/
/** Slack app client IDs (D-21): `<digits>.<digits>`, as the API checks. */
const SLACK_CLIENT_ID = /^[0-9]{3,20}\.[0-9]{3,20}$/
const SLUG = /^[a-z0-9]+(?:-[a-z0-9]+)*$/
/** Custom-field keys, as the API defines them (`CustomFieldDef.key`): used in `cf.<key>` filters. */
const FIELD_KEY = /^[a-z][a-z0-9_]{0,62}$/
const ISO_DATE = /^\d{4}-\d{2}-\d{2}$/
const TIME = /^([01]\d|2[0-3]):[0-5]\d$|^24:00$/
const TAG = /^[a-z0-9-]{1,40}$/
/** MFA codes (D-19 §B): 6 digits (TOTP / e-mail), or a recovery code `xxxx-xxxx-xxxx`; spaces and dashes are ignored. */
const OTP = /^\d{6}$/
const RECOVERY = /^[a-z2-9]{12}$/
const compact = (v: unknown) => str(v).replace(/[\s-]/g, '')

const isEmpty = (v: unknown): boolean =>
  v === null ||
  v === undefined ||
  (typeof v === 'string' && v.trim() === '') ||
  (Array.isArray(v) && v.length === 0)

/** Format rules skip empty values; pair them with `required.*` when mandatory. */
const optional = (test: (v: unknown) => boolean, msg: () => string): Rule => (v) =>
  isEmpty(v) || test(v) ? true : msg()

const str = (v: unknown) => (typeof v === 'number' ? String(v) : typeof v === 'string' ? v.trim() : '')

export function isEmail(v: unknown): boolean {
  return EMAIL.test(str(v))
}

function isUrl(v: unknown): boolean {
  try {
    const u = new URL(str(v))
    return u.protocol === 'https:' || u.protocol === 'http:'
  } catch {
    return false
  }
}

function isValidDate(v: unknown): boolean {
  const s = str(v)
  if (!ISO_DATE.test(s)) return false
  const d = new Date(s + 'T00:00:00Z')
  return !Number.isNaN(d.getTime()) && d.toISOString().slice(0, 10) === s
}

const minutes = (s: string) => {
  const [h, m] = s.split(':').map(Number)
  return (h ?? 0) * 60 + (m ?? 0)
}

let zones: Set<string> | null = null
function isTimeZone(v: unknown): boolean {
  const s = str(v)
  if (!s) return false
  try {
    zones ??= new Set((Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf?.('timeZone') ?? [])
  } catch {
    zones = new Set()
  }
  if (zones.has(s) || s === 'UTC') return true
  try {
    new Intl.DateTimeFormat('en', { timeZone: s })
    return true
  } catch {
    return false
  }
}

export interface TimeRange {
  start: string
  end: string
}

function toNumber(v: unknown): number {
  if (typeof v === 'number') return v
  const s = str(v)
  return s === '' ? NaN : Number(s)
}

export function formatBytes(n: number): string {
  if (n >= 1024 * 1024) return `${+(n / (1024 * 1024)).toFixed(1)} MB`
  if (n >= 1024) return `${Math.round(n / 1024)} KB`
  return `${n} B`
}

function files(v: unknown): File[] {
  if (!v) return []
  if (Array.isArray(v)) return v.filter((f): f is File => typeof File !== 'undefined' && f instanceof File)
  return typeof File !== 'undefined' && v instanceof File ? [v] : []
}

const msg = (code: ErrorCode | RuleCode, named?: Record<string, unknown>) => () => t(messageKeys[code], named)

const required = {
  any: ((v) => (isEmpty(v) || v === false ? t(errorCodes.required) : true)) as Rule,
  email: ((v) => (isEmpty(v) ? t(errorCodes.required) : isEmail(v) ? true : t(errorCodes.email))) as Rule,
  select: ((v) => (isEmpty(v) ? t(ruleCodes.select) : true)) as Rule,
  array: ((v) => (Array.isArray(v) && v.length > 0 ? true : t(errorCodes.required))) as Rule,
}

export interface CustomFieldDef {
  key: string
  type: 'text' | 'textarea' | 'number' | 'date' | 'select' | 'multiselect' | 'checkbox'
  required?: boolean
  options?: { value: string }[]
  min?: number
  max?: number
}

export const validations = {
  required,
  email: optional(isEmail, msg('email')),
  url: optional(isUrl, msg('url')),
  hostname: optional((v) => HOSTNAME.test(str(v)) || IPV4.test(str(v)), msg('hostname')),
  port: optional((v) => {
    const n = toNumber(v)
    return Number.isInteger(n) && n >= 1 && n <= 65535
  }, msg('port')),
  hexColor: optional((v) => HEX.test(str(v)), msg('hex_color')),
  minLength: (n: number): Rule => optional((v) => str(v).length >= n, msg('min_length', { n })),
  maxLength: (n: number): Rule => (v) =>
    typeof v === 'string' && v.length > n ? t(errorCodes.max_length, { n }) : true,
  password: optional((v) => typeof v === 'string' && v.length >= 10 && v.length <= 128, msg('password')),
  number: optional((v) => Number.isFinite(toNumber(v)), msg('number')),
  range: (min: number, max: number): Rule =>
    optional((v) => {
      const n = toNumber(v)
      return Number.isFinite(n) && n >= min && n <= max
    }, msg('range', { min, max })),
  date: optional(isValidDate, msg('date')),
  fileType: (types: readonly string[]): Rule => (v) => {
    const allowed = types.map((x) => x.toLowerCase())
    const ok = files(v).every((f) => {
      const type = (f.type || '').toLowerCase()
      const ext = '.' + (f.name.split('.').pop() ?? '').toLowerCase()
      return allowed.some((a) =>
        a.endsWith('/*') ? type.startsWith(a.slice(0, -1)) : a.startsWith('.') ? a === ext : a === type,
      )
    })
    return ok ? true : t(errorCodes.file_type)
  },
  fileSize: (maxBytes: number): Rule => (v) =>
    files(v).every((f) => f.size <= maxBytes) ? true : t(ruleCodes.file_size, { max: formatBytes(maxBytes) }),
  emailList: optional(
    (v) =>
      str(v)
        .split(/[,;\s]+/)
        .filter(Boolean)
        .every(isEmail),
    msg('email_list'),
  ),
  domain: optional((v) => DOMAIN.test(str(v)), msg('domain')),
  slug: optional((v) => SLUG.test(str(v)), msg('slug')),
  setupToken: optional((v) => /^[A-Za-z0-9_-]{16,128}$/.test(str(v)), msg('setup_token')),
  integer: optional((v) => Number.isInteger(toNumber(v)), msg('integer')),
  /** Webhook targets: https only (pair with `url` when private http targets are allowed). */
  httpsUrl: optional((v) => isUrl(v) && str(v).startsWith('https://'), msg('https_url')),
  /** Project key (`WEB`, `IT2`): checked after upper-casing, as the server does. */
  projectKey: optional((v) => PROJECT_KEY.test(str(v).toUpperCase()), msg('project_key')),
  /** Slack app client ID (`1234567890.1234567890`), checked after trimming like the server. */
  slackClientId: optional((v) => SLACK_CLIENT_ID.test(str(v).trim()), msg('pattern')),
  /** KB article / category slugs: a slug of at most 100 characters. */
  kbSlug: optional((v) => SLUG.test(str(v)) && str(v).length <= 100, msg('slug')),
  /** Custom-field keys: a lowercase letter, then letters, digits or `_` (63 max), as the API requires. */
  customFieldKey: optional((v) => FIELD_KEY.test(str(v)), () => t('validation.fieldKey')),
  /** `HH:MM` (00:00–23:59, plus 24:00 as an end of day). */
  timeOfDay: optional((v) => TIME.test(str(v)), msg('time_of_day')),
  /**
   * Interval `index` of a day's ranges: end after start, no overlap with the
   * other intervals of the same day (business calendars, FE-056).
   */
  timeRangeOrder: (ranges: () => readonly TimeRange[], index: number): Rule => () => {
    const list = ranges()
    const r = list[index]
    if (!r || !TIME.test(r.start) || !TIME.test(r.end)) return true
    const [s, e] = [minutes(r.start), minutes(r.end)]
    if (e <= s) return t(ruleCodes.time_range_order)
    const overlaps = list.some(
      (o, i) => i !== index && TIME.test(o.start) && TIME.test(o.end) && s < minutes(o.end) && minutes(o.start) < e,
    )
    return overlaps ? t(ruleCodes.time_range_order) : true
  },
  ianaTimeZone: optional(isTimeZone, msg('time_zone')),
  /** The value appears at most once in `values()` (option values, holidays). */
  uniqueValues: (values: () => readonly unknown[]): Rule => (v) => {
    if (isEmpty(v)) return true
    const key = str(v)
    return values().filter((x) => str(x) === key).length > 1 ? t(errorCodes.unique) : true
  },
  /** An end date not before `start()` (ISO dates). */
  dateRangeOrder: (start: () => unknown): Rule => (v) => {
    const s = str(start())
    if (!isValidDate(v) || !isValidDate(s)) return true
    return str(v) < s ? t(ruleCodes.date_order) : true
  },
  /**
   * A public reply must never link an internal KB article (FE-052): rejects
   * a body that still contains any of `links()` while `active()` is true.
   */
  noInternalLinks: (links: () => readonly string[], active: () => boolean): Rule => (v) =>
    active() && typeof v === 'string' && links().some((l) => l && v.includes(l)) ? t(ruleCodes.internal_link) : true,
  /**
   * A browser-notification channel switched on needs the browser's permission
   * (D-19 §D, FE-094): `permission()` is `Notification.permission`. The
   * message lives in the lazy `notifications.*` pack its form loads.
   */
  browserNotifications: (permission: () => string): Rule => (v) =>
    v !== true || permission() === 'granted' ? true : t('notifications.validation.permission'),
  /** Applies `rule` to every item of a list value (chips of a combobox, D-19 A); the first failure wins. */
  listOf: (rule: Rule): Rule => (v) => {
    for (const item of Array.isArray(v) ? v : isEmpty(v) ? [] : [v]) {
      const r = rule(item)
      if (r !== true) return r
    }
    return true
  },
  /** At most `n` items in a list value (macro tags: 20, as the API caps them). */
  maxItems: (n: number): Rule => (v) => (Array.isArray(v) && v.length > n ? t(errorCodes.max_items, { n }) : true),
  /** Ticket tag names, as the API stores them: 1..40 of a-z, 0-9 and `-` (TKT-024). */
  tagName: optional((v) => TAG.test(str(v)), msg('pattern')),
  /**
   * Automation `regex` conditions (D-19 A): the server compiles RE2, so the
   * client rejects what RE2 lacks (look-around, back-references) and anything
   * longer than 500 characters or not a valid pattern at all.
   */
  re2Pattern: optional((v) => {
    const s = typeof v === 'string' ? v : ''
    if (s.length > 500 || /\(\?<?[=!]|\\[1-9]/.test(s)) return false
    try {
      new RegExp(s)
      return true
    } catch {
      return false
    }
  }, msg('pattern')),
  /** A 6-digit one-time code (authenticator app or e-mail; MFA, D-19 §B). */
  otpCode: optional((v) => OTP.test(compact(v)), msg('otp_code')),
  /** A recovery code (12 letters/digits, dashes optional; MFA, D-19 §B). */
  recoveryCode: optional((v) => RECOVERY.test(compact(v).toLowerCase()), msg('recovery_code')),
  /** Confirmation fields (password + confirm): must equal the other field's current value. */
  sameAs: (other: () => unknown): Rule => (v) => (isEmpty(v) || v === other() ? true : t('validation.mismatch')),

  /** Rules for an admin-defined custom field (FE-054); never hand-written per field. */
  forField(def: CustomFieldDef): Rule[] {
    const rules: Rule[] = []
    if (def.required) rules.push(def.type === 'multiselect' ? required.array : def.type === 'checkbox' ? required.any : required.any)
    switch (def.type) {
      case 'number':
        rules.push(validations.number)
        if (def.min !== undefined || def.max !== undefined)
          rules.push(validations.range(def.min ?? -Number.MAX_VALUE, def.max ?? Number.MAX_VALUE))
        break
      case 'date':
        rules.push(validations.date)
        break
      case 'text':
        rules.push(validations.maxLength(500))
        break
      case 'textarea':
        rules.push(validations.maxLength(10000))
        break
      case 'select':
      case 'multiselect': {
        const values = new Set((def.options ?? []).map((o) => o.value))
        rules.push((v) => {
          const list = Array.isArray(v) ? v : isEmpty(v) ? [] : [v]
          return list.every((x) => values.has(String(x))) ? true : t(errorCodes.invalid)
        })
        break
      }
    }
    return rules
  },

  /** Maps a server field code (+ params) to a translated message (FE-006). */
  fromServer(code: string, params?: Record<string, unknown>): string {
    const key = errorCodes[code as ErrorCode]
    return key ? t(key, params) : t(errorCodes.invalid)
  },
}

export default validations
