// SPDX-License-Identifier: AGPL-3.0-only

/** Test fixtures for the Slack views (typed by the generated schema, so they track the spec). */
import type { components } from '@/api/schema'

type S = components['schemas']

export function slackStatus(over: Partial<S['SlackStatus']> = {}): S['SlackStatus'] {
  return {
    state: 'not_configured',
    client_id: '',
    client_secret_set: false,
    team_id: null,
    team_name: null,
    team_url: null,
    bot_user_id: null,
    bot_name: null,
    scopes: [],
    connected_at: null,
    connected_by: null,
    error: null,
    redirect_url: 'https://help.example.test/api/v1/integrations/slack/oauth/callback',
    https: true,
    manifest: { display_information: { name: 'Acme Help' } },
    manifest_url: 'https://api.slack.com/apps?new_app=1&manifest_json=%7B%7D',
    routes: 0,
    active_routes: 0,
    ...over,
  }
}

export function slackRoute(over: Partial<S['SlackRoute']> = {}): S['SlackRoute'] {
  return {
    id: 'r1',
    channel_id: 'C0SUPPORT',
    channel_name: 'support',
    channel_private: false,
    events: ['ticket.created', 'ticket.status_changed'],
    project_ids: [],
    priorities: [],
    threads: true,
    include_body: true,
    include_internal: false,
    active: true,
    last_error: null,
    last_error_at: null,
    last_posted_at: null,
    created_at: '2026-10-23T10:00:00Z',
    updated_at: '2026-10-23T10:00:00Z',
    ...over,
  }
}
