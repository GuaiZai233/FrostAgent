package balancequery

import "testing"

func TestConvertQuotaTurnsQuotaIntoDollars(t *testing.T) {
	got := ConvertQuota(`{"quota":92275,"used_quota":1000,"username":"x"}`, DefaultQuotaPerUnit)
	want := `{"quota":"0.18455 Dollar","used_quota":"0.002 Dollar","username":"x"}`
	if got != want {
		t.Fatalf("ConvertQuota() = %s, want %s", got, want)
	}
}

func TestConvertQuotaLeavesOtherBodiesAlone(t *testing.T) {
	for _, body := range []string{"not json", `{"balance":"12.5"}`, `[1,2]`} {
		if got := ConvertQuota(body, DefaultQuotaPerUnit); got != body {
			t.Fatalf("ConvertQuota(%q) changed the body to %q", body, got)
		}
	}
	if got := ConvertQuota(`{"quota":92275}`, 0); got != `{"quota":92275}` {
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
