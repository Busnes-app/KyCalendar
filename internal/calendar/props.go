package calendar

import (
	"errors"
	"fmt"
	"regexp"
	"slices"

	"github.com/emersion/go-ical"
)

const (
	MaxNameBytes        = 255
	MaxDescriptionBytes = 4096
)

var colorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}([0-9A-Fa-f]{2})?$`)

// CheckProps bounds client-set calendar properties; nil means unchanged and "" clears a colour.
func CheckProps(name, description, color *string) error {
	switch {
	case name != nil && len(*name) > MaxNameBytes:
		return fmt.Errorf("displayname over %d bytes", MaxNameBytes)
	case description != nil && len(*description) > MaxDescriptionBytes:
		return fmt.Errorf("calendar-description over %d bytes", MaxDescriptionBytes)
	case color != nil && *color != "" && !colorPattern.MatchString(*color):
		return errors.New("calendar-color must be #RRGGBB or #RRGGBBAA")
	}
	return nil
}

// Components is the component set a calendar holds, in canonical order: VEVENT, VTODO or
// both. An empty request is VEVENT, as every calendar was before tasks; any other name refuses.
func Components(names []string) ([]string, error) {
	if len(names) == 0 {
		return []string{ical.CompEvent}, nil
	}
	var out []string
	for _, c := range []string{ical.CompEvent, ical.CompToDo} {
		if slices.Contains(names, c) {
			out = append(out, c)
		}
	}
	for _, n := range names {
		if !slices.Contains(out, n) {
			return nil, fmt.Errorf("%w: %q", ErrUnsupportedComponent, n)
		}
	}
	return out, nil
}
