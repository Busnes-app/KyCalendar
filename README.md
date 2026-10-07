# kycalendar

KyCalendar is the Busnes.app suite calendar: personal calendars served to the web app and to
native CalDAV clients (iOS and macOS Calendar, DAVx5, Thunderbird) with per-device app
passwords. Built on the suite server base: Go backend, embedded React PWA, SQLite or
PostgreSQL, local and federated sign-in (KyIdentity or any OpenID Connect provider), SCIM provisioning, and
disaster recovery through the suite's KyRecovery.

Published image:

```bash
make ci        # gofmt, vet, race tests, smoke test
make run       # build and start on :8080; first start prints the bootstrap admin password
docker compose up -d
```

Source install (never paste this into a published-image install: the build overlay wins over a
`KY_IMAGE` digest pin, and a source install must set this line before its first `up -d` on a
new checkout; an install from before the published image existed has no such line yet, so run
this block once and confirm with `docker compose config --images`, which must print
`kycalendar:local` rather than the `ghcr.io` name):

```bash
make ci        # gofmt, vet, race tests, smoke test
make run       # build and start on :8080; first start prints the bootstrap admin password
(umask 077; t=$(mktemp ./.env.XXXXXX) && touch .env \
  && cf=$({ grep '^COMPOSE_FILE=' .env || [ $? -eq 1 ]; } | tail -n1 | cut -d= -f2-) && cf=${cf:-docker-compose.yml} \
  && case ":$cf:" in *:docker-compose.build.yml:*) ;; *) cf="$cf:docker-compose.build.yml";; esac \
  && { grep -v -e '^COMPOSE_FILE=' .env || [ $? -eq 1 ]; } > "$t" \
  && printf 'COMPOSE_FILE=%s\n' "$cf" >> "$t" && mv "$t" .env)
docker compose up -d
```

Update a published-image install on the rolling tag:

```bash
docker compose pull && docker compose up -d
```

A digest-pinned install (`KY_IMAGE` in `.env`) gets nothing from `pull`: re-run the pin recipe in
`docker-compose.yml` with the commit sha you want first, or delete that line to follow `:latest` again.

`AGENTS.md` is the contract for working in this repository.

## Container IP address

To assign the app a static IP on the Docker network, append
`docker-compose.static-ip.yml` to the existing `COMPOSE_FILE` value in `.env`, preserving
any build or LAN-DNS overlays. For a published-image install without other overlays:

```dotenv
COMPOSE_FILE=docker-compose.yml:docker-compose.static-ip.yml
KY_CONTAINER_IP=172.30.0.10
KY_NETWORK_SUBNET=172.30.0.0/24
```

Choose an unused IP inside the subnet and a subnet that does not overlap your existing
networks. These settings are consumed by Compose; `KY_HOST` remains the listener address
inside the container. Without the overlay, Docker continues assigning addresses automatically.

For an existing deployment, run `docker compose down` before changing these settings,
then `docker compose up -d` to recreate the network (brief downtime; omit `-v` to retain
database volumes). For a new deployment, just run `docker compose up -d`.

## First sign-in

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
- Adding an administrator, resetting, changing a role, disabling and enabling need a sign-in from the last 10 minutes. There is no delete: disabling keeps the person's calendars.

## Sign-in

KyCalendar always accepts local passwords. **Sign-in** connects one single sign-on provider:

- **KyIdentity**: logins with the `kycalendar.admin` app role are administrators (see above), and SCIM users from KyIdentity sign in as themselves.
- **Another OpenID Connect provider** (Keycloak, Authentik, Google, Entra ID and others): everyone who signs in is an everyday user, whatever roles the provider sends. Administrators stay local accounts.

Register a client with PKCE (S256) at the provider, with the redirect URI the screen shows (`https://<your host>/api/sso/kysignon/callback`), then enter the issuer URL, client ID, client secret (required; only confidential clients are supported) and the button label. **Test** reads the provider's discovery document without saving; **Save** tests again, needs a sign-in from the last 10 minutes, and takes effect at once. The issuer must use HTTPS and must name itself exactly as typed; redirects, loopback and link-local addresses are refused, private LAN addresses are allowed. The secret is stored encrypted under the instance's data key and never shown again; leave it blank to keep it. Backups carry the settings and the key, so a restore should restore sign-in (not covered by the restore drill).

