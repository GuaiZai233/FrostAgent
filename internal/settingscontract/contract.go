// Package settingscontract contains the test-only lifecycle inventory shared by
// backend and frontend checks. Production code must not use it to classify keys.
package settingscontract

import (
	_ "embed"
	"encoding/csv"
	"io"
	"strings"
	"testing"
)

//go:embed testdata/lifecycle.tsv
var inventory string

type Entry struct {
	Key, Scope, Catalog, Frontend           string
	InstanceRestart, ControlRestart, SQLHot bool
	// Seed is nil when the new-instance template does not contain the key.
	// An empty, non-nil seed is an explicitly stored empty value.
	Seed *string
}

func Load(t testing.TB) map[string]Entry {
	t.Helper()
	reader := csv.NewReader(strings.NewReader(inventory))
	reader.Comma = '\t'
	reader.FieldsPerRecord = 8
	header, err := reader.Read()
	if err != nil || strings.Join(header, "\t") != "key\tscope\tcatalog\tinstance_restart\tcontrol_restart\tsql_hot\tfrontend\tseed" {
		t.Fatalf("invalid lifecycle inventory header: %v, %v", header, err)
	}
	entries := make(map[string]Entry)
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		key := row[0]
		if _, exists := entries[key]; key == "" || exists {
			t.Fatalf("empty or duplicate lifecycle key %q", key)
		}
		boolean := func(value string) bool {
			t.Helper()
			if value != "true" && value != "false" {
				t.Fatalf("%s: invalid lifecycle boolean %q", key, value)
			}
			return value == "true"
		}
		if row[1] != "instance" && row[1] != "global" && row[1] != "shared" {
			t.Fatalf("%s: invalid scope %q", key, row[1])
		}
		if row[2] != "absent" && row[2] != "live" && row[2] != "restart" {
			t.Fatalf("%s: invalid catalog state %q", key, row[2])
		}
		if row[6] != "hot" && row[6] != "reload" && row[6] != "dedicated" && row[6] != "hidden" {
			t.Fatalf("%s: invalid frontend state %q", key, row[6])
		}
		var seed *string
		if row[7] != "<absent>" {
			value := row[7]
			if value == "<empty>" {
				value = ""
			}
			seed = &value
		}
		entries[key] = Entry{Key: key, Scope: row[1], Catalog: row[2],
			InstanceRestart: boolean(row[3]), ControlRestart: boolean(row[4]),
			SQLHot: boolean(row[5]), Frontend: row[6], Seed: seed}
	}
	if len(entries) == 0 {
		t.Fatal("empty lifecycle inventory")
	}
	return entries
}
