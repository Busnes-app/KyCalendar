# Accounts and groups

[Product overview](../README.md) · [Standalone installation](../INSTALL.md)

Commands and unlinked file paths below are relative to the repository root.

## First sign-in

On an empty database the server creates the local administrator `admin`. Its password is
`KY_ADMIN_PASSWORD` when set; otherwise a generated one is written to
`<data dir>/initial-admin-password` (mode 0600; `docker compose exec app cat
/app/data/initial-admin-password`) and the log names only the file. Delete the file once you
have changed the password. A database that cannot be read stops startup rather than
bootstrapping a second administrator.

Every bootstrap or `init-admin` password must be replaced, including when
`KY_ADMIN_PASSWORD` supplies it. Operator resets revoke existing sessions, MFA challenges
and app passwords immediately, reactivate local admins and require replacement at the next login. Sign in, enter the current password and a different password
of at least 12 characters, then sign in again. Until replacement, the session can only check
its identity, change the password or sign out; privileged APIs remain blocked. Replacement
revokes existing sessions, MFA transactions and app passwords atomically. Existing accounts
are not retroactively flagged, since the server cannot infer whether they still use a bootstrap password.

## Local everyday accounts

The **People** screen is the everyday way to add and manage local accounts; these commands are for scripts and recovery.

`create-user` makes a local everyday account for testing or for instances without KyIdentity.
The password is read from stdin: `printf '%s\n' "$PW" | kycalendar create-user -username alice`.
The user must change it at first sign-in. An existing name is refused, never reset.

`reset-password` sets a temporary password on any local account, read from stdin the same way:
`printf '%s\n' "$PW" | kycalendar reset-password -username alice`. The role and status stay;
sessions, MFA challenges and app passwords are revoked, and the user must change it at the next
sign-in. SSO accounts are refused: their password lives in KyIdentity.

`init-admin` makes the named local account a break-glass administrator, creating it if needed,
and sets its password, read from stdin the same way:
`printf '%s\n' "$PW" | kycalendar init-admin -username admin`. The password must be replaced at
first sign-in.

`rename-user` renames a local account, keeping its password, role and MFA:
`kycalendar rename-user -username admin -to Cal-Admin`. Use it when a KyIdentity user has the
same username as a local account: KyCalendar refuses that sign-in (409) rather than link the two
by name. CalDAV clients of a renamed everyday account must sign in with the new name.

## Two-step verification

Local accounts, the administrator included, can add a code from an authenticator app to their
sign-in. Open **Two-step verification**, enter your password and choose **Set up**. Open the
setup link on your phone, or type the key into the app, then enter the code the app shows and
choose **Turn on**. A wrong code leaves it off. Save the ten recovery codes it then shows: each
signs you in once instead of a code, and they are not shown again. Turning it on or off signs
you out everywhere else and revokes your app passwords; create new ones afterwards.

From then on, sign-in asks for the code after the password. To change phones, choose
**Turn off** with your password and a current code or a recovery code, then set it up again;
it cannot be set up again while it is on. App passwords you create afterwards are separate
credentials: two-step verification guards the web sign-in only. If you lose both the phone and
the recovery codes, ask an administrator to reset your password (for the administrator,
`kycalendar init-admin`): a reset turns two-step verification off. SSO accounts use their
identity provider's second factor instead.

## Administrators and group calendars

KyCalendar administrators manage group calendars, access, backups and the audit log. They never see events and cannot use calendars on their devices. Give a dedicated KyIdentity login the admin role, and keep everyday logins for calendar use.

In KyIdentity, on the KyCalendar app:

1. Create the app role `kycalendar.admin`. Creating an app's first role also stops KyIdentity from sending its global admin role to KyCalendar; until then, a global admin is still not a KyCalendar admin.
2. Assign `kycalendar.admin` to the dedicated administrator login, directly or through a group.
3. For group calendars, enable group delivery on the KyCalendar SCIM connector so groups and their members reach KyCalendar. KyCalendar matches a SCIM user to their KySignOn login by KyIdentity's user ID, so SCIM must come from KyIdentity, not another identity provider.

A role change takes effect at that user's next sign-in, or at once through SCIM, and signs them out of KyCalendar everywhere. After upgrading, SSO administrators sign in again once, so the app role decides who stays an administrator.

In KyCalendar, **Group calendars** lists every group calendar. Create one, then add groups:

- **reader** sees events;
- **editor** creates, changes and deletes events;
- **manager** also renames and recolours the calendar and changes who has access.

A member's phone shows every group calendar they can read under the same app password, read-only below editor. Removing someone from a group cuts their access on their next sync. Deleting a KyIdentity group removes its access and leaves the calendar for you to re-grant or delete. Deleting a calendar needs a sign-in from the last 10 minutes.

