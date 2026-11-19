#!/usr/bin/env python3
"""HTTP steps of scripts/check-postgres-cutover.sh, with the standard library only.

  ready  BASE            /readyz answers 200
  seed   BASE            ann signs in (replacing her temporary password) and adds an event
  verify BASE            the admin signs in, and ann reads the event
  backup BASE FIXTURE    the admin pins the fixture's public key and runs Back up now
"""
import base64
import http.cookiejar
import json
import sys
import urllib.error
import urllib.request

TITLE = "Cutover check"
ANN_TEMP, ANN_PASS = "Cutover-Temp-Pass-1", "Cutover-Ann-Pass-12"
ADMIN_TEMP, ADMIN_PASS = "Cutover-Admin-Pass-1", "Cutover-Admin-Pass-12"


class Client:
    def __init__(self, base):
        self.base = base
        self.jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar))

    def call(self, method, path, body=None):
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(self.base + path, data=data, method=method)
        req.add_header("Content-Type", "application/json")
        for c in self.jar:
            if c.name == "ky_csrf":
                req.add_header("X-CSRF-Token", c.value)
        try:
            with self.opener.open(req, timeout=60) as res:
                return res.status, json.loads(res.read() or b"null")
        except urllib.error.HTTPError as e:
            return e.code, e.read().decode(errors="replace")

    def sign_in(self, user, temp, final):
        """Signs in with the final password, or replaces the temporary one first."""
        code, out = self.call("POST", "/api/auth/login", {"username": user, "password": final})
        if code == 200:
            return
        code, out = self.call("POST", "/api/auth/login", {"username": user, "password": temp})
        if code != 200:
            sys.exit(f"{user} could not sign in: {code} {out}")
        if out["user"].get("must_change_password"):
            code, out = self.call("POST", "/api/auth/change-password", {"current_password": temp, "new_password": final})
            if code != 200:
                sys.exit(f"{user} could not replace the temporary password: {code} {out}")
            code, out = self.call("POST", "/api/auth/login", {"username": user, "password": final})
            if code != 200:
                sys.exit(f"{user} could not sign in again: {code} {out}")


WINDOW = "?start=2026-01-01T00:00:00Z&end=2026-12-31T00:00:00Z&tz=UTC"


def main(cmd, base, *rest):
    c = Client(base)
    if cmd == "ready":
        sys.exit(0 if c.call("GET", "/readyz")[0] == 200 else 1)
    if cmd == "seed":
        c.sign_in("ann", ANN_TEMP, ANN_PASS)
        code, cals = c.call("GET", "/api/calendars")
        if code != 200 or not cals:
            sys.exit(f"ann has no calendar: {code} {cals}")
        cal = cals[0]["id"]
        code, out = c.call("POST", f"/api/calendars/{cal}/events", {
            "title": TITLE, "start": "2026-06-01T09:00:00Z", "end": "2026-06-01T10:00:00Z", "zone": "UTC"})
        if code not in (200, 201):
            sys.exit(f"could not add the event: {code} {out}")
        return
    if cmd == "verify":
        Client(base).sign_in("admin", ADMIN_TEMP, ADMIN_PASS)
        c.sign_in("ann", ANN_TEMP, ANN_PASS)
        code, events = c.call("GET", "/api/events" + WINDOW)
        if code != 200 or TITLE not in json.dumps(events):
            sys.exit(f"ann does not see the event on PostgreSQL: {code} {events}")
        return
    if cmd == "backup":
        fixture = json.load(open(rest[0]))
        c.sign_in("admin", ADMIN_TEMP, ADMIN_PASS)
        key = fixture["PublicKey"]
        if len(base64.b64decode(key)) != 1216:
            sys.exit("the fixture public key is not a recovery public key")
        code, out = c.call("POST", "/api/backup/pin-key", {"public_key": key, "threshold": 2, "total_shares": 3})
        if code != 200:
            sys.exit(f"pin: {code} {out}")
        code, out = c.call("POST", "/api/backup/deposit")
        if code != 200 or not out.get("local_path"):
            sys.exit(f"Back up now: {code} {out}")
        return
    sys.exit(f"unknown step {cmd}")


if __name__ == "__main__":
    main(*sys.argv[1:])
