package suite

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

const notes = "https://notes.e2e.test"

// notesHdr is what KyNotes' current web client sends on every content request.
var notesHdr = map[string]string{"X-Kynotes-Key-Scheme": "shared-v2"}

// notesData is what one member made at KyNotes through its API: a keyed notebook with a saved
// page and an attachment (in S3), a sealed link, a page link and a paired device.
type notesData struct {
	user, cid, oid         string
	sealed, page           string // share link tokens
	deviceID, deviceSecret string
	generation             int64
}

// notesID is a KyNotes identifier: prefix, "_", 26 Crockford base32 characters.
func notesID(prefix string) string {
	const alphabet = "0123456789abcdefghjkmnpqrstvwxyz"
	b := make([]byte, 26)
	rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return prefix + "_" + string(b)
}

// notesWrite makes p's notesData at KyNotes. KyNotes encrypts in the client: the driver has no
// client, so it stores the notebook's key envelope the way KyNotes' own tests do
// (keyForTest), a documented shortcut. Every other write goes through the HTTP API.
func (s *suite) notesWrite(t *testing.T, b *browser, org, subject string) notesData {
	t.Helper()
	var d notesData
	d.user = s.sql("kynotes", fmt.Sprintf("SELECT id FROM users WHERE org_id = '%s' AND sso_subject = '%s'", org, subject))
	if d.user == "" || strings.Contains(d.user, "\n") {
		t.Fatalf("kynotes users of org %s with subject %s: %q", org, subject, d.user)
	}
	var c struct{ ID string }
	json.Unmarshal(b.must(200, "POST", notes+"/api/v1/containers", map[string]string{"kind": "workbook", "metaCiphertext": ""}, notesHdr), &c)
	d.cid = c.ID
	dev := notesID("dev")
	gen := s.sql("kynotes", fmt.Sprintf("WITH k AS (UPDATE containers SET shared_generation = key_generation WHERE id = '%s' RETURNING key_generation) SELECT key_generation FROM k", d.cid))
	g, err := strconv.ParseInt(gen, 10, 64)
	if err != nil {
		t.Fatalf("kynotes notebook %s key generation: %q", d.cid, gen)
	}
	s.sql("kynotes", fmt.Sprintf(`INSERT INTO devices(id,user_id,public_key,fingerprint,secret_hash,platform,created_at) VALUES('%s','%s','%s','e2e-%s','identity:e2e','identity','now');
INSERT INTO key_envelopes(id,container_id,device_id,key_generation,alg,envelope,created_at)
  VALUES('%s','%s','%s',%d,'hpke-base-xwing-647a-hkdfsha256-aes256gcm-hybridsig',decode(repeat('01',93),'hex'),'now')`,
		dev, d.user, base64.StdEncoding.EncodeToString(randomBytes(32)), dev, notesID("env"), d.cid, dev, g))
	d.generation = g
	var o struct{ ID string }
	json.Unmarshal(b.must(200, "POST", notes+"/api/v1/containers/"+d.cid+"/objects", map[string]string{"kind": "note"}, notesHdr), &o)
	d.oid = o.ID
	base := s.sql("kynotes", fmt.Sprintf("SELECT current_version FROM objects WHERE id = '%s'", d.oid))
	b.must(200, "PUT", notes+"/api/v1/objects/"+d.oid, []byte("ciphertext-"+random(8)), map[string]string{
		"X-Kynotes-Key-Scheme": "shared-v2", "X-Kynotes-Key-Generation": strconv.FormatInt(g, 10), "X-Kynotes-Base-Version": base})
	data := randomBytes(64)
	sum := sha256.Sum256(data)
	var up struct{ UploadID string }
	json.Unmarshal(b.must(200, "POST", notes+"/api/v1/containers/"+d.cid+"/uploads", map[string]any{
		"declaredBytes": len(data), "expectedDigest": hex.EncodeToString(sum[:]), "kind": "attachment"}, notesHdr), &up)
	b.must(200, "PATCH", notes+"/api/v1/uploads/"+up.UploadID, data, map[string]string{"X-Kynotes-Key-Scheme": "shared-v2", "X-Kynotes-Chunk-Index": "0"})
	b.must(200, "POST", notes+"/api/v1/uploads/"+up.UploadID+"/finalize", map[string]any{"metadataCiphertext": "", "keyGeneration": g}, notesHdr)
	expires := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	var link struct{ Token string }
	json.Unmarshal(b.must(200, "POST", notes+"/api/v1/share-links", map[string]string{
		"ciphertext": base64.RawURLEncoding.EncodeToString(randomBytes(32)), "expiresAt": expires}, notesHdr), &link)
	d.sealed = link.Token
	link.Token = ""
	json.Unmarshal(b.must(200, "POST", notes+"/api/v1/objects/"+d.oid+"/share-links", map[string]any{"version": 1, "expiresAt": expires}, notesHdr), &link)
	d.page = link.Token
	var pair struct{ Token string }
	json.Unmarshal(b.must(200, "POST", notes+"/api/v1/devices/pairing-token", nil, notesHdr), &pair)
	var reg struct {
		ID     string `json:"deviceId"`
		Secret string `json:"deviceSecret"`
	}
	json.Unmarshal(b.must(200, "POST", notes+"/api/v1/devices/register", map[string]string{"pairingToken": pair.Token,
		"publicKey": base64.StdEncoding.EncodeToString(randomBytes(32)), "platform": "unknown", "labelCiphertext": ""}, nil), &reg)
	d.deviceID, d.deviceSecret = reg.ID, reg.Secret
	if d.sealed == "" || d.page == "" || d.deviceID == "" || d.deviceSecret == "" {
		t.Fatalf("kynotes credentials for org %s incomplete: links %t/%t device %t", org, d.sealed != "", d.page != "", d.deviceID != "")
	}
	return d
}

