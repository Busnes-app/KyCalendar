"""CalDAV interop checks against a running KyCalendar, driven by scripts/interop/run.sh.

An independent client (python caldav) plus raw HTTP for byte-exact and header checks; with
KY_INTEROP_SCHEDULING=1 it also checks discovery, an internal invite and answer, and outbox
free/busy (37 checks). Every
check prints PASS or FAIL; the exit status is the number of failures.
"""

import base64
import os
import re
import sys
import urllib.error
import urllib.request
import uuid
from datetime import datetime, timezone

import caldav

BASE = os.environ["KY_BASE"]
USERS = {
    "alice": (os.environ["ALICE_ID"], os.environ["ALICE_TOK"]),
    "bob": (os.environ["BOB_ID"], os.environ["BOB_TOK"]),
}
failures = 0


def check(name, ok, detail=""):
    global failures
    print(("PASS " if ok else "FAIL ") + name + (f" ({detail})" if detail and not ok else ""))
    failures += 0 if ok else 1


def raw(user, method, url, body=None, headers=None):
    """One HTTP request as user; returns (status, headers, body bytes)."""
    _, tok = USERS[user]
    req = urllib.request.Request(url if url.startswith("http") else BASE + url, data=body, method=method)
    req.add_header("Authorization", "Basic " + base64.b64encode(f"{user}:{tok}".encode()).decode())
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req) as resp:
            return resp.status, resp.headers, resp.read()
    except urllib.error.HTTPError as e:
        return e.code, e.headers, e.read()


def event(uid, start, end, extra=""):
    return (
        "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//interop//EN\r\nBEGIN:VEVENT\r\n"
        f"UID:{uid}\r\nDTSTAMP:20261001T000000Z\r\nDTSTART:{start}\r\nDTEND:{end}\r\n"
        f"SUMMARY:Interop {uid}\r\nX-INTEROP-KEEP:yes\r\n{extra}END:VEVENT\r\nEND:VCALENDAR\r\n"
    ).encode()


SYNC_REPORT = """<?xml version="1.0"?><D:sync-collection xmlns:D="DAV:"><D:sync-token>{}</D:sync-token>
<D:sync-level>1</D:sync-level><D:prop><D:getetag/></D:prop></D:sync-collection>"""


def sync(user, cal, token=""):
    status, _, body = raw(user, "REPORT", cal, SYNC_REPORT.format(token).encode(),
                          {"Content-Type": "application/xml", "Depth": "1"})
    text = body.decode()
    m = re.search(r"<[^>]*sync-token>([^<]+)<", text)
    return status, text, m.group(1) if m else ""


alice_id, alice_tok = USERS["alice"]
home = f"/dav/{alice_id}/calendars/default/"

# Discovery: well-known and bare host both lead to the principal.
client = caldav.DAVClient(url=BASE + "/", username="alice", password=alice_tok)
principal = client.principal()
check("principal from bare host", alice_id in str(principal.url), str(principal.url))
wk = caldav.DAVClient(url=BASE + "/.well-known/caldav", username="alice", password=alice_tok).principal()
check("principal from well-known", alice_id in str(wk.url), str(wk.url))
cals = principal.calendars()
check("default calendar listed", any(str(c.url).endswith("/calendars/default/") for c in cals), str([c.url for c in cals]))
cal = next(c for c in cals if str(c.url).endswith("/calendars/default/"))

# OPTIONS: calendar-access always; scheduling only when the server enables it.
status, hdrs, _ = raw("alice", "OPTIONS", home)
dav = hdrs.get("DAV", "")
check("OPTIONS 204 advertises calendar-access", status == 204 and "calendar-access" in dav, dav)
want_sched = os.environ.get("KY_INTEROP_SCHEDULING") == "1"
check("calendar-auto-schedule advertised only when enabled", ("calendar-auto-schedule" in dav) == want_sched, dav)

# Sync baseline before any write.
status, _, token0 = sync("alice", home)
check("initial sync-collection", status == 207 and token0 != "", f"{status} {token0}")

