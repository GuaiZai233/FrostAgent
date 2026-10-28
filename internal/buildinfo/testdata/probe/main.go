package main

import (
	"encoding/json"
	"os"

	"github.com/GuaiZai233/FrostAgent/internal/buildinfo"
)

func main() {
	if err := json.NewEncoder(os.Stdout).Encode(map[string]string{
		"display": buildinfo.DisplayVersion(),
		"version": buildinfo.Version,
		"commit":  buildinfo.Commit,
	}); err != nil {
		panic(err)
	}
}
