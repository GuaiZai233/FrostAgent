package frostctl

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Flag struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required,omitempty"`
	Repeat      bool   `json:"repeat,omitempty"`
	Description string `json:"description"`
}
type Command struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Arguments   []string     `json:"arguments"`
	MinArgs     int          `json:"min_args"`
	Scope       string       `json:"scope"`
	Permission  string       `json:"permission"`
	Effect      string       `json:"side_effect"`
	Outputs     []string     `json:"outputs"`
	Flags       []Flag       `json:"flags"`
	Constraints []Constraint `json:"constraints,omitempty"`
}

func flag(name, typ, description string) Flag {
	return Flag{Name: name, Type: typ, Description: description}
}
func requiredFlag(name, typ, description string) Flag {
	return Flag{Name: name, Type: typ, Required: true, Description: description}
}
func definition(name, description, scope, effect string, args []string, flags ...Flag) Command {
	local := scope == "local"
	permission := "OS administrator over IPC; management token over HTTPS"
	if local {
		permission = "current user"
	}
	common := []Flag{flag("output", "string", "table or json"), flag("timeout", "duration", "maximum wait, default 30s")}
	if !local {
		common = append(common, flag("remote", "string", "explicit remote alias for this invocation"))
	}
	if scope == "instance" || scope == "config" || scope == "logs" || scope == "doctor" {
		common = append(common, flag("instance", "string", "explicit instance ID"))
	}
	if scope == "config" || scope == "logs" || scope == "global" {
		common = append(common, flag("scope", "string", "explicit resource scope"))
	}
	constraints := []Constraint{}
	switch scope {
	case "instance":
		constraints = append(constraints, Constraint{Any: []Condition{{Flag: "instance"}}, Message: "--instance is required"})
	case "global":
		constraints = append(constraints, Constraint{Any: []Condition{{Flag: "scope", Value: "global"}}, Message: "--scope global is required"})
	case "config":
		constraints = append(constraints, Constraint{Any: []Condition{{Flag: "instance"}, {Flag: "scope", Value: "global"}}, Message: "select --instance or --scope global"})
	case "logs":
		constraints = append(constraints, Constraint{Any: []Condition{{Flag: "instance"}, {Flag: "scope", Value: "control-plane"}}, Message: "select --instance or --scope control-plane"})
	}
	return Command{Name: name, Description: description, Arguments: args, MinArgs: len(args), Scope: scope, Permission: permission, Effect: effect, Outputs: []string{"table", "json"}, Flags: append(common, flags...), Constraints: constraints}
}
func Commands() []Command {
	cs := []Command{
		definition("version", "CLI build identity", "local", "none", nil, flag("server", "bool", "query selected server"), flag("remote", "string", "explicit remote alias with --server")),
		definition("help", "Discover commands", "local", "none", []string{"command..."}),
		definition("schema", "Machine-readable command definitions", "local", "none", []string{"command..."}),
		definition("remote list", "List saved remote connections", "local", "none", nil),
		definition("remote show", "Show a saved remote without credentials", "local", "none", []string{"name"}),
		definition("remote add", "Create an HTTPS remote alias", "local", "local-write", []string{"name"}, requiredFlag("url", "string", "HTTPS deployment URL")),
		definition("remote update", "Update URL; clear token on origin change", "local", "local-write", []string{"name"}, requiredFlag("url", "string", "HTTPS deployment URL")),
		definition("remote remove", "Remove alias and saved credential", "local", "local-write", []string{"name"}),
		definition("remote token set", "Save an origin-bound token from stdin", "local", "local-write", []string{"name"}, requiredFlag("stdin", "bool", "read token from standard input")),
		definition("remote token unset", "Remove saved token", "local", "local-write", []string{"name"}),
		definition("instance list", "List deployment instances", "deployment", "none", nil),
		definition("instance show", "Show instance metadata", "id", "none", []string{"id"}),
		definition("status", "Show instance runtime overview", "instance", "none", nil),
		definition("doctor", "Inspect observed state without external probes", "doctor", "none", nil, Flag{Name: "check", Type: "string", Repeat: true, Description: "management, storage, instance, onebot, model-router, dependencies"}),
		definition("adapter list", "List OneBot state", "instance", "none", nil),
		definition("adapter show", "Show OneBot state", "instance", "none", []string{"name"}),
		definition("storage status", "Database connectivity and compatibility", "deployment", "none", nil),
	}
	for _, item := range []struct {
		name, description string
		args              []string
	}{
		{"schema", "Describe configuration fields", []string{"key"}},
		{"list", "Read redacted configuration", nil}, {"get", "Read one configuration field", []string{"key"}},
		{"export", "Export editable non-secret configuration", nil}, {"validate", "Validate a candidate without writes", nil},
		{"diff", "Compare a candidate without writes", nil}, {"apply", "Atomically apply a batch", nil},
		{"set", "Set a non-secret field", []string{"key"}}, {"unset", "Remove a field override", []string{"key"}},
		{"secret set", "Set a secret from stdin", []string{"key"}}, {"secret unset", "Remove a secret", []string{"key"}},
	} {
		effect := "none"
		flags := []Flag{}
		switch item.name {
		case "export", "validate", "diff", "apply":
			flags = append(flags, requiredFlag("file", "string", "local JSON configuration file"))
		}
		if item.name == "apply" {
			flags = append(flags, flag("dry-run", "bool", "validate without writing"))
			effect = "write"
		}
		if item.name == "set" {
			flags = append(flags, requiredFlag("value", "string", "configuration value"))
			effect = "write"
		}
		if item.name == "unset" || strings.HasPrefix(item.name, "secret ") {
			effect = "write"
		}
		if item.name == "secret set" {
			flags = append(flags, requiredFlag("stdin", "bool", "read secret from standard input"))
		}
		c := definition("config "+item.name, item.description, "config", effect, item.args, flags...)
		if item.name == "schema" {
			c.MinArgs = 0
		}
		cs = append(cs, c)
	}
	for _, name := range []string{"list", "tail", "show", "stats", "export", "clear"} {
		flags := []Flag{}
		args := []string{}
		effect := "none"
		if name == "show" {
			args = []string{"error-uuid"}
			flags = append(flags, flag("include-payload", "bool", "include full diagnostic content"))
		} else if name == "clear" {
			effect = "write"
			flags = append(flags, requiredFlag("confirm", "bool", "confirm clearing all retained logs"))
		} else {
			for _, f := range []string{"since", "until", "level", "source", "trace", "search"} {
				flags = append(flags, flag(f, "string", "server-side "+f+" filter"))
			}
			if name != "stats" {
				flags = append(flags, flag("limit", "int", "page size"), flag("cursor", "string", "opaque page cursor"))
			}
		}
		if name == "export" {
			flags = append(flags, requiredFlag("file", "string", "local JSONL destination"))
		}
		if name == "tail" {
			flags = append(flags, flag("follow", "bool", "stream new entries; reconnect gaps are possible"))
		}
		c := definition("logs "+name, "Query selected retained logs", "logs", effect, args, flags...)
		if name == "tail" {
			c.Outputs = append(c.Outputs, "jsonl")
		}
		cs = append(cs, c)
	}
	// P1 trace, session, model, MCP, audit and billing commands. These all
	// use the same definition consumed by help, schema and Parse.
	cs = append(cs,
		definition("trace find", "Find retained records by trace or error ID", "instance", "none", nil,
			flag("trace", "string", "trace ID"), flag("error", "string", "error UUID")),
		definition("trace show", "Show the retained processing timeline", "instance", "none", []string{"trace-id"}),
		definition("session list", "List queryable sessions", "instance", "none", nil,
			flag("platform", "string", "platform filter"), flag("limit", "int", "page size"), flag("cursor", "string", "opaque page cursor")),
		definition("session inspect", "Inspect a session model context", "instance", "none", []string{"id"}, flag("include-prompt", "bool", "include the full prompt")),
		definition("session history", "Read persisted chat history", "instance", "none", []string{"id"}, flag("limit", "int", "page size"), flag("cursor", "string", "opaque page cursor"), flag("after", "string", "RFC3339 lower bound"), flag("before", "string", "RFC3339 upper bound")),
		definition("session tools", "List archived tool calls for a session", "instance", "none", []string{"id"}, flag("limit", "int", "page size")),
		definition("session tool show", "Inspect an archived tool call", "instance", "none", []string{"id", "call-id"}, flag("include-result", "bool", "include the original result"), flag("offset", "int", "result character offset")),
		definition("session summary clear", "Clear a group session summary", "instance", "write", []string{"id"}, requiredFlag("confirm", "bool", "confirm deleting the summary")),
		definition("model endpoint list", "List active shared model endpoints", "global", "none", nil),
		definition("model endpoint show", "Show one active model endpoint", "global", "none", []string{"id"}),
		definition("model endpoint add", "Add and activate a shared endpoint", "global", "write", nil, requiredFlag("file", "string", "endpoint JSON file")),
		definition("model endpoint update", "Update and activate a shared endpoint", "global", "write", []string{"id"}, requiredFlag("file", "string", "endpoint JSON file")),
		definition("model endpoint remove", "Remove an unused shared endpoint", "global", "write", []string{"id"}),
		definition("model endpoint secret set", "Set an active endpoint secret from stdin", "global", "write", []string{"id"}, requiredFlag("stdin", "bool", "read secret from standard input")),
		definition("model endpoint secret unset", "Remove an active endpoint secret", "global", "write", []string{"id"}),
		definition("model list", "List models from an active endpoint", "global", "none", nil, requiredFlag("endpoint", "string", "endpoint ID")),
		definition("model test", "Test a registered active model", "deployment", "external-call", []string{"model"}, requiredFlag("endpoint", "string", "endpoint ID")),
		definition("model route list", "List active instance model routes", "instance", "none", nil),
		definition("model route show", "Explain one active model route", "instance", "none", []string{"name"}),
		definition("model route set", "Set an active instance model route", "instance", "write", []string{"name"}, requiredFlag("file", "string", "route JSON file")),
		definition("model route unset", "Remove an instance model route override", "instance", "write", []string{"name"}),
		definition("mcp server list", "List MCP servers", "instance", "none", nil),
		definition("mcp server show", "Show an MCP server", "instance", "none", []string{"id"}),
		definition("mcp server sync", "Synchronize an MCP server tool list", "instance", "write", []string{"id"}),
		definition("mcp server add", "Add an MCP server", "instance", "write", nil, requiredFlag("file", "string", "MCP server JSON file")),
		definition("mcp server update", "Update an MCP server", "instance", "write", []string{"id"}, requiredFlag("file", "string", "MCP server JSON file")),
		definition("mcp server enable", "Enable an MCP server", "instance", "write", []string{"id"}),
		definition("mcp server disable", "Disable an MCP server", "instance", "write", []string{"id"}),
		definition("mcp server remove", "Remove an MCP server", "instance", "write", []string{"id"}, requiredFlag("confirm", "bool", "confirm deletion")),
		definition("mcp tool list", "List MCP tools", "instance", "none", nil, flag("server", "string", "server ID filter")),
		definition("mcp tool enable", "Enable one MCP tool", "instance", "write", []string{"server-id", "tool"}),
		definition("mcp tool disable", "Disable one MCP tool", "instance", "write", []string{"server-id", "tool"}),
		definition("security audit list", "List security audit records", "instance", "none", nil, flag("limit", "int", "page size, 1-1000"), flag("offset", "int", "skip matching records, 0-10000")),
		definition("security audit show", "Show one security audit record", "instance", "none", []string{"id"}),
		definition("billing status", "Show billing configuration status", "deployment", "none", nil),
	)
	// P2 lifecycle, backup and support commands.
	create := definition("instance create", "Create a disabled instance", "deployment", "write", nil, requiredFlag("name", "string", "instance name"))
	create.MinArgs = 0
	cs = append(cs, create,
		definition("instance rename", "Rename an instance", "id", "write", []string{"id"}, requiredFlag("name", "string", "new instance name")),
		definition("instance enable", "Enable an instance", "id", "write", []string{"id"}),
		definition("instance disable", "Disable an instance", "id", "write", []string{"id"}),
		definition("instance restart", "Stop and start an instance", "id", "write", []string{"id"}),
		definition("instance config copy", "Copy exportable settings into a target instance", "instance", "write", nil, requiredFlag("from", "string", "source instance ID")),
		definition("instance delete prepare", "Prepare an instance for deletion", "id", "write", []string{"id"}),
		definition("instance delete cancel", "Cancel a pending instance deletion", "id", "write", []string{"id"}),
		definition("instance delete confirm", "Confirm permanent instance deletion", "id", "write", []string{"id"}, requiredFlag("backup", "string", "verified backup path"), requiredFlag("confirm", "bool", "confirm deletion")),
		definition("backup create", "Export an instance backup ZIP", "instance", "none", nil, requiredFlag("file", "string", "local ZIP destination")),
		definition("backup verify", "Verify a backup ZIP", "deployment", "none", nil, requiredFlag("file", "string", "local ZIP path")),
		definition("backup restore", "Restore a backup into a new disabled instance", "deployment", "write", nil, requiredFlag("file", "string", "local ZIP path"), requiredFlag("name", "string", "new instance name"), flag("dry-run", "bool", "calculate a restore plan without writing"), flag("confirm", "bool", "confirm restore")),
		definition("support collect", "Collect a redacted diagnostic package", "deployment", "none", nil, requiredFlag("since", "string", "RFC3339 lower bound"), requiredFlag("file", "string", "local diagnostic ZIP path")),
	)
	for i := range cs {
		c := &cs[i]
		if c.Name == "help" || c.Name == "schema" {
			c.MinArgs = 0
		}
		switch c.Name {
		case "version":
			c.Constraints = append(c.Constraints, Constraint{When: []Condition{{Flag: "remote"}}, Any: []Condition{{Flag: "server", Value: "true"}}, Message: "--remote requires version --server"})
		case "trace find":
			c.Constraints = append(c.Constraints, Constraint{Any: []Condition{{Flag: "trace"}, {Flag: "error"}}, ExactlyOne: true, Message: "select exactly one of --trace or --error"})
		case "backup restore":
			c.Constraints = append(c.Constraints, Constraint{Any: []Condition{{Flag: "dry-run", Value: "true"}, {Flag: "confirm", Value: "true"}}, ExactlyOne: true, Message: "select exactly one of --dry-run or --confirm"})
		case "doctor":
			for _, name := range []string{"instance", "onebot", "model-router", "dependencies"} {
				c.Constraints = append(c.Constraints, Constraint{When: []Condition{{Flag: "check", Value: name}}, Any: []Condition{{Flag: "instance"}}, Message: "instance diagnostics require --instance"})
			}
		}
	}

	return cs
}

