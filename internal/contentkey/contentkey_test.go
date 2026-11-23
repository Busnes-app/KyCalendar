package contentkey

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func mustPair(t *testing.T) *KeyPair {
	t.Helper()
	k, err := NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// A seed rebuilds the same key pair, and a calendar's seed the same keys.
func TestKeysRebuildFromSeeds(t *testing.T) {
	a := mustPair(t)
	b, err := KeyPairFromSeed(a.Seed())
	if err != nil || !bytes.Equal(a.PublicKey(), b.PublicKey()) {
		t.Fatalf("account key from its seed: %v", err)
	}
	if _, err := KeyPairFromSeed(make([]byte, 31)); err == nil {
		t.Error("a 31-byte seed was accepted")
	}
	c, err := NewCalendarKey("cal_1", 1)
	if err != nil {
		t.Fatal(err)
	}
	d, err := CalendarKeyFromSeed("cal_1", 1, c.seed[:])
	if err != nil || !d.Matches(c.PublicKey()) || d.aes != c.aes {
		t.Fatalf("calendar key from its seed: %v", err)
	}
	if c.Matches(a.PublicKey()) {
		t.Error("a calendar key matched another key")
	}
}

// E13: a calendar seed wrapped to a holder opens only with that holder's key, for that calendar
// and that holder.
func TestCalendarSeedOpensOnlyForItsHolder(t *testing.T) {
	ann, bob := mustPair(t), mustPair(t)
	c, err := NewCalendarKey("cal_1", 1)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := c.WrapSeed("usr_ann", ann.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenCalendarSeed(ann, "cal_1", 1, "usr_ann", wrapped)
	if err != nil || !got.Matches(c.PublicKey()) {
		t.Fatalf("ann opens her wrap: %v", err)
	}
	for name, open := range map[string]func() error{
		"another account":  func() error { _, err := OpenCalendarSeed(bob, "cal_1", 1, "usr_ann", wrapped); return err },
		"another holder":   func() error { _, err := OpenCalendarSeed(ann, "cal_1", 1, "usr_bob", wrapped); return err },
		"another calendar": func() error { _, err := OpenCalendarSeed(ann, "cal_2", 1, "usr_ann", wrapped); return err },
		"truncated":        func() error { _, err := OpenCalendarSeed(ann, "cal_1", 1, "usr_ann", wrapped[:100]); return err },
		"another version":  func() error { _, err := OpenCalendarSeed(ann, "cal_1", 2, "usr_ann", wrapped); return err },
	} {
		if err := open(); !errors.Is(err, ErrOpen) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := c.WrapSeed("usr_ann", []byte("not a key")); err == nil {
		t.Error("a wrap to garbage succeeded")
	}
}

// E6: a slot is a key pair whose private seed opens only with its own secret, kind, account and
// slot ID; the account key is sealed to the slot's public key, for one key version.
func TestSlotsOpenOnlyWithTheirSecret(t *testing.T) {
	acct := mustPair(t)
	secret := bytes.Repeat([]byte{7}, 32)
	for _, kind := range []SlotKind{SlotAppPassword, SlotMCP, SlotSession, SlotPasskey, SlotRecovery, SlotTransition, SlotDevice} {
		kek, err := SecretKEK(kind, "usr_ann", "s1", secret)
		if err != nil {
			t.Fatal(kind, err)
		}
		slot, wrapped, err := NewSlot(kek, kind, "usr_ann", "s1")
		if err != nil {
			t.Fatal(err)
		}
		aw, err := SealAccountToSlot(slot.PublicKey(), kind, "usr_ann", "s1", 1, acct)
		if err != nil {
			t.Fatal(err)
		}
		opened, err := OpenSlot(kek, kind, "usr_ann", "s1", wrapped)
		if err != nil {
			t.Fatalf("%s opens: %v", kind, err)
		}
		if got, err := OpenAccount(opened, kind, "usr_ann", "s1", 1, aw); err != nil || !bytes.Equal(got.PublicKey(), acct.PublicKey()) {
			t.Fatalf("%s account: %v", kind, err)
		}
		other, _ := SecretKEK(kind, "usr_ann", "s1", bytes.Repeat([]byte{8}, 32))
		for name, err := range map[string]error{
			"wrong secret":  func() error { _, err := OpenSlot(other, kind, "usr_ann", "s1", wrapped); return err }(),
			"wrong kind":    func() error { _, err := OpenSlot(kek, "x", "usr_ann", "s1", wrapped); return err }(),
			"wrong user":    func() error { _, err := OpenSlot(kek, kind, "usr_bob", "s1", wrapped); return err }(),
			"wrong slot":    func() error { _, err := OpenSlot(kek, kind, "usr_ann", "s2", wrapped); return err }(),
			"wrong version": func() error { _, err := OpenAccount(opened, kind, "usr_ann", "s1", 2, aw); return err }(),
			"wrong slot id": func() error { _, err := OpenAccount(opened, kind, "usr_ann", "s2", 1, aw); return err }(),
		} {
			if !errors.Is(err, ErrOpen) {
				t.Errorf("%s %s: %v", kind, name, err)
			}
		}
	}
	if _, err := SecretKEK(SlotPassphrase, "u", "s", secret); err == nil {
		t.Error("SecretKEK took a passphrase slot")
	}
	if _, err := SecretKEK(SlotAppPassword, "u", "s", secret[:15]); err == nil {
		t.Error("SecretKEK took a 120-bit secret")
	}
}

// E11: a rotation reaches every slot with no slot's secret, keeps the old key reachable from the
// new one, and the transition slot's secret (the deployment key) opens neither the new account
// key nor the new calendar key version.
func TestRotationNeedsNoSlotSecretAndLeavesTheTransitionBehind(t *testing.T) {
	k0 := mustPair(t)
	transitionKEK, _ := SecretKEK(SlotTransition, "usr_ann", "tr", bytes.Repeat([]byte{1}, 32))
	appKEK, _ := SecretKEK(SlotAppPassword, "usr_ann", "ap", bytes.Repeat([]byte{2}, 32))
	tr, trWrapped, _ := NewSlot(transitionKEK, SlotTransition, "usr_ann", "tr")
	ap, apWrapped, _ := NewSlot(appKEK, SlotAppPassword, "usr_ann", "ap")
	trAW, _ := SealAccountToSlot(tr.PublicKey(), SlotTransition, "usr_ann", "tr", 1, k0)
	cal1, _ := NewCalendarKey("cal_1", 1)
	hold1, _ := cal1.WrapSeed("usr_ann", k0.PublicKey())

	// The rotation: K1 sealed to the app password slot by its public key only, K0 sealed to K1,
	// version 2 of the calendar key wrapped to K1; the transition slot gets nothing.
	k1 := mustPair(t)
	apAW, err := SealAccountToSlot(ap.PublicKey(), SlotAppPassword, "usr_ann", "ap", 2, k1)
	if err != nil {
		t.Fatal(err)
	}
	prev, _ := SealPreviousAccount(k1.PublicKey(), "usr_ann", 1, k0)
	cal2, _ := NewCalendarKey("cal_1", 2)
	hold2, _ := cal2.WrapSeed("usr_ann", k1.PublicKey())

	// The user, with only the app password: K1, then K0 and both calendar versions.
	slot, _ := OpenSlot(appKEK, SlotAppPassword, "usr_ann", "ap", apWrapped)
	got1, err := OpenAccount(slot, SlotAppPassword, "usr_ann", "ap", 2, apAW)
	if err != nil || !bytes.Equal(got1.PublicKey(), k1.PublicKey()) {
		t.Fatalf("app password opens K1: %v", err)
	}
	got0, err := OpenPreviousAccount(got1, "usr_ann", 1, prev)
	if err != nil || !bytes.Equal(got0.PublicKey(), k0.PublicKey()) {
		t.Fatalf("K1 opens K0: %v", err)
	}
	if _, err := OpenCalendarSeed(got0, "cal_1", 1, "usr_ann", hold1); err != nil {
		t.Errorf("version 1 through K0: %v", err)
	}
	if _, err := OpenCalendarSeed(got1, "cal_1", 2, "usr_ann", hold2); err != nil {
		t.Errorf("version 2 through K1: %v", err)
	}

	// The transition-era copy: the transition slot and the deployment key give K0, and K0 opens
	// version 1, but nothing of K1 or version 2.
	trSlot, _ := OpenSlot(transitionKEK, SlotTransition, "usr_ann", "tr", trWrapped)
	leaked, err := OpenAccount(trSlot, SlotTransition, "usr_ann", "tr", 1, trAW)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenCalendarSeed(leaked, "cal_1", 2, "usr_ann", hold2); !errors.Is(err, ErrOpen) {
		t.Errorf("K0 opened the rotated calendar key: %v", err)
	}
	if _, err := OpenAccount(trSlot, SlotAppPassword, "usr_ann", "ap", 2, apAW); !errors.Is(err, ErrOpen) {
		t.Errorf("the transition slot opened K1: %v", err)
	}
}

// E8: a passphrase slot is stretched with bounded Argon2id parameters.
func TestPassphraseSlots(t *testing.T) {
	p, err := NewArgon2Params()
	if err != nil || len(p.Salt) != 16 || p.Memory != 64*1024 || p.Time != 3 || p.Threads != 4 {
		t.Fatalf("defaults %+v %v", p, err)
	}
	p.Memory, p.Time = 8*1024, 1 // the lowest accepted cost, to keep the test fast
	acct := mustPair(t)
	kek, err := PassphraseKEK(p, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	slot, wrapped, _ := NewSlot(kek, SlotPassphrase, "usr_ann", "pp")
	aw, _ := SealAccountToSlot(slot.PublicKey(), SlotPassphrase, "usr_ann", "pp", 1, acct)
	opened, err := OpenSlot(kek, SlotPassphrase, "usr_ann", "pp", wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := OpenAccount(opened, SlotPassphrase, "usr_ann", "pp", 1, aw); err != nil || !bytes.Equal(got.PublicKey(), acct.PublicKey()) {
		t.Fatal(err)
	}
	wrong, _ := PassphraseKEK(p, "correct horse battery!")
	if _, err := OpenSlot(wrong, SlotPassphrase, "usr_ann", "pp", wrapped); !errors.Is(err, ErrOpen) {
		t.Errorf("wrong passphrase: %v", err)
	}
	for name, bad := range map[string]Argon2Params{
		"short salt":  {Salt: []byte("x"), Time: 1, Memory: 8 * 1024, Threads: 1},
		"no passes":   {Salt: p.Salt, Time: 0, Memory: 8 * 1024, Threads: 1},
		"tiny memory": {Salt: p.Salt, Time: 1, Memory: 1024, Threads: 1},
		"huge memory": {Salt: p.Salt, Time: 1, Memory: 1 << 30, Threads: 1},
		"no threads":  {Salt: p.Salt, Time: 1, Memory: 8 * 1024, Threads: 0},
	} {
		if _, err := PassphraseKEK(bad, "x"); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// E9: a recovery code is 128 bits in 26 symbols, read back whatever the case and separators.
func TestRecoveryCodes(t *testing.T) {
	shown, secret, err := NewRecoveryCode()
	if err != nil || len(secret) != 16 || len(strings.ReplaceAll(shown, "-", "")) != 26 {
		t.Fatalf("%q %x %v", shown, secret, err)
	}
	typed := strings.ToLower(strings.ReplaceAll(shown, "-", " "))
	if got, err := ParseRecoveryCode(typed); err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("parse %q: %x %v", typed, got, err)
	}
	for _, bad := range []string{"", "abc", shown + "a", strings.Replace(shown, shown[:1], "0", 1)} {
		if _, err := ParseRecoveryCode(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

// E4, E15: a part opens only as the part, object and calendar it was sealed for; a worker part
// opens through its wrapped worker key, which only that calendar's key opens.
func TestPartsAreBound(t *testing.T) {
	c, _ := NewCalendarKey("cal_1", 1)
	other, _ := NewCalendarKey("cal_2", 1)
	base, err := c.Seal(Part{Kind: PartBase, Seq: 1}, "uid-1", []byte("BEGIN:VCALENDAR"))
	if err != nil {
		t.Fatal(err)
	}
	noWorker := func(string) ([]byte, error) { return nil, errors.New("no worker key") }
	if got, err := c.Open(base, "uid-1", noWorker); err != nil || string(got) != "BEGIN:VCALENDAR" {
		t.Fatalf("open: %q %v", got, err)
	}
	for name, p := range map[string]Part{
		"another seq":  {Kind: PartBase, Seq: 2, Sealed: base.Sealed},
		"keep-alarms":  {Kind: PartBase, Seq: 1, KeepAlarms: true, Sealed: base.Sealed},
		"as a patch":   {Kind: PartPatch, Seq: 1, Label: "set LOCATION VEVENT", Sealed: base.Sealed},
		"flipped byte": {Kind: PartBase, Seq: 1, Sealed: append(bytes.Clone(base.Sealed[:20]), append([]byte{base.Sealed[20] ^ 1}, base.Sealed[21:]...)...)},
	} {
		if _, err := c.Open(p, "uid-1", noWorker); !errors.Is(err, ErrOpen) {
			t.Errorf("%s: %v", name, err)
		}
	}
	c2, _ := CalendarKeyFromSeed("cal_1", 2, c.seed[:])
	if _, err := c2.Open(base, "uid-1", noWorker); !errors.Is(err, ErrOpen) {
		t.Errorf("another calendar key version: %v", err)
	}
	renumbered := base
	renumbered.KeyVersion = 2
	if _, err := c.Open(renumbered, "uid-1", noWorker); !errors.Is(err, ErrOpen) {
		t.Errorf("a part claiming another key version: %v", err)
	}
	if _, err := c.Open(base, "uid-2", noWorker); !errors.Is(err, ErrOpen) {
		t.Errorf("another object: %v", err)
	}
	if _, err := other.Open(base, "uid-1", noWorker); !errors.Is(err, ErrOpen) {
		t.Errorf("another calendar: %v", err)
	}

	w, err := NewWorkerKey()
	if err != nil {
		t.Fatal(err)
	}
	wrap, err := w.WrapFor("cal_1", 1, c.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	patch, err := w.Seal(Part{Kind: PartPatch, Seq: 3, Label: "set LOCATION VEVENT"}, "cal_1", 1, "uid-1", []byte("LOCATION:Room 2"))
	if err != nil || patch.WorkerKey != w.ID {
		t.Fatal(err)
	}
	keys := func(id string) ([]byte, error) {
		if id != w.ID {
			return nil, errors.New("unknown worker key")
		}
		return wrap, nil
	}
	if got, err := c.Open(patch, "uid-1", keys); err != nil || string(got) != "LOCATION:Room 2" {
		t.Fatalf("worker patch: %q %v", got, err)
	}
	relabelled := patch
	relabelled.Label = "set SUMMARY VEVENT"
	if _, err := c.Open(relabelled, "uid-1", keys); !errors.Is(err, ErrOpen) {
		t.Errorf("relabelled patch: %v", err)
	}
	if _, err := other.OpenWorkerKey(w.ID, wrap); !errors.Is(err, ErrOpen) {
		t.Errorf("another calendar opened the worker key: %v", err)
	}
	if _, err := c.OpenWorkerKey("00000000000000000000000000000000", wrap); !errors.Is(err, ErrOpen) {
		t.Errorf("a worker key under another ID: %v", err)
	}
	for name, p := range map[string]Part{
		"base with label":      {Kind: PartBase, Seq: 1, Label: "x"},
		"patch without label":  {Kind: PartPatch, Seq: 1},
		"patch keeping alarms": {Kind: PartPatch, Seq: 1, Label: "x", KeepAlarms: true},
		"unknown kind":         {Kind: 9, Seq: 1},
	} {
		if _, err := c.Seal(p, "uid-1", nil); err == nil {
			t.Errorf("%s sealed", name)
		}
	}
}

// The part list round-trips, and anything malformed, truncated or trailing is refused.
func TestPartListEncoding(t *testing.T) {
	parts := []Part{
		{Kind: PartBase, Seq: 1, Sealed: []byte("base")},
		{Kind: PartBase, Seq: 4, KeepAlarms: true, WorkerKey: "0123456789abcdef0123456789abcdef", Sealed: []byte("update")},
		{Kind: PartPatch, Seq: 5, Label: "conference VEVENT", WorkerKey: "fedcba9876543210fedcba9876543210", Sealed: []byte("link")},
	}
	b, err := EncodeParts(parts)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeParts(b)
	if err != nil || len(got) != 3 {
		t.Fatalf("decode %v %v", got, err)
	}
	for i := range parts {
		if got[i].Kind != parts[i].Kind || got[i].Seq != parts[i].Seq || got[i].KeepAlarms != parts[i].KeepAlarms ||
			got[i].Label != parts[i].Label || got[i].WorkerKey != parts[i].WorkerKey || !bytes.Equal(got[i].Sealed, parts[i].Sealed) {
			t.Errorf("part %d: %+v want %+v", i, got[i], parts[i])
		}
	}
	if _, err := EncodeParts([]Part{{Kind: PartBase, Seq: 1}, {Kind: PartPatch, Seq: 1, Label: "x"}}); err == nil {
		t.Error("a repeated sequence number encoded")
	}
	// The same list built by hand: the second part's seq (bytes 2-9 of its header) set to the first's.
	two, err := EncodeParts([]Part{{Kind: PartBase, Seq: 1}, {Kind: PartPatch, Seq: 2, Label: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	dup := bytes.Clone(two)
	second := 6 + 15 + 1 + 4 // magic+count, first header, its key ID length byte, its sealed length
	dup[second+9] = 1
	for name, bad := range map[string][]byte{
		"empty":       nil,
		"wrong magic": append([]byte("KCP2"), b[4:]...),
		"truncated":   b[:len(b)-1],
		"trailing":    append(bytes.Clone(b), 0),
		"bad flags":   append(bytes.Clone(b[:7]), append([]byte{2}, b[8:]...)...),
		"dup seq":     dup,
	} {
		if _, err := DecodeParts(bad); err == nil {
			t.Errorf("%s decoded", name)
		}
	}
	for _, id := range []string{"abc", "0123456789ABCDEF0123456789ABCDEF", "0123456789abcdef"} {
		if _, err := EncodeParts([]Part{{Kind: PartBase, Seq: 1, WorkerKey: id}}); err == nil {
			t.Errorf("worker key ID %q encoded", id)
		}
	}
	// Sealing under a non-canonical ID is refused too, so no such part is ever made.
	w, _ := NewWorkerKey()
	w.ID = "0123456789ABCDEF0123456789ABCDEF"
	if _, err := w.Seal(Part{Kind: PartPatch, Seq: 1, Label: "x"}, "cal_1", 1, "uid", nil); err == nil {
		t.Error("a part sealed under an uppercase worker key ID")
	}
	if _, err := EncodeParts(make([]Part, maxParts+1)); err == nil {
		t.Error("too many parts encoded")
	}
}
