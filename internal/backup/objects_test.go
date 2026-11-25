package backup_test

import (
	"context"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Busnes-app/ky-primitives/capsule"
	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kycalendar/internal/backup"
	"github.com/Busnes-app/kycalendar/internal/config"
	"github.com/Busnes-app/kycalendar/internal/store"
)

// fakeS3 is a versioned bucket behind TLS: PUT, ListObjectsV2, ListObjectVersions and exact
// version DELETE, which is what a backup run uses. failPut makes every PUT answer 500.
type fakeS3 struct {
	mu       sync.Mutex
	versions map[string][]fakeVersion // oldest first
	next     int
	failPut  bool
}

type fakeVersion struct {
	id   string
	data []byte
}

func newFakeS3(t *testing.T) (*fakeS3, config.ObjectStorageConfig) {
	t.Helper()
	f := &fakeS3{versions: map[string][]fakeVersion{}}
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	t.Cleanup(backup.SetObjectHTTPForTest(srv.Client()))
	return f, config.ObjectStorageConfig{Endpoint: srv.URL, Bucket: "cal", AccessKey: "ak", SecretKey: "sk"}
}

func etag(b []byte) string { return fmt.Sprintf("%q", fmt.Sprintf("%x", sha256.Sum256(b))) }

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := strings.TrimPrefix(r.URL.Path, "/cal/")
	q := r.URL.Query()
	switch {
	case r.Method == http.MethodPut:
		if f.failPut {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		b, _ := io.ReadAll(r.Body)
		f.next++
		v := fakeVersion{id: strconv.Itoa(f.next), data: b}
		f.versions[key] = append(f.versions[key], v)
		w.Header().Set("ETag", etag(b))
		w.Header().Set("X-Amz-Version-Id", v.id)
	case r.Method == http.MethodDelete:
		vs := f.versions[key]
		vs = slices.DeleteFunc(vs, func(v fakeVersion) bool { return v.id == q.Get("versionId") })
		if f.versions[key] = vs; len(vs) == 0 {
			delete(f.versions, key)
		}
		w.Header().Set("X-Amz-Version-Id", q.Get("versionId"))
		w.WriteHeader(http.StatusNoContent)
	case q.Has("versions"):
		type version struct {
			XMLName   xml.Name `xml:"Version"`
			Key       string
			VersionId string
			Size      int
			IsLatest  bool
		}
		var out struct {
			XMLName     xml.Name `xml:"ListVersionsResult"`
			IsTruncated bool
			Versions    []version
		}
		for _, k := range f.keys(q.Get("prefix")) {
			for i, v := range f.versions[k] {
				out.Versions = append(out.Versions, version{Key: k, VersionId: v.id, Size: len(v.data), IsLatest: i == len(f.versions[k])-1})
			}
		}
		_ = xml.NewEncoder(w).Encode(out)
	case q.Get("list-type") == "2":
		type object struct {
			Key  string
			Size int
			ETag string
		}
		var out struct {
			XMLName     xml.Name `xml:"ListBucketResult"`
			IsTruncated bool
			Contents    []object
		}
		for _, k := range f.keys(q.Get("prefix")) {
			v := f.versions[k][len(f.versions[k])-1]
			out.Contents = append(out.Contents, object{Key: k, Size: len(v.data), ETag: etag(v.data)})
		}
		_ = xml.NewEncoder(w).Encode(out)
	default:
		w.WriteHeader(http.StatusNotImplemented)
	}
}

