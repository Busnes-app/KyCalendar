package contentkey

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/vectors.json")

// vectors are docs/CONTENT-ENCRYPTION-FORMAT.md's test vectors: fixed keys and the sealed bytes
// they open. HPKE and AES-GCM are randomized, so -update writes new sealed bytes; every later run
// must open the stored ones, which is what keeps the format stable for other clients (option 1).
type vectors struct {
	AccountSeed  string `json:"account_seed"`
	AccountPub   string `json:"account_public_key"`
	CalendarID   string `json:"calendar_id"`
	CalendarSeed string `json:"calendar_seed"`
	CalendarPub  string `json:"calendar_public_key"`
	HolderUserID string `json:"holder_user_id"`
	CalendarVer  uint32 `json:"calendar_key_version"`
	AccountVer   uint32 `json:"account_key_version"`
	SeedWrap     string `json:"calendar_seed_wrap"`
	Slot         struct {
		Kind        SlotKind `json:"kind"`
		UserID      string   `json:"user_id"`
		SlotID      string   `json:"slot_id"`
		Secret      string   `json:"secret"`
		Wrapped     string   `json:"wrapped"`
		AccountWrap string   `json:"account_wrap"`
	} `json:"slot"`
	Passphrase struct {
		Params      Argon2Params `json:"params"`
		Passphrase  string       `json:"passphrase"`
		Wrapped     string       `json:"wrapped"`
		AccountWrap string       `json:"account_wrap"`
	} `json:"passphrase_slot"`
	PreviousSeed string `json:"previous_account_seed"`
	PreviousWrap string `json:"previous_account_wrap"` // version 1 sealed to the current account key
	UID          string `json:"uid"`
	Parts        string `json:"parts"` // EncodeParts: a calendar-key base and a worker patch
	WorkerKeyID  string `json:"worker_key_id"`
	WorkerWrap   string `json:"worker_key_wrap"`
	Base         string `json:"base_plaintext"`
	Patch        string `json:"patch_plaintext"`
}

const vectorsPath = "testdata/vectors.json"

func h(b []byte) string { return hex.EncodeToString(b) }

