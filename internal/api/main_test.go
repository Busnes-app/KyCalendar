package api_test

import (
	"os"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/testdb"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testdb.DropTemplate()
	os.Exit(code)
}
