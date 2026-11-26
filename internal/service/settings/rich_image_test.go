package settings

import (
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/richimage"
)

func TestRichImageThemeModeValidation(t *testing.T) {
	for _, value := range []string{"", "model", "day_night"} {
		if err := validateUpdate(richimage.ThemeModeKey, value); err != nil {
			t.Errorf("%q must be accepted: %v", value, err)
		}
	}
	for _, value := range []string{"dark", "auto", "light"} {
		if err := validateUpdate(richimage.ThemeModeKey, value); err == nil {
			t.Errorf("%q must be rejected", value)
		}
	}
}
