package instanceconfig

import (
	"testing"

	"github.com/joho/godotenv"
)

func TestFormatEnvEntryPreservesQuotesAndJSONWithoutAdditionalKeys(t *testing.T) {
	for _, value := range []string{
		`{"curl":"curl http://localhost -H 'X-Key: {{API_KEY}}'","api_key":"synthetic-'quoted'-key"}`,
		`["send_message","fixture-'quoted'-tool"]`,
		`"leading double quote`,
		`'leading single quote`,
		`'quoted' OTHER_KEY=injected`,
		`"quoted" OTHER_KEY=injected`,
		"first line\n'second line'\nOTHER_KEY=literal",
		`$literal with 'single' and "double" quotes`,
	} {
		formatted, err := FormatEnvEntry("CONFIG", value)
		if err != nil {
			t.Fatalf("format rejected quoted value: %v", err)
		}
		parsed, err := godotenv.Unmarshal(formatted)
		if err != nil || len(parsed) != 1 || parsed["CONFIG"] != value {
			t.Fatalf("quoted value did not round-trip: %v", err)
		}
	}
}
