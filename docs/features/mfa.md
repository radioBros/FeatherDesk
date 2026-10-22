# Two-step verification (MFA)

> Part of the [FeatherDesk manual](../DOCUMENTATION.md). The overview is in [Admin guide → Two-step verification](../DOCUMENTATION.md#717-two-step-verification-system--support-settings-admin), and the SSO interplay in [Two-step verification and SSO](../DOCUMENTATION.md#57-two-step-verification-and-sso); this page has every detail.

Two-step verification adds a second step after the password. Someone who learns a password still cannot sign in without the second
factor. It covers **local accounts**: staff (including the break-glass admin) and customers who sign in with a password or a
sign-in link.

- **Single sign-on (SSO) accounts** are not covered. They get two-step verification from their identity provider (Authentik,
  Keycloak, …), so FeatherDesk never asks them for a code. An account counts as an SSO account once it has an SSO identity
  or is managed by the identity provider: created by an SSO sign-in, signed in through SSO, or linked with
  `FD_OIDC_LINK_BY_EMAIL`.
  - **SSO accounts sign in only through the identity provider**, so its second factor can't be skipped.
  - Their password sign-in fails like a wrong password.
  - Sign-in links, invitation links, password reset and password change are refused.
  - Linking removes the old local password and signs out password sessions.

  See [Two-step verification and SSO](../DOCUMENTATION.md#57-two-step-verification-and-sso).
- **Break-glass admins** are the exception. A break-glass admin keeps its password sign-in even when it is linked to SSO, and
  every such sign-in goes through two-step verification and the policy, like any local account.
- **Personal access tokens (API tokens)** are not affected. A token keeps working whether or not its owner uses two-step
  verification, so treat tokens like passwords: give them short expiry dates and the smallest scopes. A token can't use the
  self-service routes (`/me/mfa`), but an admin's token with the `admin` scope can read and change the policy and reset users.

This guide covers users and admins. The last section has API details for integrators.

## Methods

| Method | How it works | Needs |
|---|---|---|
| **Authenticator app** (TOTP) | A 6-digit code from an app such as Aegis, Google Authenticator, Microsoft Authenticator, 1Password or Bitwarden. The code changes every 30 seconds | Nothing on the server |
| **Code by email** | A 6-digit code is emailed when you sign in. It is valid for 10 minutes and works once | Working email (SMTP) on the site |
| **Recovery codes** | 10 one-time codes, created when you turn on your first method. Each one works once | Nothing: keep them somewhere safe |

The authenticator app is the recommended method. An email code is only as safe as the mailbox it goes to.

## For users

### Turn it on

**Customers:** open **Profile → Two-step verification** (`/profile/security`). **Staff:** open your **Profile** page
(`/agent/profile`); the **Two-step verification** card is below **Password**. Accounts without a password don't see it.

1. Choose **Set up** next to *Authenticator app*. You are asked for your password first. This happens before every change, and it is
   remembered for 10 minutes on this browser.
2. Scan the QR code with your authenticator app. If you can't scan it, type the key shown below the QR code into the app instead.
3. Enter the 6-digit code the app shows, then choose **Confirm and turn on**.
4. Your **recovery codes** appear. They are shown only once. Copy or download them and keep them somewhere safe, for example in a
   password manager. Without them, losing your phone means asking an admin for help.

To use **codes by email**, choose **Turn on** next to *Code by email*. The option is greyed out when the site has no email
configured. If this is your first method, you get recovery codes the same way.

### Sign in

After your password (or after following a sign-in link, verification link or invitation link) you are asked for the second step:

- **Authenticator app:** type the current code from the app.
- **Code by email:** choose **Email me a code**, then type the code from the email. You can ask for a new code after 30 seconds
  (**Send a new code**). A code is sent at most 3 times per sign-in, and each new code replaces the previous one.
- **Recovery code:** type one of your saved codes (`abcd-efgh-jkmn`; case and dashes don't matter). That code is then used up.

The second step must be completed within 5 minutes (10 minutes after an email code was sent). After 5 wrong codes you have to start
again with your password. Wrong codes also count toward the normal sign-in limit: 10 failures within 15 minutes (wrong passwords,
wrong codes and wrong password confirmations together) lock the account for 15 minutes from IP addresses that have never signed
in to it successfully.

### Recovery codes

The profile shows how many unused codes are left. Choose **New codes** to replace all of them. The old ones stop working
immediately. Do this when you have used a few, or when you think someone else may have seen them.

### Change phone or turn it off

- **New phone:** choose **Replace** next to *Authenticator app* and scan the new QR code. The old app stops working once the new
  code is confirmed.
- **Turn off:** choose **Turn off** next to a method. When you turn off your last method, your recovery codes are deleted too.
- If the site **requires** two-step verification, you can't turn off your last method. Set up another one first.

### Lost your phone?

1. Sign in with a **recovery code**, or with **Code by email** if you turned it on.
2. Set up the authenticator app again (**Replace**) and create new recovery codes.
3. If you have neither, ask an admin to **reset your two-step verification**. You then sign in with your password only, and are
   asked to set it up again if the site requires it.

## For admins

### Require it (policy)

**Administration → System → Support settings**, card **Security**, **Require two-step verification**, then **Save**:

| Setting | Who must use it |
|---|---|
| **Nobody (optional)** | The default. Everyone may turn it on in their profile |
| **All staff accounts** | Every local staff account, including the break-glass admin |
| **Everyone** | Every local account, staff and customers |

When a covered user without a second factor signs in, they enter their password and are then **taken through the setup** (QR code
or email code, then the recovery codes) before they get in. They have no session until setup is finished. Users who are already
signed in keep their session; the requirement applies at their next sign-in. Every policy change is written to the audit log
(`auth.mfa_policy_changed`).

> **Before requiring it for staff,** make sure you can still get in. Set it up on your own account first. Keep the break-glass
> admin's recovery codes with the break-glass password.

### Reset a user

A user who lost both their device and their recovery codes needs a reset. Open the user (**Administration → People → Staff**, then
the pencil to open **Edit staff member**; or **Customers**, then the customer's detail page) and choose **Reset two-step
verification** in the two-step verification card (disabled while it is off). This removes their authenticator app, email
codes, recovery codes and any sign-in in progress. Their sessions stay. The reset is written to the audit log (`auth.mfa_reset`,
with you as the actor). Only admins can reset.

**Locked out yourself?** If the last admin loses their second factor, reset it from the server:

```sh
docker compose -f deploy/compose.yml exec app /featherdesk reset-mfa --email admin@example.com
```

This works like the admin reset (audited with `via: cli`). It takes no instance lock, so it works while the app runs. With the
app stopped, use a one-off container instead:

```sh
docker compose -f deploy/compose.yml run --rm app reset-mfa --email admin@example.com
```

Together with `reset-password`, the break-glass admin can always get back in.

### Email codes need SMTP

Email codes only work while email is configured (**Admin → Email**). Without SMTP, the option can't be turned on, and sending a code
is refused (`email-disabled`). Users who already had email codes then see only their other methods. A user whose only method is email
codes and who has no recovery codes left needs a reset.

### Audit log entries

| Action | When |
|---|---|
| `auth.mfa_challenged` | The password (or link) was right and a second step was asked for |
| `auth.login_success` | `data.mfa` names the method used for the second step |
| `auth.mfa_failed` | A wrong or replayed code (`data.reason` is `wrong_code` or `replay`; an expired email code counts as `wrong_code`) |
| `auth.mfa_enabled` / `auth.mfa_disabled` | A user turned a method on or off (`data.forced` for setup during sign-in) |
| `auth.mfa_recovery_used` / `auth.mfa_recovery_regenerated` | A recovery code was used / all codes were replaced |
| `auth.reauth_failed` | Wrong password when confirming a change |
| `auth.mfa_reset` | Admin or CLI reset |
| `auth.mfa_policy_changed` | The policy changed (`data.from`, `data.to`) |

No audit entry or log line ever contains a secret or a code. The API returns them only once: the authenticator secret during
setup, and the recovery codes when they are created. Status endpoints never contain one.

### How secrets are stored

- **Authenticator secrets** are encrypted with `FD_SECRET_KEY` (AES-256-GCM, their own derived key). `featherdesk rotate-secrets`
  re-encrypts them along with the other stored secrets. Rotate before you remove `FD_SECRET_KEY_PREVIOUS`, or every authenticator
  app stops working.
- **Recovery codes** and **sign-in attempts** are stored as SHA-256 hashes. **Email codes** are stored as an HMAC under
  `FD_SECRET_KEY`. If sending the email has to be retried, the queued email job (with the code) waits in the retry queue; the
  code is useless after its 10 minutes.
- A forced setup still in progress during sign-in is not re-encrypted by `rotate-secrets`; it expires within 5 minutes.
- An authenticator code is accepted once, and only within one 30-second step either side of the server clock. Keep the server
  clock synchronised (NTP).

### Limitations

- No WebAuthn or passkeys. No SMS codes.
- The 10-minute "password confirmed" window and the sign-in limits are kept in memory, so a restart clears them. This is harmless:
  users confirm their password again.

## API

All paths are relative to `/api/v1`. The full schemas are in `api/openapi.yaml` (tags *Auth*, *Me*, *Administration*).

**Sign in.** `POST /auth/login` (and `POST /auth/verify-email`, `POST /auth/magic-link` with a token, `POST /auth/invite/accept`)
answers with either `Me` (signed in) or an `MfaChallenge`. These are the only local ways to a session, and all of them pass the
same check. `POST /auth/password/reset` answers 204 and never signs in. The setup wizard only creates a new account. For an SSO
account (except a break-glass admin), `/auth/login` answers the generic 401 and the link endpoints answer 400. A challenge sets
no cookie:

```json
{"mfa_required": true, "mfa_token": "…", "methods": ["totp", "recovery"], "enrollment_required": false, "expires_at": "…"}
```

| Step | Request | Answer |
|---|---|---|
| Email code | `POST /auth/mfa/email/send {mfa_token}` | `{ok: true}`; 409 `email-disabled`, or 409 when the account has no email method; 429 when sent too often; 401 when the challenge expired |
| Second step | `POST /auth/mfa/verify {mfa_token, method, code}` | `Me` + cookie; 422 on `code` (wrong, try again) or on `method` (`not_allowed`); 429 (per-IP limit); 401 (expired or out of attempts: sign in again) |
| Forced setup | `POST /auth/mfa/enroll/totp {mfa_token}` | `{secret, otpauth_uri}` |
| Forced setup | `POST /auth/mfa/enroll/confirm {mfa_token, method: totp\|email, code}` | `{me, recovery_codes}` + cookie |

**Self-service** (session cookie only; tokens get 403). Every change needs `POST /me/mfa/reauth {password}` on the same session
within the last 10 minutes, otherwise 403. Changes also answer 403 for SSO accounts (break-glass admins excepted) and accounts
without a password.

| Request | Does |
|---|---|
| `GET /me/mfa` | Status: `available`, `enabled`, `required`, `totp_enabled`, `totp_enabled_at`, `email_enabled`, `email_available`, `recovery_codes_remaining`, `reauth_expires_at` |
| `POST /me/mfa/totp/setup` | New secret + `otpauth://` URI, pending for 15 minutes |
| `POST /me/mfa/totp/confirm {code}` | Turns it on; `recovery_codes` when this is the first method. 422 on a wrong code; 409 when no setup is pending or it is older than 15 minutes |
| `DELETE /me/mfa/totp` | Turns it off (409 when the policy needs it) |
| `POST /me/mfa/email` / `DELETE /me/mfa/email` | Email codes on / off |
| `POST /me/mfa/recovery-codes` | Replaces all recovery codes (409 when no method is on) |

**Admin** (`admin` scope for tokens): `GET /admin/security`, `PATCH /admin/security {mfa_required: none|staff|all}`,
`GET /admin/users/{id}/mfa`, `POST /admin/users/{id}/mfa/reset` (204). The policy is the settings key `security`; see
[Settings stored in the database](../DOCUMENTATION.md#settings-stored-in-the-database).

TOTP follows RFC 6238 with the defaults every authenticator app uses: SHA-1, 30-second steps, 6 digits.