Switching provider, or changing the issuer, disables every account of the previous provider (their calendars stay) so that a different provider can never sign in as one of them; the screen states how many before you confirm. Each single sign-on account remembers the provider and issuer it was created under and signs in only through that one, so re-enabling it later (for example from SCIM) does not hand it to the new provider. The issuer is compared exactly: `https://id.example` and `https://id.example/` are different issuers, so adding or removing a trailing slash is a provider change and needs the client secret again. The login page shows "Continue with <label>" only while a provider is configured.

Environment variables win: `KY_KYSIGNON_ISSUER` fixes the provider to KyIdentity and locks the issuer; `KY_KYSIGNON_CLIENT_ID` and `KY_KYSIGNON_SECRET` lock their fields but apply only beside the issuer variable. Changing `KY_KYSIGNON_ISSUER` between restarts (or the provider kind) counts as a provider change too; a new client ID or secret does not. Start this version once before changing `KY_KYSIGNON_ISSUER`; the first start binds the current issuer and disables nothing. KyIdentity's directory webhook (`KY_KYSIGNON_HMAC_SECRET`) is applied only while sign-in is bound to KyIdentity; otherwise it is ignored and logged. It only takes access away: it creates new directory users and deactivates, deletes or updates the display name and email of known ones, but never re-activates a disabled account, so a stale webhook secret cannot hand a disabled row to a later login. `KY_SSO_ENABLED=false` switches single sign-on off whatever is saved. An issuer set by the environment is not rediscovered on Save and may be loopback or plain HTTP; Test still reads its discovery document without the address guard, with a 20-second limit and no redirects.

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
| `KY_CALENDAR_MAX_BYTES_PER_USER` | `16777216` (16 MiB) | Stored event bytes across all of a user's calendars. |
| `KY_CALENDAR_MAX_BYTES_TOTAL` | `41943040` (40 MiB) on SQLite, `0` (off) on Postgres | Stored event bytes across every user. |

The instance cap exists because a capsule carries the SQLite database as one file and refuses a
file over 64 MiB: past it, every backup fails for everyone. Raising it on SQLite trades that
guarantee away. Postgres deployments make no capsules, so the cap is off there. The cap is
shared: once it is full, every user's writes get 507 until data is removed, which is visible,
where a backup that silently stopped is not. Keep the per-user cap well below it; deleting a user
deletes their calendars and frees their share.

## Connect a phone

Native clients need the server on HTTPS. With an `https://` `KY_APP_URL`, session cookies are
`Secure` and HSTS is sent; `KY_COOKIE_SECURE=false` overrides that for local testing only. Sign in as an everyday user (administrators are refused
on CalDAV), open **Phones & apps**, name the device and create an app password. The page shows
the server, your user name and the password once; revoke it there at any time.

- iPhone and iPad: Settings > Calendar > Accounts > Add Account > Other > Add CalDAV Account.
- Android: DAVx5 > Add account > Login with URL and user name, URL `https://<your host>/`.
- Thunderbird: New Calendar > On the Network, location `https://<your host>/`.

Enter the app password where the client asks for a password. Per-user caps are in
[Calendar limits](#calendar-limits).

## Disaster recovery

Every backup is one `.kycap` capsule: the database snapshot, the deployment's encryption key,
the settings that describe the deployment, and the pinned suite recovery public key. It is
sealed to the suite recovery key, which only the custodians' cards (k of n, split at the suite
ceremony) can reconstruct. Nothing on this server, and nothing on KyRecovery, can open one.
The mechanics are `github.com/Busnes-app/ky-primitives/recoveryclient`; this repository
supplies what it seals and how it checks a drill.

**Capsules are SQLite-only today.** The snapshot is `VACUUM INTO` against the local database
file; on `KY_DB_DRIVER=postgres` there is no snapshot and every backup refuses with "no
consistent database snapshot for this driver". A Postgres deployment must back its database up
itself, with `pg_dump` on its own schedule and its own retention, and must protect that dump:
it is the plaintext of everything a capsule would have sealed. Nothing travels in a capsule
there, because no capsule is made. The recovery key pin, the pairing and the schedule live in
the database and so ride in the `pg_dump`; `data/encryption.key` and `data/recovery.pub` do
not, and you must copy them separately. Without `encryption.key` no TOTP secret and no
KyRecovery token in that dump can be decrypted.

