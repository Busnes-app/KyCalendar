package calendar_test

import (
	"strings"
	"testing"

	"github.com/Busnes-app/kycalendar/internal/calendar"
)

func TestCheckProps(t *testing.T) {
	s := func(v string) *string { return &v }
	ok := []struct{ name, desc, color *string }{
		{nil, nil, nil}, {s("Team"), s(""), s("")}, {nil, nil, s("#00aa11")}, {nil, nil, s("#00aa11ff")},
		{s(strings.Repeat("n", calendar.MaxNameBytes)), s(strings.Repeat("d", calendar.MaxDescriptionBytes)), nil},
	}
	for i, c := range ok {
		if err := calendar.CheckProps(c.name, c.desc, c.color); err != nil {
			t.Errorf("ok %d: %v", i, err)
		}
	}
	bad := []struct{ name, desc, color *string }{
		{s(strings.Repeat("n", calendar.MaxNameBytes+1)), nil, nil},
		{nil, s(strings.Repeat("d", calendar.MaxDescriptionBytes+1)), nil},
		{nil, nil, s("red")}, {nil, nil, s("#12345")},
	}
	for i, c := range bad {
		if err := calendar.CheckProps(c.name, c.desc, c.color); err == nil {
			t.Errorf("bad %d accepted", i)
		}
	}
}
