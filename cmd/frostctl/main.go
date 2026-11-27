package main

import (
	"context"
	"github.com/GuaiZai233/FrostAgent/internal/frostctl"
	"os"
)

func main() { os.Exit(frostctl.Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