type Invocation struct {
	Command Command
	Args    []string
	Values  map[string][]string
}

func (i Invocation) Value(name string) string {
	vs := i.Values[name]
	if len(vs) > 0 {
		return vs[len(vs)-1]
	}
	return ""
}
func (i Invocation) Bool(name string) bool { return i.Value(name) == "true" }
func Parse(args []string) (Invocation, error) {
	if len(args) == 0 {
		args = []string{"help"}
	}
	commands := Commands()
	var selected *Command
	var words []string
	var values map[string][]string
	var selectedErr error
	for idx := range commands {
		c := &commands[idx]
		w, v, err := parseFlags(args, c.Flags)
		parts := strings.Fields(c.Name)
		if len(w) >= len(parts) && strings.Join(w[:len(parts)], " ") == c.Name && (selected == nil || len(c.Name) > len(selected.Name)) {
			selected, words, values, selectedErr = c, w, v, err
		}
	}
	if selected == nil {
		return Invocation{}, errors.New("unknown command or options; run frostctl help")
	}
	if selectedErr != nil {
		return Invocation{}, selectedErr
	}
	positional := words[len(strings.Fields(selected.Name)):]
	inv := Invocation{Command: *selected, Args: positional, Values: values}
	allowed := map[string]Flag{}
	for _, f := range selected.Flags {
		allowed[f.Name] = f
		if f.Required {
			if f.Type == "bool" {
				if inv.Bool(f.Name) == false {
					return inv, fmt.Errorf("--%s is required", f.Name)
				}
			} else if _, exists := values[f.Name]; !exists || (f.Name != "value" && inv.Value(f.Name) == "") {
				return inv, fmt.Errorf("--%s is required", f.Name)
			}
		}
	}
	for key, vs := range values {
		f, ok := allowed[key]
		if !ok {
			return inv, fmt.Errorf("--%s is not supported by %s", key, selected.Name)
		}
		if len(vs) > 1 && !f.Repeat {
			return inv, fmt.Errorf("--%s cannot be repeated", key)
		}
		if f.Type == "int" {
			n, err := strconv.Atoi(vs[0])
			if selected.Name == "security audit list" && key == "limit" && (err != nil || n < 1 || n > 1000) {
				return inv, errors.New("--limit must be between 1 and 1000")
			}
			if err != nil || n < 0 || (key != "offset" && n < 1) || n > 10000 {
				return inv, fmt.Errorf("--%s must be between 1 and 10000", key)
			}
		}
		if f.Type == "duration" {
			d, err := time.ParseDuration(vs[0])
			if err != nil || d <= 0 {
				return inv, fmt.Errorf("--%s must be a positive duration", key)
			}
		}
	}
	if len(positional) < selected.MinArgs || (selected.Name != "help" && selected.Name != "schema" && len(positional) > len(selected.Arguments)) {
		return inv, fmt.Errorf("usage: frostctl %s %s", selected.Name, strings.Join(selected.Arguments, " "))
	}
	output := inv.Value("output")
	if output == "" {
		output = "table"
		values["output"] = []string{output}
	}
	valid := false
	for _, v := range selected.Outputs {
		valid = valid || output == v
	}
	if !valid {
		return inv, fmt.Errorf("unsupported output %q", output)
	}
	if inv.Value("instance") != "" && inv.Value("scope") != "" && inv.Value("scope") != "instance" {
		return inv, errors.New("--instance conflicts with --scope")
	}
	if scope := inv.Value("scope"); scope != "" {
		valid := scope == "global" && (selected.Scope == "config" || selected.Scope == "global") || scope == "instance" && (selected.Scope == "config" || selected.Scope == "logs") || scope == "control-plane" && selected.Scope == "logs"
		if !valid {
			return inv, errors.New("invalid resource scope")
		}
	}
	if selected.Name == "doctor" {
		checks := values["check"]
		if len(checks) == 0 {
			checks = []string{"management", "storage", "instance", "onebot", "model-router", "dependencies"}
			values["check"] = checks
		}
		for _, v := range checks {
			switch v {
			case "management", "storage":
			case "instance", "onebot", "model-router", "dependencies":
				if inv.Value("instance") == "" {
					return inv, errors.New("instance diagnostics require --instance")
				}
			default:
				return inv, fmt.Errorf("unknown check %q", v)
			}
		}
	}
	for _, rule := range selected.Constraints {
		if err := rule.validate(inv); err != nil {
			return inv, err
		}
	}

	return inv, nil
}
func SelectSchema(path []string) ([]Command, error) {
	prefix := strings.Join(path, " ")
	out := []Command{}
	for _, c := range Commands() {
		if prefix == "" || c.Name == prefix || strings.HasPrefix(c.Name, prefix+" ") {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(out) == 0 {
		return nil, errors.New("unknown command")
	}
	return out, nil
}
