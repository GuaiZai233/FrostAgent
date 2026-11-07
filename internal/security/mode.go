package security

import "strings"

// ControlMode represents the global security control operating mode.
type ControlMode string

const (
	ControlModeOff        ControlMode = "off"
	ControlModeSimple     ControlMode = "simple"
	ControlModeAggressive ControlMode = "aggressive"
)

// ParseControlMode parses raw string into a valid ControlMode.
// Unset or invalid values safely default to ControlModeSimple.
func ParseControlMode(raw string) ControlMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(ControlModeOff):
		return ControlModeOff
	case string(ControlModeAggressive):
		return ControlModeAggressive
	case string(ControlModeSimple):
		return ControlModeSimple
	default:
		return ControlModeSimple
	}
}

// IsValidControlMode returns true if the raw string matches one of the 3 supported modes.
func IsValidControlMode(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(ControlModeOff), string(ControlModeSimple), string(ControlModeAggressive):
		return true
	default:
		return false
	}
}
