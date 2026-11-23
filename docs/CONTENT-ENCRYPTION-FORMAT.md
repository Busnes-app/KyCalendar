# Content encryption formats, version 1

These are the byte formats of KyCalendar's content encryption at rest. The design and its reasons
are in [the design spec](superpowers/specs/2026-11-22-content-encryption-design.md); the code is
`internal/contentkey`. Anything that reads or writes sealed calendar content without the server,
such as a device client in a later end-to-end phase, follows this document. The test vectors in
`internal/contentkey/testdata/vectors.json` must open with any implementation.

Nothing in the server uses these formats until build step CE2.

## Suite

There is one fixed suite, version 1, and no field names it:

- **KEM:** X-Wing, ML-KEM-768 with X25519 (`MLKEM768X25519`, HPKE KEM ID `0x647a`). It is the
  same KEM as the suite's capsules and recovery keys.
- **KDF:** HKDF-SHA256.
- **AEAD:** AES-256-GCM, 12-byte nonce and 16-byte tag.

Every `info` string below contains `v1`, so a ciphertext moved into another use or version fails
to open.

**Bindings.** An AAD below written as `bind(a, b, …)` is each field as a 4-byte big-endian
length followed by its bytes, concatenated. No two field lists give the same bytes.

**AES sealing** is `nonce(12) ‖ AES-256-GCM(key, aad, plaintext)` with a random nonce.

**HPKE sealing** is base mode with one message per context: `enc(1120) ‖ ciphertext`. `enc` is
the X-Wing encapsulated key (the 1,088-byte ML-KEM-768 ciphertext and a 32-byte X25519 point).

## Keys

- **Account key.** A random 32-byte seed. It is an X-Wing private key as `crypto/hpke`'s
  `NewPrivateKey` takes it. `account_keys.public_key` is the serialized X-Wing public key.
- **Versions.** Account and calendar keys each have a version, starting at 1. A rotation (design
  E11) adds a version and keeps the older ones, so content sealed under them still opens.
- **Calendar key.** A random 32-byte seed per version, from which two keys are derived:
  - the X-Wing key pair, from the seed `HKDF-SHA256(seed, salt = none, info = "kycalendar content v1 calendar kem", 32)`;
  - the AES-256-GCM key, `HKDF-SHA256(seed, salt = none, info = "kycalendar content v1 calendar aes", 32)`.

  `calendar_keys.public_key` is the calendar's X-Wing public key. An unlocked request compares it
  with the key derived from the seed before sealing to it.
- **Calendar seed wrap** (`calendar_key_holders.wrapped_seed`): the HPKE seal of the 32-byte
  calendar seed to the holder's account public key, with info `kycalendar content v1 calendar seed`
  and AAD `bind(calendar ID, calendar key version, holder user ID)`, the version in decimal.
- **Worker key.** A random 32-byte AES-256-GCM key with a random 16-byte ID, shown as hex.
  `worker_keys.wrapped` is the HPKE seal of the key to one calendar key version's public key, with
  info `kycalendar content v1 worker key` and AAD `bind(calendar ID, calendar key version, worker
  key ID)`.
- **Previous account key** (`account_previous_keys`): the HPKE seal of an older account seed to the
  current account public key, with info `kycalendar content v1 previous account key` and AAD
  `bind(user ID, older version)`.

## Unlock slots

A slot is its own X-Wing key pair:
- `key_slots.wrapped` is the AES seal of the slot's 32-byte private seed. Its key is the slot's
  KEK, and its AAD is `bind("kycalendar content v1 slot", kind, user ID, slot ID)`.
- `key_slots.account_wrap` is the HPKE seal of the account seed to the slot's public key, with info
  `kycalendar content v1 account key` and AAD `bind(kind, user ID, slot ID, account key version)`.

So a new account key reaches every slot with no slot's secret, which is what lets a rotation run
with no user present.

There are two KEK rules:

- **High-entropy secrets:** `apppass`, `mcp`, `session`, `passkey` (the WebAuthn PRF output),
  `recovery`, `transition` and `device`. The KEK is
  `HKDF-SHA256(secret, salt = bind(user ID, slot ID), info = "kycalendar content v1 slot " + kind, 32)`.
  The secret must be at least 16 bytes.
- **Chosen secrets:** `passphrase` and `password`. The KEK is
  `Argon2id(passphrase, salt, time, memory, threads, 32)`, version 19. `key_slots.params` holds
  `{"salt", "time", "memory", "threads"}`, with memory in KiB. The defaults are 3 passes, 64 MiB
  and 4 lanes, with a random 16-byte salt.

  A reader refuses parameters outside these bounds: salt of at least 16 bytes, 1–10 passes,
  8–256 MiB and 1–16 lanes.

A **recovery code** is 16 random bytes, shown as 26 RFC 4648 base32 symbols without padding, in
groups of 4, 4, 4, 4, 5 and 5. When reading one, case, spaces and dashes are ignored.

## Sealed parts

`calendar_objects.sealed` is a part list:

```
"KCP1" ‖ count(u16)
then, per part:
  kind(u8) ‖ flags(u8) ‖ seq(u64) ‖ key_version(u32) ‖ label_len(u8) ‖ label ‖ key_id_len(u8) ‖ key_id ‖ sealed_len(u32) ‖ sealed
```

All integers are big-endian.

- **`kind`:** 1 is a base (a whole iCalendar body), 2 is a patch (operations on named
  properties).
- **`flags`:** bit 0 is `keep-alarms` (a base only). Any other bit set is malformed.
- **`seq`:** the object's `parts_version` when the part was written. It is unique within the
  object and orders the parts. A part does not record its position, because removing an earlier
  part must not change the parts after it.
- **`key_version`:** the calendar key version the part is sealed under, or that its worker key is
  wrapped to. A reader opens it with that version only.
- **`label`:** a patch's readable label, its operation, property and component key, never a value.
  A base has an empty label and a patch must have one.
- **`key_id`:** empty for a part sealed under the calendar's AES key, or the worker key's 16-byte
  ID.
- **`sealed`:** the AES seal of the part's plaintext, under the calendar's AES key or the worker
  key. The AAD is
  `bind("kycalendar content v1 part", calendar ID, object UID, kind, keep, seq, key version, label, key ID)`,
  where:
  - `kind` is the decimal kind;
  - `keep` is `"1"` or `"0"`;
  - `seq` and `key version` are decimal;
  - `key ID` is lowercase hex, or empty.

A list holds at most 64 parts. A reader refuses unknown magic, a truncated or trailing list, a
repeated `seq`, a base with a label and a patch without one.

The plaintext of a base and of a patch, and how parts merge with the skeleton, are defined in
build step CE1b.