// notesAnswers is what each of d's credentials gets now: session, device, sealed link, page link.
func (s *suite) notesAnswers(session *browser, d notesData) [4]int {
	var out [4]int
	out[0], _ = session.do("GET", notes+"/api/v1/containers", nil, notesHdr)
	out[1] = s.notesDevice(d, "/api/v1/sync/pending")
	anon := s.browser()
	out[2], _ = anon.do("GET", notes+"/api/v1/share-links/"+d.sealed, nil, nil)
	out[3], _ = anon.do("GET", notes+"/api/v1/share-links/"+d.page, nil, nil)
	return out
}

// notesDevice sends one GET with only d's device credential, as a paired app does.
func (s *suite) notesDevice(d notesData, path string) int {
	req, _ := http.NewRequest("GET", notes+path, nil)
	req.Header.Set("X-Kynotes-Device-Id", d.deviceID)
	req.Header.Set("X-Kynotes-Device-Secret", d.deviceSecret)
	req.Header.Set("X-Kynotes-Key-Scheme", "shared-v2")
	res, err := (&http.Client{Transport: s.web, Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return 0
	}
	res.Body.Close()
	return res.StatusCode
}

// notesOrgRows counts, per table, the rows of every org_id column in KyNotes' schema that name org.
func (s *suite) notesOrgRows(org string) map[string]string {
	s.t.Helper()
	left := map[string]string{}
	tables := s.sql("kynotes", "SELECT table_name FROM information_schema.columns WHERE column_name = 'org_id' AND table_schema = current_schema() ORDER BY 1")
	for _, table := range strings.Fields(tables) {
		if n := s.sql("kynotes", fmt.Sprintf(`SELECT count(*) FROM "%s" WHERE org_id = '%s'`, table, org)); n != "0" {
			left[table] = n
		}
	}
	return left
}

// notesReported is the usage KyNotes last reported to KyIdentity for org ("" if none).
func (s *suite) notesReported(org string) string {
	return s.sql("kyidentity", fmt.Sprintf(`SELECT s.used_bytes FROM org_storage s JOIN app_registry r ON r.id = s.app_id
WHERE r.client_id = 'kynotes' AND s.org_id = '%s'`, org))
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}
