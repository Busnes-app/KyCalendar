package backup_test

import (
	"context"
	"net"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Busnes-app/ky-primitives/s3"
	"github.com/Busnes-app/kycalendar/internal/backup"
	"github.com/Busnes-app/kycalendar/internal/config"
)

// The real-artifact check of D15 copies: the KY_TEST_S3_* fixture is a disposable loopback,
// signature-checking, versioned S3 server (CI: seaweedfs) behind a verified TLS proxy. Pruning
// must leave no version or delete marker of an old copy.
func TestMultiTenantRunAgainstVersionedS3(t *testing.T) {
	endpoint := os.Getenv("KY_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("set KY_TEST_S3_* for the disposable versioned S3 fixture")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || !net.ParseIP(u.Hostname()).IsLoopback() {
		t.Fatal("the fixture must be loopback HTTP, never a live store")
	}
	proxy := httptest.NewTLSServer(httputil.NewSingleHostReverseProxy(u))
	t.Cleanup(proxy.Close)
	t.Cleanup(backup.SetObjectHTTPForTest(proxy.Client()))
	objects := config.ObjectStorageConfig{Endpoint: proxy.URL, Bucket: os.Getenv("KY_TEST_S3_BUCKET"), AccessKey: os.Getenv("KY_TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("KY_TEST_S3_SECRET_KEY")}
	c := objects.S3()
	c.HTTP = proxy.Client()
	client, err := s3.New(c)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t.Cleanup(func() { _ = client.DeletePrefix(context.Background(), backup.ObjectPrefix) })
	cfg, st, rc := multiInstance(t, objects)
	before := tree(t, cfg.Database.DataDir)
	var keys []string
	for range 3 {
		res, err := backup.Run(ctx, cfg, rc, st, &storingDepositor{})
		if err != nil || res.LocalError != "" {
			t.Fatalf("run: %v %q", err, res.LocalError)
		}
		keys = append(keys, res.LocalPath)
	}
	page, err := client.ListObjectVersions(ctx, backup.ObjectPrefix, s3.VersionCursor{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, o := range page.Objects {
		if o.DeleteMarker || !strings.HasSuffix(o.Key, ".kycap") {
			t.Fatalf("bucket keeps %q (marker %v)", o.Key, o.DeleteMarker)
		}
		got = append(got, o.Key)
	}
	slices.Sort(got)
	if !slices.Equal(got, keys[1:]) {
		t.Fatalf("versions %v, want the two newest %v", got, keys[1:])
	}
	if after := tree(t, cfg.Database.DataDir); !slices.Equal(before, after) {
		t.Fatalf("KY_DATA_DIR changed:\nbefore %v\nafter  %v", before, after)
	}
}