## People

**People** lists every account. Local accounts (people who sign in with a password) can be edited here; accounts from KyIdentity or SCIM carry a "Managed by ..." badge and change only in the identity provider.

- **Add person**: username, optional display name and email, and Everyday user or Administrator. KyCalendar generates a temporary password and shows it once: give it to the person in person or over another channel you trust, not in the same message as the username. They choose their own password at first sign-in.
- **Reset password** shows a new temporary password once and signs the person out everywhere; their phones stop syncing until they create new app passwords.
- **Make admin / Make user** and **Disable** sign the person out everywhere. Administrators never see calendars. You cannot demote or disable yourself, and nobody can demote or disable the last active local administrator: that account is the way back in when single sign-on is down.
- **Reattach to current sign-in** appears on a single sign-on account bound to an earlier provider or issuer, which the current provider therefore refuses. It binds the account to the current provider and issuer, enables it and signs it out everywhere, keeping its calendars; the person's next sign-in through the provider decides their role. Use it only when you are sure the new provider's account is the same person: whoever the new provider signs in with that account's subject gets the account and its calendars. The confirm names the old and the new binding; if the sign-in settings change before you confirm, KyCalendar refuses and shows the list again. An account your identity provider disabled (through SCIM or the directory webhook) under the current issuer is not offered: restore it in the identity provider. That includes accounts of an issuer you switched away from and back to: they belong to the current issuer again, and SCIM restores them. This is the one change the People screen makes to a synced account.
- Adding an administrator, resetting, changing a role, disabling, enabling and reattaching need a sign-in from the last 10 minutes. There is no delete: disabling keeps the person's calendars.
- SCIM is off until you set `KY_SCIM_TOKEN` (at least 32 characters) in `.env`; that token is the bearer your identity provider's SCIM connector uses. The SCIM screen shows whether one is set, never the token.
- SCIM never sees local accounts, OpenID Connect accounts or accounts of an earlier provider binding: it cannot list, change, disable or delete them, cannot create a synced account with a local account's username (in any case), and cannot add a local account to a synced group.

## Sign-in

KyCalendar always accepts local passwords. **Sign-in** connects one single sign-on provider:

- **KyIdentity**: logins with the `kycalendar.admin` app role are administrators (see above), and SCIM users from KyIdentity sign in as themselves.
- **Another OpenID Connect provider** (Keycloak, Authentik, Google, Entra ID and others): everyone who signs in is an everyday user, whatever roles the provider sends. Administrators stay local accounts.

Register a client with PKCE (S256) at the provider, with the redirect URI the screen shows (`https://<your host>/api/sso/kysignon/callback`), then enter the issuer URL, client ID, client secret (required; only confidential clients are supported) and the button label. **Test** reads the provider's discovery document without saving; **Save** tests again, needs a sign-in from the last 10 minutes, and takes effect at once. The issuer must use HTTPS and must name itself exactly as typed; redirects, loopback and link-local addresses are refused, private LAN addresses are allowed. The secret is stored encrypted under the instance's data key, with the provider, issuer and client ID it was entered for, and never shown again; leave it blank to keep it. It is used only for that registration: if the issuer or client ID changes, on this screen or through `KY_KYSIGNON_ISSUER`/`KY_KYSIGNON_CLIENT_ID`, single sign-on stays off until you enter the new provider's secret, so one provider's secret is never sent to another. A secret saved by a pre-release build of this screen has no such record and must be entered again. Backups carry the settings and the key, so a restore should restore sign-in (not covered by the restore drill).

To end sessions here when someone signs out at the provider, or an administrator ends their login there, also register `https://<your host>/api/sso/kysignon/backchannel-logout` as the client's back-channel logout URI (OpenID Connect Back-Channel Logout; KyIdentity supports it). Without it, a session here lasts until it expires (7 days by default). A logout ends the web sessions of that login, or, when the provider ends every login of the person, their sessions signed in before it, including a sign-in still completing; app passwords on phones and agent tokens are separate and stay until revoked or the account is disabled. The upgrade that adds this signs every single sign-on user out of the web app once, because their older sessions cannot be matched to a provider login; local accounts stay signed in.

Switching provider, or changing the issuer, disables every account of the previous provider (their calendars stay) so that a different provider can never sign in as one of them; the screen states how many before you confirm. Each single sign-on account remembers the provider and issuer it was created under and signs in only through that one, so re-enabling it later (for example from SCIM) does not hand it to the new provider. The issuer is compared exactly: `https://id.example` and `https://id.example/` are different issuers, so adding or removing a trailing slash is a provider change and needs the client secret again. An instance that already ran a pre-release build of this screen with a saved issuer ending in `/` sees its first start of this version as a provider change. On upgrade, accounts that were disabled at that moment are not bound to the current provider and stay refused even if SCIM re-enables them. The login page shows "Continue with <label>" only while a provider is configured.

