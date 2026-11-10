package balancequery

import "testing"

func TestConvertQuotaFollowsCurrencyRules(t *testing.T) {
	cases := []struct {
		name, body, unit, want string
		rate                   float64
	}{
		{"USD divides by ratio", `{"quota":92275,"used_quota":1000,"username":"x"}`, "USD", `{"quota":"0.18455 USD","used_quota":"0.002 USD","username":"x"}`, 7.3},
		{"CNY multiplies by exchange rate", `{"quota":500000}`, "CNY", `{"quota":"7.3 CNY"}`, 7.3},
		{"CNY falls back to default rate", `{"quota":500000}`, "CNY", `{"quota":"7.3 CNY"}`, 0},
		{"TOKENS shows raw quota", `{"quota":500000}`, "TOKENS", `{"quota":"500000 TOKENS"}`, 7.3},
		{"unknown display type labels dollars", `{"quota":500000}`, "EUR", `{"quota":"1 EUR"}`, 7.3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ConvertQuota(tc.body, DefaultQuotaPerUnit, tc.rate, tc.unit); got != tc.want {
				t.Fatalf("ConvertQuota() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestConvertQuotaLeavesOtherBodiesAlone(t *testing.T) {
	for _, body := range []string{"not json", `{"balance":"12.5"}`, `[1,2]`} {
		if got := ConvertQuota(body, DefaultQuotaPerUnit, 7.3, "USD"); got != body {
			t.Fatalf("ConvertQuota(%q) changed the body to %q", body, got)
		}
	}
	if got := ConvertQuota(`{"quota":92275}`, 0, 7.3, "USD"); got != `{"quota":92275}` {
		t.Fatalf("zero ratio must disable conversion, got %s", got)
	}
}

func TestValidateQuotaPerUnit(t *testing.T) {
	if err := ValidateQuotaPerUnit(0); err != nil {
		t.Fatal("zero must be accepted (disabled)")
	}
	if err := ValidateQuotaPerUnit(-1); err == nil {
		t.Fatal("negative ratio must be rejected")
	}
}

func TestConvertQuotaCoversNestedUserQuota(t *testing.T) {
	body := `{"object":"token_usage","total_available":500000,"data":{"user_quota":{"total_available":1000000,"total_used":500000}}}`
	got := ConvertQuota(body, DefaultQuotaPerUnit, 7.3, "USD")
	want := `{"data":{"user_quota":{"total_available":"2 USD","total_used":"1 USD"}},"object":"token_usage","total_available":"1 USD"}`
	if got != want {
		t.Fatalf("ConvertQuota() = %s, want %s", got, want)
	}
}

func TestConvertQuotaCustomCurrency(t *testing.T) {
	// 1 USD of quota (500000) at a custom rate of 0.9 with a euro symbol.
	got := ConvertQuotaCustom(`{"quota":500000}`, DefaultQuotaPerUnit, 7.3, "CUSTOM", "€", 0.9)
	if got != `{"quota":"€0.9"}` {
		t.Fatalf("custom currency = %s", got)
	}
	got = ConvertQuotaCustom(`{"quota":500000}`, DefaultQuotaPerUnit, 7.3, "CUSTOM", "", 0.9)
	if got != `{"quota":"0.9 CUSTOM"}` {
		t.Fatalf("custom currency without symbol = %s", got)
	}
}
