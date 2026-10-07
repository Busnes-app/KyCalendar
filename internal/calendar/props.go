package calendar

import (
	"errors"
	"fmt"
	"regexp"
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
