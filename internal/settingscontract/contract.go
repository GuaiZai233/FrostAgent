// Package settingscontract contains the test-only lifecycle inventory shared by
// backend and frontend checks. Production code must not use it to classify keys.
package settingscontract

import _ "embed"

//go:embed testdata/lifecycle.json
var Inventory []byte

type Entry struct {
	Key             string `json:"key"`
	Scope           string `json:"scope"`
	Catalog         string `json:"catalog"`
	InstanceRestart bool   `json:"instanceRestart"`
	ControlRestart  bool   `json:"controlRestart"`
	SQLHot          bool   `json:"sqlHot"`
	Frontend        string `json:"frontend"`
	// Seed is nil for an absent default and points to an empty string for an
	// explicitly stored empty default.
	Seed *string `json:"seed"`
}
