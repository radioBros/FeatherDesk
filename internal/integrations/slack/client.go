// SPDX-License-Identifier: AGPL-3.0-only

package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is Slack's origin: OAuth pages under /oauth, Web API under /api.
const DefaultBaseURL = "https://slack.com"

// Client is a minimal Slack Web API client (D-21 §9: no SDK, stdlib only).
// Methods verified against docs.slack.dev on 2026-10-23.
type Client struct {
	base string
	http *http.Client
}

// NewClient returns a client for base (DefaultBaseURL when empty).
func NewClient(base string, hc *http.Client) *Client {
	if base == "" {
		base = DefaultBaseURL
	}
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{base: strings.TrimRight(base, "/"), http: hc}
}

// AuthorizeURL is the OAuth v2 consent page for the bot scopes.
func (c *Client) AuthorizeURL(clientID, redirectURI, state string, scopes []string) string {
	q := url.Values{"client_id": {clientID}, "scope": {strings.Join(scopes, ",")}, "redirect_uri": {redirectURI}, "state": {state}}
	return c.base + "/oauth/v2/authorize?" + q.Encode()
}

// APIError is a Slack {"ok": false, "error": code} answer or a transport failure.
type APIError struct {
	Code       string        // Slack error code, or "http_<status>" / "transport"
	RetryAfter time.Duration // set on ratelimited (HTTP 429)
	Err        error         // transport error, if any
}

func (e *APIError) Error() string {
	if e.Err != nil {
		return "slack: " + e.Code + ": " + e.Err.Error()
	}
	return "slack: " + e.Code
}

func (e *APIError) Unwrap() error { return e.Err }

// authCodes mean the bot token is no longer usable: the connection is broken.
var authCodes = map[string]bool{"invalid_auth": true, "token_revoked": true, "account_inactive": true, "not_authed": true,
	"token_expired": true, "missing_scope": true, "no_permission": true, "team_access_not_granted": true, "ekm_access_denied": true}

// channelCodes mean the route's channel cannot be posted to until an admin acts.
var channelCodes = map[string]bool{"channel_not_found": true, "not_in_channel": true, "is_archived": true,
	"restricted_action": true, "restricted_action_read_only_channel": true, "restricted_action_thread_only_channel": true}

// Auth reports a broken token.
func (e *APIError) Auth() bool { return authCodes[e.Code] }

// Channel reports a channel the bot cannot post to.
func (e *APIError) Channel() bool { return channelCodes[e.Code] }

// Permanent reports an error a retry cannot fix (bad request, auth, channel).
func (e *APIError) Permanent() bool {
	if e.Auth() || e.Channel() {
		return true
	}
	switch e.Code {
	case "invalid_blocks", "msg_too_long", "msg_blocks_too_long", "no_text", "invalid_arguments", "invalid_arg_name",
		"invalid_code", "code_already_used", "bad_redirect_uri", "invalid_client_id", "bad_client_secret", "invalid_grant_type":
		return true
	}
	return false
}

// AsAPIError unwraps err into an *APIError.
func AsAPIError(err error) (*APIError, bool) {
	var e *APIError
	ok := errors.As(err, &e)
	return e, ok
}

// call POSTs form to /api/<method> and decodes the JSON answer into out.
func (c *Client) call(ctx context.Context, method, token string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/"+method, strings.NewReader(form.Encode()))
	if err != nil {
		return &APIError{Code: "transport", Err: err}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "FeatherDesk-Slack/1")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return c.do(req, out)
}

// callJSON POSTs a JSON body (chat.postMessage takes blocks as JSON).
func (c *Client) callJSON(ctx context.Context, method, token string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/"+method, strings.NewReader(string(b)))
	if err != nil {
		return &APIError{Code: "transport", Err: err}
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("User-Agent", "FeatherDesk-Slack/1")
	req.Header.Set("Authorization", "Bearer "+token)
	return c.do(req, out)
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return &APIError{Code: "transport", Err: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return &APIError{Code: "transport", Err: err}
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		secs, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		return &APIError{Code: "ratelimited", RetryAfter: time.Duration(max(secs, 1)) * time.Second}
	}
	if resp.StatusCode >= 300 {
		return &APIError{Code: fmt.Sprintf("http_%d", resp.StatusCode)}
	}
	var head struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return &APIError{Code: "invalid_response", Err: err}
	}
	if !head.OK {
		if head.Error == "" {
			head.Error = "unknown_error"
		}
		return &APIError{Code: head.Error}
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return &APIError{Code: "invalid_response", Err: err}
		}
	}
	return nil
}

