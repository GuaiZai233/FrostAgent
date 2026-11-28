package frostctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/buildinfo"
)

type Result struct {
	Status       string            `json:"status"`
	Data         any               `json:"data,omitempty"`
	Error        *Error            `json:"error,omitempty"`
	Resource     string            `json:"resource,omitempty"`
	Verification []string          `json:"verification_command,omitempty"`
	Correlation  map[string]string `json:"correlation,omitempty"`
}
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// openClient is replaced in tests to route Run through a local test server.
var openClient = newClient

func Run(ctx context.Context, args []string, in io.Reader, out, diagnostics io.Writer) int {
	inv, err := Parse(args)
	if err != nil {
		output := "table"
		for n, a := range args {
			if a == "--output=json" || a == "--output" && n+1 < len(args) && args[n+1] == "json" {
				output = "json"
			}
		}
		render(out, diagnostics, output, Result{Status: "failed", Error: &Error{Code: "invalid_argument", Message: err.Error()}})
		return 2
	}
	timeout := 30 * time.Second
	if value := inv.Value("timeout"); value != "" {
		timeout, err = time.ParseDuration(value)
		if err != nil || timeout <= 0 {
			render(out, diagnostics, inv.Value("output"), Result{Status: "failed", Error: &Error{Code: "invalid_argument", Message: "--timeout must be a positive duration"}})
			return 2
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var data any
	switch inv.Command.Name {
	case "help", "schema":
		data, err = SelectSchema(inv.Args)
		if err == nil && inv.Command.Name == "help" && inv.Value("output") == "table" {
			for _, c := range data.([]Command) {
				fmt.Fprintf(out, "%-30s %s\n", c.Name, c.Description)
				if len(inv.Args) > 0 {
					for _, f := range c.Flags {
						required := ""
						if f.Required {
							required = " (required)"
						}
						fmt.Fprintf(out, "  --%-20s %s%s\n", f.Name, f.Description, required)
					}
					for _, constraint := range c.Constraints {
						fmt.Fprintf(out, "  constraint: %s\n", constraint.Message)
					}
				}
			}
			return 0
		}
	case "version":
		if !inv.Bool("server") {
			data = map[string]any{"version": buildinfo.DisplayVersion(), "commit": buildinfo.SourceCommit(), "program": "frostctl"}
			break
		}
		fallthrough
	default:
		if strings.HasPrefix(inv.Command.Name, "remote ") {
			data, err = remoteCommand(ctx, inv, in)
		} else {
			var c *client
			c, err = openClient(ctx, inv)
			if err == nil {
				defer c.close()
				data, err = execute(ctx, c, inv, in, out)
			}
		}
	}
	result := Result{Status: "success", Data: data}
	if remote, ok := data.(Remote); ok && err == nil && inv.Command.Name == "remote token set" && remote.Store == "file" {
		fmt.Fprintln(diagnostics, "warning: system credential storage unavailable; token saved in an unencrypted file restricted to the current user (store=file)")
	}
	exit := 0
	if err != nil {
		result.Status = "failed"
		result.Error = &Error{Code: "operation_failed", Message: err.Error()}
		exit = 1
		var commandErr *commandError
		if errors.As(err, &commandErr) {
			result.Status = commandErr.Status
			result.Error.Code = commandErr.Code
			result.Resource = commandErr.Resource
			result.Verification = commandErr.Verification
			result.Correlation = commandErr.Correlation
			if result.Status == "unknown" {
				exit = 3
			}
		}
	}
	if err = render(out, diagnostics, inv.Value("output"), result); err != nil {
		fmt.Fprintln(diagnostics, "cannot write command result")
		return 1
	}
	return exit
}
func render(out, diagnostics io.Writer, format string, result Result) error {
	if format == "json" || format == "jsonl" {
		encoder := json.NewEncoder(out)
		if format == "json" {
			encoder.SetIndent("", "  ")
		}
		return encoder.Encode(result)
	}
	if result.Error != nil {
		fmt.Fprintf(diagnostics, "%s: %s (%s)\n", result.Status, result.Error.Message, result.Error.Code)
		if result.Resource != "" {
			fmt.Fprintf(diagnostics, "resource: %s\n", result.Resource)
		}
		if len(result.Verification) > 0 {
			fmt.Fprintf(diagnostics, "verify: %s\n", shellJoin(result.Verification))
		}
		for _, key := range slices.Sorted(maps.Keys(result.Correlation)) {
			fmt.Fprintf(diagnostics, "correlation %s: %s\n", key, result.Correlation[key])
		}
	}
	encoded, err := json.Marshal(result.Data)
	if err != nil {
		return err
	}
	var object any
	if json.Unmarshal(encoded, &object) != nil {
		return err
	}
	return renderTable(out, object)
}

// shellJoin quotes arguments a POSIX shell would split or expand, so a printed
// verification command can be pasted as-is.
func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for n, arg := range args {
		if arg == "" || strings.ContainsAny(arg, " \t\n'\"\\$`;&|<>()*?[]{}#~!") {
			arg = strconv.Quote(arg)
		}
		quoted[n] = arg
	}
	return strings.Join(quoted, " ")
}
