package balancequery

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
)

// DefaultQuotaPerUnit is the New API default ratio of internal quota units per dollar.
const DefaultQuotaPerUnit = 500000

// DefaultQuotaDisplayType labels converted amounts when the template has no display type set.
const DefaultQuotaDisplayType = "USD"

// quotaFields are the New API response fields measured in internal quota units.
var quotaFields = map[string]bool{
	"quota": true, "used_quota": true,
	"total_granted": true, "total_used": true, "total_available": true,
}

// ValidateQuotaPerUnit accepts 0 (conversion disabled) or a finite positive ratio.
func ValidateQuotaPerUnit(v float64) error {
	if v == 0 {
		return nil
	}
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return errors.New("Quota 换算比例必须为正数")
	}
	return nil
}

// ConvertQuota rewrites New API quota fields in a JSON object into dollars with a unit suffix.
// Non-JSON bodies and bodies without quota fields are returned unchanged.
func ConvertQuota(body string, perUnit float64, unit string) string {
	if unit == "" {
		unit = DefaultQuotaDisplayType
	}
	if perUnit <= 0 {
		return body
	}
	var fields map[string]any
	if json.Unmarshal([]byte(body), &fields) != nil {
		return body
	}
	changed := convertQuotaFields(fields, perUnit, unit)
	if data, ok := fields["data"].(map[string]any); ok && convertQuotaFields(data, perUnit, unit) {
		changed = true
	}
	if !changed {
		return body
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return body
	}
	return string(out)
}

func formatDollars(v float64) string {
	s := strconv.FormatFloat(v, 'f', 6, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}

// convertQuotaFields rewrites quota-valued numbers in one object and reports whether any changed.
func convertQuotaFields(fields map[string]any, perUnit float64, unit string) bool {
	changed := false
	for name, value := range fields {
		number, ok := value.(float64)
		if !ok || !quotaFields[name] {
			continue
		}
		fields[name] = formatDollars(number/perUnit) + " " + unit
		changed = true
	}
	return changed
}
