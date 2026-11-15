# internal/booking

## Purpose
The pure part of booking pages (spec `docs/superpowers/specs/2026-11-15-booking-page-design.md`):
a booking type's rules, the slot grid, busy scans over stored objects, visitor text, and the
calendar objects a booking mails and stores. No storage, HTTP or mail: `internal/store`
(`bookings.go`) and `internal/api` (`booking.go`) call it.

## Local Contracts
- **Grid:** each window starts its slots at its own start and steps by the duration in absolute
  time; a slot ends by the window's end. Windows are wall-clock `HH:MM` (end may be `24:00`) in
  the type's IANA zone, built with `time.Date`, so DST shortens or lengthens that day's window.
- **Offered:** on the grid, start ≥ now + notice and < now + horizon days, free of every busy
  period widened by the buffers (`Free`), and the type's count for that local day (`Day`) below
  the cap. `Offered` is the only test a hold or reschedule start passes.
- **Busy:** `calendar.BusyTimes` over the given objects (floating times in the type's zone;
  transparent and cancelled instances free, tentative busy), counting by day the instances of
  objects marked `X-KYCALENDAR-BOOKING-TYPE:<type id>` (`TypeProp`). Past 5000 instances or the
  deadline it is `ErrTooBusy`.
- **Visitor text:** `CleanName` (1-100 characters, no control, format or separator characters,
  no `"`), `CleanNote` (≤ 1000 characters, newline and tab only, CRLF to LF), `CleanTitle`.
- **`HoldMessage`** is METHOD:REQUEST, STATUS:TENTATIVE, SEQUENCE:0 with fixed text and the
  confirm link only: never the visitor's name or note. **`Booked`** is the member's event
  (SEQUENCE:1, same UID, `TypeProp`); the caller invites the booker and names them
  (`NameAttendee`). **`Move`** rewrites DTSTART/DTEND for a reschedule.

## Verification
- `go test ./internal/booking/`