# Plain event, no attendees: PUT, byte-identical GET, ETag, conditional writes.
uid = f"interop-{uuid.uuid4()}"
href = f"{home}{uid}.ics"
body = event(uid, "20261105T100000Z", "20261105T110000Z")
status, hdrs, _ = raw("alice", "PUT", href, body, {"Content-Type": "text/calendar", "If-None-Match": "*"})
etag = hdrs.get("ETag", "")
check("PUT new event 201", status == 201, str(status))
status, _, _ = raw("alice", "PUT", href, body, {"Content-Type": "text/calendar", "If-None-Match": "*"})
check("PUT If-None-Match:* on existing is 412", status == 412, str(status))
status, hdrs, got = raw("alice", "GET", href)
check("GET is byte-identical to PUT", status == 200 and got == body, str(status))
check("GET ETag matches PUT ETag", etag != "" and hdrs.get("ETag") == etag, f"{etag} {hdrs.get('ETag')}")
status, _, _ = raw("alice", "PUT", href, body, {"Content-Type": "text/calendar", "If-Match": '"stale"'})
check("PUT with stale If-Match is 412", status == 412, str(status))

# REPORT via the independent client: time-range query and multiget.
found = cal.search(start=datetime(2026, 11, 5, tzinfo=timezone.utc), end=datetime(2026, 11, 6, tzinfo=timezone.utc), event=True)
check("calendar-query time-range finds the event", any(uid in str(e.url) for e in found), str([e.url for e in found]))
none = cal.search(start=datetime(2026, 12, 1, tzinfo=timezone.utc), end=datetime(2026, 12, 2, tzinfo=timezone.utc), event=True)
check("calendar-query outside the range is empty", not any(uid in str(e.url) for e in none))
mg = cal.calendar_multiget([caldav.lib.url.URL.objectify(BASE + href)])
check("calendar-multiget returns the event", len(mg) == 1 and "X-INTEROP-KEEP:yes" in mg[0].data, str(len(mg)))

# Sync tokens: the write shows up, an edit moves the token, a delete is reported.
status, text, token1 = sync("alice", home, token0)
check("sync after PUT reports the event", status == 207 and uid in text and token1 != token0, text[:200])
edited = event(uid, "20261105T100000Z", "20261105T120000Z")
status, hdrs, _ = raw("alice", "PUT", href, edited, {"Content-Type": "text/calendar", "If-Match": etag})
# Today's behaviour, pinned: an update answers 201 (upstream TODO), with a new ETag.
check("PUT with current If-Match is 201 with a new ETag", status == 201 and hdrs.get("ETag", etag) != etag, str(status))
status, text, token2 = sync("alice", home, token1)
check("sync after edit reports only the change", status == 207 and uid in text and token2 != token1, text[:200])
status, text, token3 = sync("alice", home, token2)
check("sync with current token is empty", status == 207 and uid not in text and token3 == token2, text[:200])
status, _, _ = raw("alice", "DELETE", href)
check("DELETE 204", status == 204, str(status))
status, text, _ = sync("alice", home, token2)
check("sync after DELETE reports 404 for the href", status == 207 and uid in text and "404" in text, text[:300])
status, text, _ = sync("alice", home, "http://kycalendar/sync/garbage")
check("foreign sync token is valid-sync-token", status in (403, 409) and "valid-sync-token" in text, f"{status} {text[:200]}")

# A second user's home is refused.
bob_id, _ = USERS["bob"]
status, _, _ = raw("alice", "PROPFIND", f"/dav/{bob_id}/calendars/", b"", {"Depth": "1"})
check("another user's home is 403", status == 403, str(status))