func unh(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeVectors(t *testing.T) {
	t.Helper()
	var v vectors
	acct, _ := KeyPairFromSeed(bytes.Repeat([]byte{0x11}, 32))
	v.CalendarVer, v.AccountVer = 2, 2
	cal, _ := CalendarKeyFromSeed("cal_vector", v.CalendarVer, bytes.Repeat([]byte{0x22}, 32))
	v.AccountSeed, v.AccountPub = h(acct.Seed()), h(acct.PublicKey())
	v.CalendarID, v.CalendarSeed, v.CalendarPub = "cal_vector", h(cal.seed[:]), h(cal.PublicKey())
	v.HolderUserID = "usr_vector"
	wrap, err := cal.WrapSeed(v.HolderUserID, acct.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	v.SeedWrap = h(wrap)

	v.Slot.Kind, v.Slot.UserID, v.Slot.SlotID = SlotAppPassword, "usr_vector", "slot_vector"
	secret := bytes.Repeat([]byte{0x33}, 32)
	v.Slot.Secret = h(secret)
	kek, _ := SecretKEK(v.Slot.Kind, v.Slot.UserID, v.Slot.SlotID, secret)
	slot, slotWrapped, _ := NewSlot(kek, v.Slot.Kind, v.Slot.UserID, v.Slot.SlotID)
	aw, _ := SealAccountToSlot(slot.PublicKey(), v.Slot.Kind, v.Slot.UserID, v.Slot.SlotID, v.AccountVer, acct)
	v.Slot.Wrapped, v.Slot.AccountWrap = h(slotWrapped), h(aw)

	v.Passphrase.Params = Argon2Params{Salt: bytes.Repeat([]byte{0x44}, 16), Time: 1, Memory: 8 * 1024, Threads: 1}
	v.Passphrase.Passphrase = "correct horse battery"
	pkek, _ := PassphraseKEK(v.Passphrase.Params, v.Passphrase.Passphrase)
	pslot, pWrapped, _ := NewSlot(pkek, SlotPassphrase, "usr_vector", "pp_vector")
	paw, _ := SealAccountToSlot(pslot.PublicKey(), SlotPassphrase, "usr_vector", "pp_vector", v.AccountVer, acct)
	v.Passphrase.Wrapped, v.Passphrase.AccountWrap = h(pWrapped), h(paw)
	previous, _ := KeyPairFromSeed(bytes.Repeat([]byte{0x55}, 32))
	pw, _ := SealPreviousAccount(acct.PublicKey(), "usr_vector", 1, previous)
	v.PreviousSeed, v.PreviousWrap = h(previous.Seed()), h(pw)

	v.UID, v.Base, v.Patch = "uid-vector", "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n", "LOCATION:Room 2"
	base, _ := cal.Seal(Part{Kind: PartBase, Seq: 1}, v.UID, []byte(v.Base))
	w, _ := NewWorkerKey()
	ww, _ := w.WrapFor(v.CalendarID, v.CalendarVer, cal.PublicKey())
	v.WorkerKeyID, v.WorkerWrap = w.ID, h(ww)
	patch, _ := w.Seal(Part{Kind: PartPatch, Seq: 2, Label: "set LOCATION VEVENT"}, v.CalendarID, v.CalendarVer, v.UID, []byte(v.Patch))
	parts, err := EncodeParts([]Part{base, patch})
	if err != nil {
		t.Fatal(err)
	}
	v.Parts = h(parts)
	b, _ := json.MarshalIndent(v, "", "  ")
	if err := os.WriteFile(vectorsPath, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The stored vectors still open with the code as it is: the format has not moved.
func TestVectorsStillOpen(t *testing.T) {
	if *update {
		writeVectors(t)
	}
	raw, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	acct, err := KeyPairFromSeed(unh(t, v.AccountSeed))
	if err != nil || h(acct.PublicKey()) != v.AccountPub {
		t.Fatalf("account public key: %v", err)
	}
	cal, err := OpenCalendarSeed(acct, v.CalendarID, v.CalendarVer, v.HolderUserID, unh(t, v.SeedWrap))
	if err != nil || h(cal.seed[:]) != v.CalendarSeed || h(cal.PublicKey()) != v.CalendarPub {
		t.Fatalf("calendar seed wrap: %v", err)
	}
	kek, _ := SecretKEK(v.Slot.Kind, v.Slot.UserID, v.Slot.SlotID, unh(t, v.Slot.Secret))
	slot, err := OpenSlot(kek, v.Slot.Kind, v.Slot.UserID, v.Slot.SlotID, unh(t, v.Slot.Wrapped))
	if err != nil {
		t.Fatalf("slot: %v", err)
	}
	if got, err := OpenAccount(slot, v.Slot.Kind, v.Slot.UserID, v.Slot.SlotID, v.AccountVer, unh(t, v.Slot.AccountWrap)); err != nil || h(got.Seed()) != v.AccountSeed {
		t.Fatalf("slot's account key: %v", err)
	}
	pkek, _ := PassphraseKEK(v.Passphrase.Params, v.Passphrase.Passphrase)
	pslot, err := OpenSlot(pkek, SlotPassphrase, "usr_vector", "pp_vector", unh(t, v.Passphrase.Wrapped))
	if err != nil {
		t.Fatalf("passphrase slot: %v", err)
	}
	if got, err := OpenAccount(pslot, SlotPassphrase, "usr_vector", "pp_vector", v.AccountVer, unh(t, v.Passphrase.AccountWrap)); err != nil || h(got.Seed()) != v.AccountSeed {
		t.Fatalf("passphrase slot's account key: %v", err)
	}
	if got, err := OpenPreviousAccount(acct, "usr_vector", 1, unh(t, v.PreviousWrap)); err != nil || h(got.Seed()) != v.PreviousSeed {
		t.Fatalf("previous account key: %v", err)
	}
	parts, err := DecodeParts(unh(t, v.Parts))
	if err != nil || len(parts) != 2 {
		t.Fatalf("parts: %v", err)
	}
	keys := func(id string) ([]byte, error) {
		if id != v.WorkerKeyID {
			return nil, errors.New("unknown worker key")
		}
		return unh(t, v.WorkerWrap), nil
	}
	for i, want := range []string{v.Base, v.Patch} {
		if got, err := cal.Open(parts[i], v.UID, keys); err != nil || string(got) != want {
			t.Errorf("part %d: %q %v", i, got, err)
		}
	}
}
