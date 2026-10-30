package web

import (
	"bytes"
	"os"
	"testing"
)

// The About window shows src/mit.txt; it must stay the repository's licence byte for byte.
func TestAboutLicenceMatchesRepository(t *testing.T) {
	shown, err := os.ReadFile("src/mit.txt")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := os.ReadFile("../LICENSE.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(shown, repo) {
		t.Fatal("web/src/mit.txt differs from LICENSE.txt; copy LICENSE.txt over it")
	}
}