Environment variables win: `KY_KYSIGNON_ISSUER` fixes the provider to KyIdentity and locks the issuer; `KY_KYSIGNON_CLIENT_ID` and `KY_KYSIGNON_SECRET` lock their fields but apply only beside the issuer variable. KyIdentity configured through the environment also needs `KY_KYSIGNON_SECRET` (only confidential clients are supported); without it, and without a secret saved on this screen for the same issuer and client ID, single sign-on stays off and the startup log says so. Changing `KY_KYSIGNON_ISSUER` between restarts (or the provider kind) counts as a provider change too; a new client ID or secret does not. Start this version once before changing `KY_KYSIGNON_ISSUER`; the first start binds the current issuer and disables nothing. KyIdentity's directory webhook (`KY_KYSIGNON_HMAC_SECRET`) is applied only while sign-in is bound to KyIdentity; otherwise it is ignored and logged. It only takes access away: it creates new directory users and deactivates, deletes or updates the display name and email of known ones, but never re-activates a disabled account, so a stale webhook secret cannot hand a disabled row to a later login. `KY_SSO_ENABLED=false` switches single sign-on off whatever is saved. An issuer set by the environment is not rediscovered on Save and may be loopback or plain HTTP; Test still reads its discovery document without the address guard, with a 20-second limit and no redirects.

## Groups

**Groups** lists every group: local ones you create here and, when SCIM is connected, the ones your identity provider syncs (badged "Managed by SCIM"; change those in the identity provider). Create a group, rename it, search for people to add, remove members, or delete it. Only active everyday people can be members: administrators never see calendars. Adding or removing someone reaches their phone at its next sync. Deleting a group removes its members and its access to group calendars (the confirm says how many); the calendars stay. Deleting needs a sign-in from the last 10 minutes.

Names are unique ignoring case. If your identity provider sends a group whose name a local group already has, KyCalendar refuses it and the local group shows a warning: rename the local group and the next sync creates the synced one.

## Calendar limits

Native clients sync over CalDAV at `/dav/` with an app password. Each user is capped, and so is
the instance; a write past a cap gets 507 Insufficient Storage. Each per-user limit must be
positive or startup fails.

| Variable | Default | Meaning |
|---|---|---|
| `KY_CALENDAR_MAX_OBJECTS_PER_USER` | `20000` | Events across all of a user's calendars. |
| `KY_CALENDAR_MAX_CALENDARS_PER_USER` | `50` | Calendars a user may own. |
| `KY_CALENDAR_MAX_BYTES_PER_USER` | `16777216` (16 MiB), capped at a tenth of `KY_CALENDAR_MAX_BYTES_TOTAL` when that is set (4 MiB on SQLite) | Stored event bytes across all of a user's calendars, and separately per group calendar, so no single owner can fill the instance. |
| `KY_CALENDAR_MAX_BYTES_TOTAL` | `41943040` (40 MiB) on SQLite, `0` (off) on Postgres | Stored event bytes across every user. |
| `KYCALENDAR_SCHEDULING` | `false` | Server-side invitations. **Leave it off: unfinished.** On, calendar apps hand invitations to the server and people on this instance get them in their calendar. People outside the instance need KyPost (below), which does not accept them yet. |
| `KYCALENDAR_KYPOST_URL` | empty | KyPost's `https://` address. KyPost mails invitations to people outside the instance from the organizer's own mailbox, and hands their replies back. Needs `KYCALENDAR_KYPOST_SECRET_FILE`. |
| `KYCALENDAR_KYPOST_SECRET_FILE` | empty | A file holding the secret KyCalendar and KyPost share, at least 32 bytes (`openssl rand -hex 32`), mode 600. In Compose, put it in `./data` and name it `/app/data/kypost.secret`. |
| `KYCALENDAR_KYPOST_ALLOW_PRIVATE` | `false` | Let KyPost be on your own network (a private address). Loopback stays refused; `https://` is always required. |

The instance cap exists because a capsule carries the SQLite database as one file and refuses a
file over 64 MiB: past it, every backup fails for everyone. Raising it on SQLite trades that
guarantee away. Postgres deployments make no capsules, so the cap is off there. The cap is
shared: once it is full, every user's writes get 507 until data is removed, which is visible,
where a backup that silently stopped is not. Keep the per-user cap well below it; deleting a user
deletes their calendars and frees their share.
