package backup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// scratchPrefix names the plaintext snapshot directories under the data directory.
const scratchPrefix = "snapshot-"

// NewScratch makes a 0700 snapshot directory under dataDir and holds a flock on it until
// release, which also removes it. A process killed in between leaves it unlocked, for
// RemoveStaleSnapshots at the next start.
func NewScratch(dataDir string) (dir string, release func(), err error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", nil, err
	}
	dir, err = os.MkdirTemp(dataDir, scratchPrefix+"*") // MkdirTemp creates 0700
	if err != nil {
		return "", nil, err
	}
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err == nil {
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
	}
	if err != nil {
		if fd > 0 {
			_ = unix.Close(fd)
		}
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	return dir, func() {
		_ = os.RemoveAll(dir)
		_ = unix.Close(fd)
	}, nil
}

// RemoveStaleSnapshots removes the snapshot directories under dataDir that a killed process
// left: only directories (never a symlink), owned by this user, that nobody holds locked,
// still the same inode once locked. A running CLI deposit keeps its own. It returns how many
// it removed.
func RemoveStaleSnapshots(dataDir string) (int, error) {
	entries, err := os.ReadDir(dataDir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), scratchPrefix) {
			continue
		}
		removed, err := removeIfStale(filepath.Join(dataDir, e.Name()))
		if err != nil {
			errs = append(errs, err)
		} else if removed {
			n++
		}
	}
	return n, errors.Join(errs...)
}

func removeIfStale(path string) (bool, error) {
	var before unix.Stat_t
	if err := unix.Lstat(path, &before); err != nil {
		return false, nil
	}
	if before.Mode&unix.S_IFMT != unix.S_IFDIR || before.Uid != uint32(os.Getuid()) {
		return false, nil
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return false, nil // replaced by a symlink or gone since
	}
	defer unix.Close(fd)
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return false, nil // in use
		}
		return false, err
	}
	var held, now unix.Stat_t
	if unix.Fstat(fd, &held) != nil || unix.Lstat(path, &now) != nil || held.Ino != now.Ino || held.Dev != now.Dev || held.Ino != before.Ino {
		return false, nil
	}
	return true, os.RemoveAll(path)
}