# Free/busy (RFC 4791 §7.10), when the server supports it.
if os.environ.get("KY_INTEROP_FREEBUSY") == "1":
    busy = f"interop-busy-{uuid.uuid4()}"
    free = f"interop-free-{uuid.uuid4()}"
    raw("alice", "PUT", f"{home}{busy}.ics", event(busy, "20261110T090000Z", "20261110T100000Z", "LOCATION:Secret room\r\n"), {"Content-Type": "text/calendar"})
    raw("alice", "PUT", f"{home}{free}.ics", event(free, "20261110T130000Z", "20261110T140000Z", "TRANSP:TRANSPARENT\r\n"), {"Content-Type": "text/calendar"})
    q = """<?xml version="1.0"?><C:free-busy-query xmlns:C="urn:ietf:params:xml:ns:caldav">
<C:time-range start="20261110T000000Z" end="20261111T000000Z"/></C:free-busy-query>"""
    status, hdrs, got = raw("alice", "REPORT", home, q.encode(), {"Content-Type": "application/xml", "Depth": "1"})
    text = got.decode()
    check("free-busy-query is 200 text/calendar", status == 200 and hdrs.get("Content-Type", "").startswith("text/calendar"), f"{status} {hdrs.get('Content-Type')}")
    check("free-busy has the busy period", "FREEBUSY" in text and "20261110T090000Z/20261110T100000Z" in text, text)
    check("free-busy omits the transparent event", "20261110T130000Z" not in text, text)
    check("free-busy reveals no event details", "Secret" not in text and "Interop" not in text and busy not in text, text)
    fb = cal.freebusy_request(datetime(2026, 11, 10, tzinfo=timezone.utc), datetime(2026, 11, 11, tzinfo=timezone.utc))
    check("independent client parses the VFREEBUSY", "VFREEBUSY" in fb.data, fb.data[:200])

# Server-side scheduling (RFC 6638), when the server runs with it on.
if want_sched:
    import time

    check("client sees scheduling support", client.check_scheduling_support() is True)
    addrs = [str(a) for a in principal.calendar_user_address_set()]
    check("calendar-user-address-set is the user's email", "mailto:alice@example.com" in [a.lower() for a in addrs], str(addrs))
    check("schedule inbox discovered", principal.schedule_inbox().url.path.endswith("/calendars/_inbox/"))
    check("schedule outbox discovered", principal.schedule_outbox().url.path.endswith("/calendars/_outbox/"))

    bob_id, bob_tok = USERS["bob"]
    bob_home = f"/dav/{bob_id}/calendars/default/"
    muid = f"interop-meet-{uuid.uuid4()}"
    meet = event(muid, "20261112T150000Z", "20261112T160000Z",
                 "ORGANIZER:mailto:alice@example.com\r\nATTENDEE;PARTSTAT=ACCEPTED:mailto:alice@example.com\r\n"
                 "ATTENDEE;RSVP=TRUE:mailto:bob@example.com\r\nLOCATION:Room 4\r\n")
    status, _, _ = raw("alice", "PUT", f"{home}{muid}.ics", meet, {"Content-Type": "text/calendar", "If-None-Match": "*"})
    check("organizer PUT with an internal attendee", status == 201, str(status))

    def bob_copy():
        bob_cal = caldav.DAVClient(url=BASE + bob_home, username="bob", password=bob_tok).calendar(url=BASE + bob_home)
        for ev in bob_cal.events():
            if muid in ev.data:
                return ev
        return None

    copy = None
    for _ in range(30):  # the delivery worker runs every 5 s
        copy = bob_copy()
        if copy:
            break
        time.sleep(1)
    check("invitation delivered to the attendee's calendar", copy is not None and "NEEDS-ACTION" in copy.data)

    if copy is not None:
        status, _, _ = raw("bob", "PUT", str(copy.url.path), copy.data.replace("NEEDS-ACTION", "ACCEPTED").encode(),
                           {"Content-Type": "text/calendar"})
        check("attendee accepts", status in (201, 204), str(status))
        answered = False
        for _ in range(30):
            _, _, got = raw("alice", "GET", f"{home}{muid}.ics")
            if b"PARTSTAT=ACCEPTED" in got and b"bob@example.com" in got and got.count(b"ACCEPTED") >= 2:
                answered = True
                break
            time.sleep(1)
        check("organizer's copy records the answer", answered)

    fb = principal.freebusy_request(datetime(2026, 11, 12, tzinfo=timezone.utc), datetime(2026, 11, 13, tzinfo=timezone.utc),
                                    ["mailto:bob@example.com"])
    bob_fb = fb.get("mailto:bob@example.com")  # the client returns {address: FreeBusy, "errors": {...}}
    text = str(bob_fb.data) if bob_fb is not None else repr(fb)
    check("outbox free/busy has bob's busy meeting", "20261112T150000Z/20261112T160000Z" in text, text[:300])
    check("outbox free/busy reveals no details", "Room 4" not in text and muid not in text, text[:300])

print(f"{failures} failure(s)")
sys.exit(min(failures, 100))