Backups also require the default database path: a capsule restores the database as
`<KY_DATA_DIR>/kycalendar.db`, so a backup refuses while `KY_DB_DSN` opens any other file.
Unset `KY_DB_DSN` or point it at `<KY_DATA_DIR>/kycalendar.db`.

The admin screen **Backup & recovery** shows four facts (recovery key, KyRecovery, local
copies, schedule) and the actions: Back up now, Download capsule, Run restore drill, the
schedule, pairing with Unpair, and pinning the key by hand.

### Two ways to get a key

- **Pair with KyRecovery.** Its dashboard issues a six-digit code; entering it here hands this
  server the suite public key and a deposit credential. The key is pinned once and never
  replaced: a later pairing that returns a different key is refused.
- **Pin the key by hand.** For a server with no KyRecovery: paste the base64 public key the
  ceremony page shows, with its k-of-n. Capsules then go only to the local directory.

### Why TLS matters here

The capsule is sealed, so a copy of it is worthless to an eavesdropper. What the wire does
carry is the suite public key at pairing (trust on first use), the deposit credential, and
each receipt. A man in the middle at pairing could substitute a key whose shares they hold,
so pairing over plain HTTP is refused outright, and a pairing across your own network should
be checked: compare the key ID on the screen with the ceremony card, or pin the key by hand
and skip the question.

### One run, every destination

Back up now, the schedule and the `deposit` command all do the same thing: seal one capsule
and deliver it to every configured destination. A pinned key with no destination is refused
with a message that says so. A local write that fails does not stop the deposit, and a
refused deposit does not remove the local copy.

### Environment

The server refuses a non-empty `KY_BACKUP_DIR`, `KY_BACKUP_KEEP`, `KY_BACKUP_DEPOSIT_INTERVAL` or `KY_BACKUP_ALLOW_PRIVATE_RECOVERY` at startup, compose included; the replacements are the `KYCALENDAR_BACKUP_*` names below, and `KYCALENDAR_DNS` replaces `KY_DNS`. Re-running the LAN-DNS block below migrates its own old `.env` lines; rename any other `KY_BACKUP_*` lines in `.env` by hand.

| Variable | Default | Meaning |
|---|---|---|
| `KYCALENDAR_BACKUP_DIR` | empty (off) | Directory for sealed local copies, `<service>.<capsule-id>.kycap` at mode 0600 (`kycalendar.cap-kycalendar-<n>.kycap`). Pruning removes only this application's own prefix. |
| `KYCALENDAR_BACKUP_KEEP` | `7` | Local copies to retain; below 1 refuses startup. |
| `KYCALENDAR_BACKUP_DEPOSIT_INTERVAL` | `24h` | Default schedule only. The admin screen's setting wins; `0` is off; 15 minutes to 366 days otherwise. |
| `KYCALENDAR_BACKUP_ALLOW_PRIVATE_RECOVERY` | `false` | Admit a KyRecovery on an RFC1918 or CGNAT address behind your own TLS proxy. Loopback, link-local and other reserved ranges stay refused; HTTPS stays required. Logged at startup and on the pairing audit row. |
| `KYCALENDAR_DNS` | unset | Only in `docker-compose.lan-dns.yml`: the container's resolver, for names that exist only on your LAN. |

Reach a KyRecovery that only your LAN's DNS knows:

The snippet appends `docker-compose.lan-dns.yml` to whatever `COMPOSE_FILE` chain `.env` already
holds (build overlay, local override) and leaves the rest of the chain alone; the resolver and the private-recovery flag
sit next to it: the resolver comes from an exported
`KYCALENDAR_DNS` (`export KYCALENDAR_DNS=<addr>`; fish: `set -x KYCALENDAR_DNS <addr>`) or, when that is unset, from the `KYCALENDAR_DNS` line
already in `.env`; there is no default, the block refuses to guess. An exported value overrides
`.env`, so re-running is a no-op only while `KYCALENDAR_DNS` is unset in your shell; the flag is set to true. One block for every install type:

