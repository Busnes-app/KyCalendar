package backup

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/ky-primitives/s3"
	"github.com/Busnes-app/kycalendar/internal/config"
)

// ObjectPrefix is where a multi-tenant install's backup copies live in its bucket (tenancy
// spec D15). Keys are <prefix><20-digit Unix nanoseconds>-<local copy name>, so key order is
// write order.
const ObjectPrefix = "platform/backups/"

// objectHTTP is the S3 client's transport; nil is the library's default. Tests set it.
var objectHTTP *http.Client

// errNoObjectStorage is a multi-tenant backup run with no bucket to put its copy in.
var errNoObjectStorage = errors.New("backup: multi-tenant mode keeps backup copies in object storage; set KYCALENDAR_S3_*")

// objectCopy moves the local copy a run wrote into scratch to the bucket, then keeps the
// newest keep copies there. It returns the object key. The copy just written is never pruned.
func objectCopy(ctx context.Context, objects config.ObjectStorageConfig, local string, keep int, now time.Time) (string, error) {
	c := objects.S3()
	c.HTTP = objectHTTP
	client, err := s3.New(c)
	if err != nil {
		return "", err
	}
	f, err := os.Open(local)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	key := fmt.Sprintf("%s%020d-%s", ObjectPrefix, now.UnixNano(), filepath.Base(local))
	if _, err := client.Put(ctx, key, f, info.Size()); err != nil {
		return "", err
	}
	return key, pruneObjects(ctx, client, key, keep)
}

// pruneObjects erases every version of this service's oldest copies beyond keep, so a deleted
// org's data ages out of the bucket as it does from a local directory.
func pruneObjects(ctx context.Context, client *s3.Client, mine string, keep int) error {
	var keys []string
	for next := ""; ; {
		page, err := client.ListObjects(ctx, ObjectPrefix, next)
		if err != nil {
			return fmt.Errorf("prune: %w", err)
		}
		for _, o := range page.Objects {
			if isCopy(o.Key) {
				keys = append(keys, o.Key)
			}
		}
		if next = page.Next; next == "" {
			break
		}
	}
	slices.Sort(keys)
	for _, k := range keys[:max(len(keys)-keep, 0)] {
		if k == mine {
			continue
		}
		if err := client.DeleteAllVersions(ctx, k); err != nil {
			return fmt.Errorf("prune: %w", err)
		}
	}
	return nil
}

// isCopy matches keys objectCopy writes for this service, and nothing an operator put beside them.
func isCopy(key string) bool {
	name, ok := strings.CutPrefix(key, ObjectPrefix)
	stamp, rest, found := strings.Cut(name, "-")
	if !ok || !found || len(stamp) != 20 || strings.Trim(stamp, "0123456789") != "" {
		return false
	}
	return strings.HasPrefix(rest, recoveryclient.LocalPrefix(ServiceName)) && strings.HasSuffix(rest, ".kycap")
}
