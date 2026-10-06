# Plan 1 smoke evidence (2026-10-06)

Automated smoke with an independent CalDAV client (python `caldav` 3.3.1, Python 3.14) against
the built binary on plain HTTP at localhost. Real clients need HTTPS. This is not the Plan 4
interop gate.

## Setup

```
go build -o /tmp/kycal-smoke/kycalendar ./cmd/server
python3 -m venv /tmp/kycal-smoke-venv && /tmp/kycal-smoke-venv/bin/pip install caldav
bash /tmp/kycal-smoke/start.sh            # fresh KY_DATA_DIR, port 18431, KY_CAPTCHA_PROVIDER=none, records server.pid
python3 -I /tmp/kycal-smoke/mkuser.py     # everyday user alice
/tmp/kycal-smoke-venv/bin/python -I /tmp/kycal-smoke/smoke.py
kill "$(cat /tmp/kycal-smoke/server.pid)"
```

No HTTP route or CLI creates a local everyday user (`init-admin` makes admins, SCIM users carry
no password), so `mkuser.py` clones the bootstrap admin row into a `role='user'` row in the
throwaway SQLite file. The login and app-password path under test is the real one.

## Result

```
PASS login as everyday user
create response keys: ['created_at', 'id', 'label', 'password']
PASS app password created
PASS principal discovered from bare URL http://127.0.0.1:18431/dav/usr_smokealice01/
PASS calendars listed ['http://127.0.0.1:18431/dav/usr_smokealice01/calendars/default/']
PASS event created http://127.0.0.1:18431/dav/usr_smokealice01/calendars/default/smoke-uid-0001%40example.test.ics
PASS read back SUMMARY
PASS raw GET is byte-identical to PUT body
PASS edit persisted
PASS time-range search finds it
PASS time-range outside returns nothing
PASS deleted event is gone
PASS app password revoked via API
PASS call after revoke gets 401 AuthorizationError at 'http://127.0.0.1:18431/.well-known/caldav', reason Unauth
PASS raw PROPFIND after revoke is 401 401
SMOKE OK
```

The line "principal discovered from bare URL" is mislabelled: the client started at
`/.well-known/caldav` (see `smoke.py`), so this run tested well-known discovery, not the bare host.

Process hygiene: the server was stopped by recorded PID; `ps` showed no kycalendar process afterwards.

## Findings