```bash
(umask 077; touch .env \
  && cf=$({ grep '^COMPOSE_FILE=' .env || [ $? -eq 1 ]; } | tail -n1 | cut -d= -f2-) && cf=${cf:-docker-compose.yml} \
  && dns=${KYCALENDAR_DNS:-$({ grep '^KYCALENDAR_DNS=' .env || [ $? -eq 1 ]; } | tail -n1 | cut -d= -f2-)} \
  && : "${dns:?no resolver chosen: export KYCALENDAR_DNS=<your LAN resolver> (fish: set -x KYCALENDAR_DNS <addr>), then re-run this block}" \
  && case ":$cf:" in *:docker-compose.lan-dns.yml:*) ;; *) cf="$cf:docker-compose.lan-dns.yml";; esac \
  && t=$(mktemp ./.env.XXXXXX) && { grep -v -e '^COMPOSE_FILE=' -e '^KYCALENDAR_DNS=' -e '^KYCALENDAR_BACKUP_ALLOW_PRIVATE_RECOVERY=' -e '^KY_DNS=' -e '^KY_BACKUP_ALLOW_PRIVATE_RECOVERY=' .env || [ $? -eq 1 ]; } > "$t" \
  && printf 'COMPOSE_FILE=%s\nKYCALENDAR_DNS=%s\nKYCALENDAR_BACKUP_ALLOW_PRIVATE_RECOVERY=true\n' "$cf" "$dns" >> "$t" && mv "$t" .env)
docker compose up -d --force-recreate
docker inspect kycalendar --format '{{.HostConfig.Dns}}'   # must print the resolver you chose
```

Turning it off: remove the resolver and the flag, strip only `docker-compose.lan-dns.yml` from
`COMPOSE_FILE` (a build overlay or local override in the chain survives), and recreate:

```bash
(umask 077; t=$(mktemp ./.env.XXXXXX) && touch .env \
  && cf=$({ grep '^COMPOSE_FILE=' .env || [ $? -eq 1 ]; } | tail -n1 | cut -d= -f2- | tr ':' '\n' | grep -vx docker-compose.lan-dns.yml | paste -sd: -) \
  && { grep -v -e '^COMPOSE_FILE=' -e '^KYCALENDAR_DNS=' -e '^KYCALENDAR_BACKUP_ALLOW_PRIVATE_RECOVERY=' -e '^KY_DNS=' -e '^KY_BACKUP_ALLOW_PRIVATE_RECOVERY=' .env || [ $? -eq 1 ]; } > "$t" \
  && { [ -z "$cf" ] || [ "$cf" = docker-compose.yml ] || printf 'COMPOSE_FILE=%s\n' "$cf" >> "$t"; } && mv "$t" .env)
docker compose up -d --force-recreate
```

`KYCALENDAR_DNS` takes effect only while `docker-compose.lan-dns.yml` is in `COMPOSE_FILE`, but
`KYCALENDAR_BACKUP_ALLOW_PRIVATE_RECOVERY` persists in `.env` on its own and keeps relaxing destination checks until you
remove it.

### Upgrading from plaintext local backups

Earlier builds wrote unencrypted backups into the backup directory. The variable is now `KYCALENDAR_BACKUP_DIR` and
means sealed capsules. Retention deliberately never touches files it did not write, so old
plaintext backups stay where they are: move them out of the directory, keep them until a
restore from a capsule has been proven, then remove them securely. They are the live
directory in the clear.

### Upgrading to the `kycalendar` service name

Earlier builds claimed and sealed under `KY_APP_NAME`; the service name is now fixed at
`kycalendar`. An instance paired under another name must re-pair: the KyRecovery admin
revokes the old token, then pair again from the screen (the same key comes back, so it is
accepted). Local copies written under the old name are neither listed nor pruned; move them
out by hand. An explicitly empty `KY_BACKUP_DIR=` (local copies off) does not carry over:
compose now defaults to `/app/backups`, so set `KYCALENDAR_BACKUP_DIR=` in `.env` to keep
them off.

### Restoring

`docs/RESTORE.md` is the runbook: opening a capsule with the custodians' cards, putting the
result in service, and what to distrust afterwards. Drill it once a quarter with real cards.

## Upgrading after the Busnes-app owner move

The GitHub organisation was renamed on 2026-09-16 and the image now lives at `ghcr.io/busnes-app/kycalendar`. The project no longer controls `ghcr.io/busness-app`; GHCR does not redirect it, and anything served under that name must be treated as untrusted. If `KY_IMAGE` still names the old namespace or image name, re-pinning is required, not optional: inspect `git remote -v` before any `git pull`, `make ci`, or `docker compose` command, and replace a retired-owner remote with `https://github.com/Busnes-app/kycalendar.git` (prefer a fresh clone plus a known commit). Then remove `KY_IMAGE` to follow the compose default or verify and pin a digest using `docs/RESTORE.md` before pulling.
