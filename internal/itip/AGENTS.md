# internal/itip

## Purpose
Pure iTIP (RFC 5546) rules for server-side scheduling (RFC 6638): which messages a write
produces, how inbound messages are checked, and how replies and organizer updates land in a
stored object. Design: `docs/superpowers/specs/2026-10-17-kycalendar-v1b-invitations-design.md`.

## Ownership
Owns calendar user addresses, message building, the inbound boundary and reply merging.
Delivery, queues, credentials and storage belong to their callers; nothing here does I/O.

## Local Contracts
- `Address` is `mailto:` plus a lower-cased RFC 5322 addr-spec of at most 254 bytes, with no
  display name, CR/LF, comma, semicolon, space, quote or angle bracket. `ParseAddress` is the
  only constructor.
- `Outgoing(self, old, new, now)` (old nil: create; new nil: delete):
  - **Organizer** (ORGANIZER is self):
    - REQUEST to every server-scheduled attendee on a create or when `Changed`;
    - CANCEL to removed attendees;
    - CANCEL to all on delete.
    An answer recorded on the organizer's copy (PARTSTAT only) sends nothing.
  - **Attendee:** a REPLY to the organizer only when self's own PARTSTAT changed and ORGANIZER is
    unchanged since the previous version (so a rewritten ORGANIZER cannot aim a reply at any
    address); a delete is a DECLINED REPLY; an attendee's first write and their other edits send
    nothing.
  - **Anyone else, or no ORGANIZER:** nothing.
  - **Never scheduled by the server:** attendees with `SCHEDULE-AGENT` other than SERVER.
- Messages never carry VALARMs or the SCHEDULE-AGENT, SCHEDULE-STATUS and SCHEDULE-FORCE-SEND
  parameters. A REPLY carries only UID, RECURRENCE-ID, DTSTART, SEQUENCE, ORGANIZER, DTSTAMP and
  self's ATTENDEE, so other attendees and the text never leave in a reply. Building a message
  never changes the stored object.
- `Significant`: DTSTART, DTEND, DURATION, DUE, RRULE, RDATE, EXDATE, STATUS or LOCATION
  changed, or an occurrence added or removed. `Changed` adds SUMMARY, DESCRIPTION, ORGANIZER,
  CONFERENCE and the attendee set.
- `BumpSequence` raises SEQUENCE past the old object's highest on a significant change the
  client did not number.
- Inbound is hostile input:
  - `Decode` refuses bodies over `calendar.MaxObjectSize` and `calendar.LongParams` input, and
    turns go-ical panics into `ErrInvalid`.
  - `Validate` accepts METHOD REQUEST, REPLY or CANCEL; VEVENT and VTIMEZONE only; one UID;
    ORGANIZER on every VEVENT; at most 100 attendees per VEVENT and 1000 per object; every
    address valid.
- `ApplyReply(stored, reply, sender)`:
  - changes only the PARTSTAT of the ATTENDEE equal to sender, never times, text, other
    attendees, or overrides the object lacks;
  - ignores a reply to an older SEQUENCE and an unknown PARTSTAT;
  - refuses another UID (`ErrUIDMismatch`) and a sender who is not an attendee
    (`ErrNotAttendee`).
- Building a message or copy re-encodes the object. go-ical's encoder is stricter than its decoder,
  so a client's object can decode and still not encode: `Outgoing` and `AttendeeCopy` return
  `ErrInvalid` then, never panic (the delivery worker runs outside HTTP recovery).
- `AttendeeCopy(request, existing, self)`: the organizer's object without METHOD. It keeps
  self's VALARMs from `existing`, and self's PARTSTAT unless the change is significant, which
  resets it to NEEDS-ACTION.

## Verification
- `go test -race ./internal/itip/`
- `go test ./internal/itip/ -run '^$' -fuzz FuzzInbound -fuzztime 60s` (3.6M inputs clean with Outgoing and AttendeeCopy in the target, 2026-10-19)

## Child DOX Index
None.