- In the run above, PROPFIND on the bare `/` returned the SPA HTML (200), not a DAV response.
  Fixed afterwards: PROPFIND, REPORT and OPTIONS on `/` now answer 308 to `/.well-known/caldav`,
  which answers 308 to `/dav/<user-id>/`. 308 rather than 301 because a 301 lets a client turn
  PROPFIND into GET (Go's client does), which would land on the SPA. Re-checked with the same
  python `caldav` client given `url="http://127.0.0.1:18432/"`: the pre-fix binary failed
  (`AttributeError` parsing the SPA), the fixed one printed
  `principal from bare host: http://127.0.0.1:18432/dav/usr_smokealice01/` and listed
  `calendars/default/`. Real devices still need the bare-host column below.
- The `caldav` library re-serializes event data on read (property order, line endings); the
  byte-identical check therefore uses a plain urllib PUT and GET.

## Scripts

`start.sh`
```bash
#!/bin/bash
# Start the server on a throwaway data dir; record the PID. Usage: start.sh
set -eu
D=/tmp/kycal-smoke-data
rm -rf "$D"
mkdir -p "$D"
KY_PORT=18431 KY_HOST=127.0.0.1 KY_DATA_DIR="$D" KY_DB_DRIVER=sqlite \
  KY_CAPTCHA_PROVIDER=none KY_ADMIN_PASSWORD='SmokeBootstrapPass123!' KY_APP_URL=http://127.0.0.1:18431 \
  /tmp/kycal-smoke/kycalendar >/tmp/kycal-smoke/server.log 2>&1 &
echo $! >/tmp/kycal-smoke/server.pid
for _ in $(seq 1 30); do
  if curl -s -o /dev/null http://127.0.0.1:18431/api/auth/me; then echo "up pid=$(cat /tmp/kycal-smoke/server.pid)"; exit 0; fi
  kill -0 "$(cat /tmp/kycal-smoke/server.pid)" 2>/dev/null || { echo "server died"; cat /tmp/kycal-smoke/server.log; exit 1; }
  sleep 0.5
done
echo "server did not come up"; exit 1
```

`mkuser.py`
```python
"""Clone the bootstrap admin row into an everyday user (no HTTP route creates local users)."""
import sqlite3

c = sqlite3.connect("/tmp/kycal-smoke-data/kycalendar.db")
c.execute(
    "insert into users (id, username, display_name, password_hash, role, status, sso_provider, must_change_password, created_at, updated_at)"
    " select 'usr_smokealice01', 'alice', 'Alice', password_hash, 'user', 'active', 'local', 0, created_at, updated_at from users where username='admin'"
)
c.commit()
print(list(c.execute("select username, role, status, must_change_password from users")))
```

`smoke.py`
```python
"""CalDAV smoke with the independent python `caldav` client against a local kycalendar."""
import base64
import http.cookiejar
import json
import sys
import urllib.error
import urllib.request

import caldav
from caldav.lib.error import AuthorizationError

BASE = "http://127.0.0.1:18431"
PASSWORD = "SmokeBootstrapPass123!"
SUMMARY = "Smoke ünïcode lunch; with, punctuation"
UID = "smoke-uid-0001@example.test"


def ok(name, cond, detail=""):
    print(("PASS " if cond else "FAIL ") + name + (" " + detail if detail else ""))
    if not cond:
        sys.exit(1)


jar = http.cookiejar.CookieJar()
op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))


def api(method, path, body=None):
    csrf = next((c.value for c in jar if c.name == "ky_csrf"), "")
    req = urllib.request.Request(
        BASE + path, method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={"Content-Type": "application/json", "X-CSRF-Token": csrf},
    )
    with op.open(req) as r:
        raw = r.read()
        return r.status, (json.loads(raw) if raw else None)


st, me = api("POST", "/api/auth/login", {"username": "alice", "password": PASSWORD})
ok("login as everyday user", st == 200 and me.get("authenticated") is True)
st, created = api("POST", "/api/app-passwords", {"label": "caldav smoke"})
print("create response keys:", sorted(created))
token = next(v for v in created.values() if isinstance(v, str) and v.startswith("kc_"))
pw_id = created.get("id")
ok("app password created", st in (200, 201))

auth = {"Authorization": "Basic " + base64.b64encode(f"alice:{token}".encode()).decode()}

ICS = f"""BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//smoke//EN
BEGIN:VEVENT
UID:{UID}
DTSTAMP:20261006T100000Z
DTSTART:20261010T120000Z
DTEND:20261010T130000Z
SUMMARY:{SUMMARY}
END:VEVENT
END:VCALENDAR
"""

with caldav.DAVClient(url=BASE + "/.well-known/caldav", username="alice", password=token) as client:
    principal = client.principal()
    ok("principal discovered from bare URL", principal is not None, str(principal.url))
    cals = principal.calendars()
    ok("calendars listed", len(cals) >= 1, str([str(c.url) for c in cals]))
    cal = cals[0]
    ev = cal.save_event(ICS)
    ok("event created", ev is not None, str(ev.url))
    got = cal.event_by_uid(UID)
    ok("read back SUMMARY", "SUMMARY:Smoke \u00fcn\u00efcode lunch\\; with\\, punctuation" in got.data)
    # raw PUT/GET with plain urllib: server must return exactly the bytes stored
    raw = ICS.replace("\n", "\r\n").replace(UID, "raw-uid-0002@example.test").encode()
    rurl = str(cal.url) + "raw-0002.ics"
    urllib.request.urlopen(urllib.request.Request(rurl, data=raw, method="PUT", headers={**auth, "Content-Type": "text/calendar; charset=utf-8"}))
    body = urllib.request.urlopen(urllib.request.Request(rurl, headers=auth)).read()
    ok("raw GET is byte-identical to PUT body", body == raw)
    urllib.request.urlopen(urllib.request.Request(rurl, method="DELETE", headers=auth))
    got.data = got.data.replace("SUMMARY:Smoke", "SUMMARY:EDITED Smoke")
    got.save()
    again = cal.event_by_uid(UID)
    ok("edit persisted", "SUMMARY:EDITED Smoke" in again.data)
    from datetime import datetime, timezone
    hits = cal.search(start=datetime(2026, 10, 10, tzinfo=timezone.utc), end=datetime(2026, 10, 11, tzinfo=timezone.utc), event=True, expand=False)
    ok("time-range search finds it", len(hits) == 1)
    miss = cal.search(start=datetime(2026, 11, 1, tzinfo=timezone.utc), end=datetime(2026, 11, 2, tzinfo=timezone.utc), event=True, expand=False)
    ok("time-range outside returns nothing", len(miss) == 0)
    again.delete()
    try:
        cal.event_by_uid(UID)
        ok("deleted event is gone", False)
    except caldav.error.NotFoundError:
        ok("deleted event is gone", True)

st, _ = api("DELETE", f"/api/app-passwords/{pw_id}")
ok("app password revoked via API", st in (200, 204))

try:
    with caldav.DAVClient(url=BASE + "/.well-known/caldav", username="alice", password=token) as client:
        client.principal().calendars()
    ok("call after revoke gets 401", False, "call succeeded")
except AuthorizationError as e:
    ok("call after revoke gets 401", "Unauth" in str(e), str(e)[:80])
    try:
        urllib.request.urlopen(urllib.request.Request(BASE + "/dav/usr_smokealice01/", method="PROPFIND", headers=auth))
        ok("raw PROPFIND after revoke is 401", False)
    except urllib.error.HTTPError as he:
        ok("raw PROPFIND after revoke is 401", he.code == 401, str(he.code))
print("SMOKE OK")
```

## Real devices - PENDING (Yoshi)

Run against the machine's HTTPS address. Sign in as an everyday user (not an administrator),
create an app password on Phones & apps, then for each client:

1. Add the account (iOS: Settings > Calendar > Accounts > Add Account > Other > Add CalDAV Account;
   DAVx5: Add account > Login with URL and user name, URL `https://<host>/`;
   Thunderbird: New Calendar > On the Network, location `https://<host>/`).
2. Create an event on the device.
3. Confirm the server has it: `PROPFIND` with `Depth: 1` on `/dav/<user-id>/calendars/default/`
   lists the new object, or the event shows up on a second client on the same account.
4. Edit it on the device and confirm the change syncs.
5. Delete it on the device and confirm it is gone.
6. Revoke the app password on Phones & apps and confirm the next sync fails.

Also record whether the client reached the principal when given only the bare host
`https://<host>/` (yes/no), and which URL it actually used if the log shows it.

| Client | Version | Reached principal from bare host? yes/no | 1 add | 2 create | 3 server sees it | 4 edit | 5 delete | 6 revoke | Notes |
|---|---|---|---|---|---|---|---|---|---|
| iOS Calendar | | | | | | | | | |
| DAVx5 | | | | | | | | | |
| Thunderbird | | | | | | | | | |

Do not mark Plan 1 done until these are filled in.
