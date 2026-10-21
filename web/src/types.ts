// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Hand-written shapes for the foundation layer. They mirror api/openapi.yaml
 * (components.schemas) and are replaced by generated types where the views
 * import from '@/api/schema' directly.
 */
export type Kind = 'staff' | 'customer'
export type Role = 'admin' | 'supervisor' | 'agent' | 'requester' | 'org_manager'
export type TicketStatus = 'new' | 'open' | 'pending' | 'on_hold' | 'resolved' | 'closed'
export type Priority = 'low' | 'normal' | 'high' | 'urgent'
export type MessageKind = 'public' | 'internal' | 'system'
export type ThemeMode = 'light' | 'dark' | 'system'
export type Area = 'portal' | 'agent' | 'kb' | 'setup'

export interface AreaColors {
  primary?: string
  secondary?: string
  accent?: string
}

export interface Branding {
  product_name: string
  company_name?: string
  support_url?: string
  logo_light_url?: string | null
  logo_dark_url?: string | null
  icon_url?: string | null
  favicon_url?: string | null
  primary_color: string
  secondary_color?: string
  accent_color?: string
  portal?: AreaColors
  agent?: AreaColors
  default_theme: ThemeMode
  radius: 'sharp' | 'default' | 'rounded'
  font_family?: 'system' | 'serif' | 'mono'
  login_title?: string
  login_subtitle?: string
  login_background_url?: string | null
  login_background_color?: string | null
  portal_welcome_md?: string
  new_ticket_intro_md?: string
  privacy_url?: string | null
  terms_url?: string | null
  imprint_url?: string | null
  powered_by: boolean
  default_locale?: string
  locales?: string[]
}

export interface AudienceAuth {
  local: boolean
  oidc: boolean
  oidc_label?: string
  self_registration?: boolean
  magic_link?: boolean
}

export interface AuthConfig {
  staff: AudienceAuth
  customer: AudienceAuth
  branding: Branding
  tier_labels: Record<string, string>
  email_enabled: boolean
  setup_pending: boolean
  upload: { max_bytes: number; allowed_types: string[]; max_files?: number }
  message_max_length: number
  customer_urgent_allowed: boolean
  kb_enabled?: boolean
  /** support.portal_enabled: false = only admin and staff sign in; every customer entry point is hidden. */
  portal_enabled?: boolean
}

export interface Me {
  id: string
  kind: Kind
  role: Role
  tier?: number | null
  name: string
  email: string
  organization_id?: string | null
  locale?: string
  csrf_token: string
  idp_managed?: boolean
  has_password?: boolean
  teams?: string[]
  /** Staff: the projects the user belongs to (D-20). */
  projects?: { project_id: string; role?: 'member' | 'project_admin' }[]
  /** Staff: the switcher's saved choice, null = all projects. */
  current_project_id?: string | null
  allowed_areas?: string[]
}

/** The project chip next to a ticket (API `ProjectRef`). */
export interface ProjectRef {
  id: string
  key: string
  name: string
  color: string
}

export interface Attachment {
  id: string
  filename: string
  content_type: string
  size_bytes: number
  url?: string
}

export interface Message {
  id: string
  kind: MessageKind
  body_md: string
  author?: { id: string; name: string; kind: Kind } | null
  created_at: string
  attachments?: Attachment[]
}
