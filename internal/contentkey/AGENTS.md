# Content keys

## Purpose
Content encryption at rest's keys and sealed byte formats, version 1
(`docs/CONTENT-ENCRYPTION-FORMAT.md`; design `docs/superpowers/specs/2026-11-22-content-encryption-design.md`).

## Ownership
Owns account and calendar keys, holder seed wraps, unlock slots and their KEKs, recovery codes,
worker keys, part sealing and the part list encoding. It has no store, HTTP or iCalendar code:
the skeleton split and merge are `internal/skeleton`, the tables are migration 38, and nothing
calls either until build step CE2.

## Local Contracts
- **Format version 1 is fixed.** The suite is X-Wing (`MLKEM768X25519`), HKDF-SHA256 and
  AES-256-GCM, named by no field. Every `info` string and AAD prefix carries `v1`.
  - A change to any byte format, info string or AAD is a new version: new strings and a new list
    magic, never an edit of version 1.
  - `docs/CONTENT-ENCRYPTION-FORMAT.md` changes in the same commit as the code.
- **`testdata/vectors.json` must keep opening.** Regenerate it with `go test -run
  TestVectorsStillOpen -update` only for a new format version, and say so in the PR. Clients
  outside the server (option 1) rely on these bytes.
- **Every wrap binds what it is for.**
  - A part binds its calendar, object UID, kind, `keep-alarms` flag, sequence number, label and
    worker key ID.
  - A seed wrap binds its calendar and holder.
  - A slot is a key pair. Its private seed binds its kind, user and slot ID, and the account key
    sealed to it binds those and the account key version.
  - A worker key binds its calendar, calendar key version and ID.
  - A seed wrap binds its calendar key version too, and a previous account key binds its user and
    version.

  A new readable field joins the AAD.
- **Keys are versioned (E3, E11).** A rotation adds account and calendar key versions and keeps
  the old ones. A part names and binds its calendar key version, and opens only with it.
- **Parts bind their sequence number (`parts_version`), never their position.** Removing a
  superseded part must leave the rest openable.
- **KEK rules:**
  - Chosen secrets (`passphrase`, `password`) go only through `PassphraseKEK`, whose bounds
    refuse cheap or memory-exhausting stored parameters.
  - High-entropy secrets go through `SecretKEK` and need at least 128 bits.
- **Failures:** every open failure is `ErrOpen`, with nothing about which binding failed.

## Verification
`go test ./internal/contentkey` (unit tests and the stored vectors), and the repository's
`make ci`.