func (f *fakeS3) keys(prefix string) []string {
	var out []string
	for k := range f.versions {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// copies lists the latest bytes of every key under prefix, and how many versions each holds.
func (f *fakeS3) copies(prefix string) (map[string][]byte, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out, n := map[string][]byte{}, 0
	for _, k := range f.keys(prefix) {
		out[k] = f.versions[k][len(f.versions[k])-1].data
		n += len(f.versions[k])
	}
	return out, n
}

// tree is every path under dir, with each file's bytes digest, for before/after comparisons.
func tree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		out = append(out, strings.TrimPrefix(p, dir))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// testCell is the storage cell multiInstance records.
const testCell = "us-east-2"

// multiInstance is a recorded multi-tenant install with a pinned recovery key, no KyRecovery
// pairing and objects as its object storage.
func multiInstance(t *testing.T, objects config.ObjectStorageConfig) (*config.Config, store.Store, recoveryclient.RunConfig) {
	t.Helper()
	// Multi-tenant installs are PostgreSQL; on SQLite the database lives in the data directory.
	cfg, st := sqliteInstance(t)
	if os.Getenv("KY_TEST_POSTGRES_DSN") != "" {
		cfgs, sts := replicas(t, 1)
		cfg, st = cfgs[0], sts[0]
	}
	ctx := context.Background()
	for k, v := range map[string]string{store.TenancySetting: "multi", store.StorageCellSetting: testCell} {
		if err := st.Settings().SetSetting(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := backup.PinRecoveryKey(ctx, cfg, st, newKey(t, 2, 3)); err != nil {
		t.Fatal(err)
	}
	cfg.ObjectStorage = objects
	cfg.Backup.Keep = 2
	rc, err := backup.RunConfig(cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	return cfg, st, rc
}

// Tenancy spec D15: a multi-tenant run puts its copy in the bucket, keeps the newest Keep
// there with every version of the older ones erased, and leaves KY_DATA_DIR as it found it.
func TestMultiTenantRunCopiesToObjectStorage(t *testing.T) {
	f, objects := newFakeS3(t)
	cfg, st, rc := multiInstance(t, objects)
	ctx := context.Background()
	// An operator's own object beside the copies is never pruned.
	f.versions[backup.ObjectPrefix(testCell)+"README"] = []fakeVersion{{id: "op", data: []byte("hands off")}}
	// Nor is another cell's copy in the same bucket, however old (tenancy spec, Cells).
	other := backup.ObjectPrefix("us-east-1") + "00000000000000000001-kycalendar.cap-kycalendar-1.kycap"
	f.versions[other] = []fakeVersion{{id: "cell", data: []byte("other cell")}}
	before := tree(t, cfg.Database.DataDir)
	var last recoveryclient.Result
	for range 4 {
		res, err := backup.Run(ctx, cfg, rc, st, &storingDepositor{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(res.LocalPath, backup.ObjectPrefix(testCell)) || res.LocalError != "" {
			t.Fatalf("result names %q (error %q), not an object key", res.LocalPath, res.LocalError)
		}
		last = res
	}
	if after := tree(t, cfg.Database.DataDir); !slices.Equal(before, after) {
		t.Fatalf("KY_DATA_DIR changed across the runs:\nbefore %v\nafter  %v", before, after)
	}
	got, versions := f.copies(backup.ObjectPrefix(testCell))
	if len(got) != 3 || versions != 3 || got[backup.ObjectPrefix(testCell)+"README"] == nil {
		t.Fatalf("bucket holds %d keys in %d versions, want the operator's README and 2 copies, one version each: %v", len(got), versions, slices.Sorted(maps.Keys(got)))
	}
	if all, _ := f.copies(""); all[other] == nil {
		t.Fatal("the run pruned another cell's copy")
	}
	m, err := capsule.ReadUnverifiedManifest(got[last.LocalPath])
	if err != nil || m.CapsuleID != last.Manifest.CapsuleID {
		t.Fatalf("the newest object is not the last run's capsule: %v %q", err, m.CapsuleID)
	}
}

// Without a bucket a multi-tenant run refuses before it seals anything; with one that fails,
// the run fails (nothing was deposited) and still leaves nothing on disk.
func TestMultiTenantRunNeedsWorkingObjectStorage(t *testing.T) {
	cfg, st, rc := multiInstance(t, config.ObjectStorageConfig{})
	ctx := context.Background()
	before := tree(t, cfg.Database.DataDir)
	if _, err := backup.Run(ctx, cfg, rc, st, &storingDepositor{}); err == nil || !strings.Contains(err.Error(), "KYCALENDAR_S3") {
		t.Fatalf("a run with no object storage: %v", err)
	}
	f, objects := newFakeS3(t)
	cfg.ObjectStorage = objects
	if err := st.Settings().DeleteSetting(ctx, store.StorageCellSetting); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Run(ctx, cfg, rc, st, &storingDepositor{}); err == nil || !strings.Contains(err.Error(), "storage cell") {
		t.Fatalf("a run with no storage cell: %v", err)
	}
	if err := st.Settings().SetSetting(ctx, store.StorageCellSetting, testCell); err != nil {
		t.Fatal(err)
	}
	f.failPut = true
	res, err := backup.Run(ctx, cfg, rc, st, &storingDepositor{})
	if err == nil || res.LocalPath != "" || !strings.Contains(res.LocalError, "object copy") {
		t.Fatalf("a failed upload: err=%v path=%q local_error=%q", err, res.LocalPath, res.LocalError)
	}
	if after := tree(t, cfg.Database.DataDir); !slices.Equal(before, after) {
		t.Fatalf("KY_DATA_DIR changed:\nbefore %v\nafter  %v", before, after)
	}
	if got, _ := f.copies(""); len(got) != 0 {
		t.Fatalf("the bucket holds %d objects", len(got))
	}
}
