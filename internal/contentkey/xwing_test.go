package contentkey

import (
	"bytes"
	"errors"
	"testing"

	"github.com/Busnes-app/ky-primitives/xwing"
)

// The shared envelope has one extra byte. Both directions must preserve the exact
// v1 info and AAD, including for empty plaintext. Stored vectors pin old ciphertexts.
func TestXWingV1Framing(t *testing.T) {
	pair := mustPair(t)
	priv, err := xwing.FromSeed(pair.Seed())
	if err != nil {
		t.Fatal(err)
	}
	pub, err := parsePublicKey(pair.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	aad := bind("cal_1", "2", "usr_ann")
	for _, info := range []string{
		"kycalendar content v1 calendar seed",
		"kycalendar content v1 account key",
		"kycalendar content v1 previous account key",
		"kycalendar content v1 worker key",
	} {
		t.Run(info, func(t *testing.T) {
			for _, msg := range [][]byte{nil, bytes.Repeat([]byte{7}, SeedBytes)} {
				sealed, err := sealTo(pub, info, aad, msg)
				if err != nil {
					t.Fatal(err)
				}
				if len(sealed) != xwing.Overhead-1+len(msg) {
					t.Fatalf("v1 length = %d", len(sealed))
				}
				framed := append([]byte{xwing.Version}, sealed...)
				if got, err := xwing.Open(priv, []byte(info), aad, framed); err != nil || !bytes.Equal(got, msg) {
					t.Fatalf("shared reader: %x, %v", got, err)
				}
				shared, err := xwing.Seal(priv.Public(), []byte(info), aad, msg)
				if err != nil {
					t.Fatal(err)
				}
				original := bytes.Clone(shared)
				if got, err := openWith(pair.priv, info, aad, shared[1:]); err != nil || !bytes.Equal(got, msg) {
					t.Fatalf("v1 reader: %x, %v", got, err)
				}
				if !bytes.Equal(shared, original) {
					t.Fatal("opening modified the input")
				}
				for name, bad := range map[string][]byte{
					"empty": nil, "short enc": sealed[:10], "short tag": sealed[:xwing.Overhead-2],
					"prefixed": framed, "trailing": append(bytes.Clone(sealed), 0),
				} {
					if got, err := openWith(pair.priv, info, aad, bad); got != nil || err != ErrOpen {
						t.Errorf("%s: %x, %v; want ErrOpen", name, got, err)
					}
				}
				for _, wrongInfo := range []string{"", "kycalendar content v2 calendar seed"} {
					if _, err := openWith(pair.priv, wrongInfo, aad, sealed); err != ErrOpen {
						t.Errorf("wrong info %q: %v", wrongInfo, err)
					}
				}
			}
		})
	}
}

// V1 has no org field: callers must resolve globally unique user/calendar IDs in
// the authorized org. Reusing key material here proves the bindings, not just keys,
// reject moving rows to another org's IDs. This is not a store authorization test.
func TestBindingsRejectOtherOrgIDsWithSameKeys(t *testing.T) {
	account := mustPair(t)
	const calA, calB = "cal_org_a", "cal_org_b"
	const userA, userB = "usr_org_a", "usr_org_b"
	a, err := NewCalendarKey(calA, 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := CalendarKeyFromSeed(calB, 1, a.seed[:])
	if err != nil {
		t.Fatal(err)
	}
	holder, err := a.WrapSeed(userA, account.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	worker, err := NewWorkerKey()
	if err != nil {
		t.Fatal(err)
	}
	workerWrap, err := worker.WrapFor(calA, 1, a.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	base, err := a.Seal(Part{Kind: PartBase, Seq: 1}, "same-uid", []byte("content"))
	if err != nil {
		t.Fatal(err)
	}
	patch, err := worker.Seal(Part{Kind: PartPatch, Seq: 2, Label: "conference"}, calA, 1, "same-uid", []byte("patch"))
	if err != nil {
		t.Fatal(err)
	}
	kek, err := SecretKEK(SlotSession, userA, "same-slot", bytes.Repeat([]byte{1}, SeedBytes))
	if err != nil {
		t.Fatal(err)
	}
	slot, slotWrap, err := NewSlot(kek, SlotSession, userA, "same-slot")
	if err != nil {
		t.Fatal(err)
	}
	accountWrap, err := SealAccountToSlot(slot.PublicKey(), SlotSession, userA, "same-slot", 1, account)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := SealPreviousAccount(account.PublicKey(), userA, 1, account)
	if err != nil {
		t.Fatal(err)
	}
	for name, open := range map[string]func() error{
		"holder calendar": func() error { _, e := OpenCalendarSeed(account, calB, 1, userA, holder); return e },
		"holder user":     func() error { _, e := OpenCalendarSeed(account, calA, 1, userB, holder); return e },
		"worker wrap":     func() error { _, e := b.OpenWorkerKey(worker.ID, workerWrap); return e },
		"base":            func() error { _, e := b.Open(base, "same-uid", nil); return e },
		"worker part": func() error {
			// Rewrap to B so the part's own calendar AAD is the only failing binding.
			wrap, e := worker.WrapFor(calB, 1, b.PublicKey())
			if e != nil {
				return e
			}
			_, e = b.Open(patch, "same-uid", func(string) ([]byte, error) { return wrap, nil })
			return e
		},
		"slot seed":    func() error { _, e := OpenSlot(kek, SlotSession, userB, "same-slot", slotWrap); return e },
		"account wrap": func() error { _, e := OpenAccount(slot, SlotSession, userB, "same-slot", 1, accountWrap); return e },
		"previous key": func() error { _, e := OpenPreviousAccount(account, userB, 1, previous); return e },
	} {
		t.Run(name, func(t *testing.T) {
			if err := open(); !errors.Is(err, ErrOpen) {
				t.Fatalf("moved row opened: %v", err)
			}
		})
	}
}
