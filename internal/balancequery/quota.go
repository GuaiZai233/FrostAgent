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

// DefaultUSDExchangeRate converts dollars to CNY when a CNY display has no rate configured.
const DefaultUSDExchangeRate = 7.3

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

// ConvertQuota rewrites New API quota fields into a readable amount with a unit label, following New API's
// per-currency rules: TOKENS shows the raw quota; CNY shows dollars times the exchange rate; any other
// display type (USD by default) shows dollars. Non-JSON bodies and bodies without quota fields are unchanged.
func ConvertQuota(body string, perUnit, usdRate float64, unit string) string {
	if perUnit <= 0 {
		return body
	}
	if unit == "" {
		unit = DefaultQuotaDisplayType
	}
	var fields map[string]any
	if json.Unmarshal([]byte(body), &fields) != nil {
		return body
	}
	changed := convertQuotaFields(fields, perUnit, usdRate, unit)
	if data, ok := fields["data"].(map[string]any); ok && convertQuotaFields(data, perUnit, usdRate, unit) {
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

// convertQuotaFields rewrites quota-valued numbers in one object and reports whether any changed.
func convertQuotaFields(fields map[string]any, perUnit, usdRate float64, unit string) bool {
	changed := false
	for name, value := range fields {
		number, ok := value.(float64)
		if !ok || !quotaFields[name] {
			continue
		}
		fields[name] = formatQuotaAmount(number, perUnit, usdRate, unit)
		changed = true
	}
	return changed
}

// formatQuotaAmount applies the display rule for one quota value.
func formatQuotaAmount(quota, perUnit, usdRate float64, unit string) string {
	switch strings.ToUpper(unit) {
	case "TOKENS":
		return formatDecimal(quota) + " TOKENS"
	case "CNY":
		rate := usdRate
		if rate <= 0 {
			rate = DefaultUSDExchangeRate
		}
		return formatDecimal(quota/perUnit*rate) + " CNY"
	default:
		return formatDecimal(quota/perUnit) + " " + unit
	}
}

func formatDecimal(v float64) string {
	s := strconv.FormatFloat(v, 'f', 6, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}