// OAuthResult is the part of the oauth.v2.access answer FeatherDesk keeps.
type OAuthResult struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	BotUserID   string `json:"bot_user_id"`
	AppID       string `json:"app_id"`
	Team        struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"team"`
	Enterprise *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"enterprise"`
	IsEnterpriseInstall bool `json:"is_enterprise_install"`
}

// Exchange trades an OAuth code for the bot token (oauth.v2.access).
func (c *Client) Exchange(ctx context.Context, clientID, clientSecret, code, redirectURI string) (OAuthResult, error) {
	var out OAuthResult
	err := c.call(ctx, "oauth.v2.access", "", url.Values{"client_id": {clientID}, "client_secret": {clientSecret}, "code": {code}, "redirect_uri": {redirectURI}}, &out)
	return out, err
}

// AuthTest is auth.test: who the token is.
type AuthTest struct {
	URL    string `json:"url"`
	Team   string `json:"team"`
	User   string `json:"user"`
	TeamID string `json:"team_id"`
	UserID string `json:"user_id"`
	BotID  string `json:"bot_id"`
}

// AuthTest checks the token and returns the workspace URL and bot name.
func (c *Client) AuthTest(ctx context.Context, token string) (AuthTest, error) {
	var out AuthTest
	err := c.call(ctx, "auth.test", token, url.Values{}, &out)
	return out, err
}

// Revoke revokes the token (auth.revoke); the app stays installed in Slack's
// app list until removed there.
func (c *Client) Revoke(ctx context.Context, token string) error {
	return c.call(ctx, "auth.revoke", token, url.Values{}, nil)
}

// Channel is one conversations.list entry.
type Channel struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	IsPrivate  bool   `json:"is_private"`
	IsMember   bool   `json:"is_member"`
	IsArchived bool   `json:"is_archived"`
	NumMembers int    `json:"num_members"`
}

// maxChannelPages caps conversations.list paging (200 per page).
const maxChannelPages = 25

// Channels lists public and private (bot-invited) channels, not archived.
func (c *Client) Channels(ctx context.Context, token string) ([]Channel, error) {
	var all []Channel
	cursor := ""
	for range maxChannelPages {
		var page struct {
			Channels []Channel `json:"channels"`
			Meta     struct {
				NextCursor string `json:"next_cursor"`
			} `json:"response_metadata"`
		}
		form := url.Values{"types": {"public_channel,private_channel"}, "exclude_archived": {"true"}, "limit": {"200"}}
		if cursor != "" {
			form.Set("cursor", cursor)
		}
		if err := c.call(ctx, "conversations.list", token, form, &page); err != nil {
			return nil, err
		}
		for _, ch := range page.Channels {
			if !ch.IsArchived {
				all = append(all, ch)
			}
		}
		cursor = page.Meta.NextCursor
		if cursor == "" {
			break
		}
	}
	return all, nil
}

// Message is a chat.postMessage request.
type Message struct {
	Channel        string  `json:"channel"`
	Text           string  `json:"text"`
	Blocks         []Block `json:"blocks,omitempty"`
	ThreadTS       string  `json:"thread_ts,omitempty"`
	ReplyBroadcast bool    `json:"reply_broadcast,omitempty"`
	UnfurlLinks    bool    `json:"unfurl_links"`
	UnfurlMedia    bool    `json:"unfurl_media"`
}

// Post sends m and returns the message ts (thread root for later replies).
func (c *Client) Post(ctx context.Context, token string, m Message) (string, error) {
	var out struct {
		TS string `json:"ts"`
	}
	err := c.callJSON(ctx, "chat.postMessage", token, m, &out)
	return out.TS, err
}
