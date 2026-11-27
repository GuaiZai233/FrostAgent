package frostctl

import (
	"errors"
	"fmt"
	"strings"
)

type Condition struct {
	Flag  string `json:"flag"`
	Value string `json:"value,omitempty"`
}
type Constraint struct {
	When       []Condition `json:"when,omitempty"`
	Any        []Condition `json:"any_of"`
	ExactlyOne bool        `json:"exactly_one,omitempty"`
	Message    string      `json:"description"`
}

func (c Condition) matches(i Invocation) bool {
	boolean := false
	for _, f := range i.Command.Flags {
		if f.Name == c.Flag {
			boolean = f.Type == "bool"
		}
	}
	for _, v := range i.Values[c.Flag] {
		if (c.Value == "" && v != "" && (!boolean || v == "true")) || c.Value != "" && v == c.Value {
			return true
		}
	}
	return false
}
func (c Constraint) validate(i Invocation) error {
	for _, when := range c.When {
		if !when.matches(i) {
			return nil
		}
	}
	n := 0
	for _, condition := range c.Any {
		if condition.matches(i) {
			n++
		}
	}
	if n == 0 || c.ExactlyOne && n != 1 {
		return errors.New(c.Message)
	}
	return nil
}
func parseFlags(args []string, flags []Flag) ([]string, map[string][]string, error) {
	words := []string{}
	values := map[string][]string{}
	known := map[string]Flag{}
	for _, f := range flags {
		known[f.Name] = f
	}
	for n := 0; n < len(args); n++ {
		a := args[n]
		if !strings.HasPrefix(a, "--") {
			words = append(words, a)
			continue
		}
		key, value, has := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		f, ok := known[key]
		if !ok {
			return words, values, fmt.Errorf("unsupported option --%s", key)
		}
		if f.Type == "bool" {
			if !has {
				value = "true"
			}
			if value != "true" && value != "false" {
				return words, values, fmt.Errorf("--%s expects a boolean", key)
			}
		} else if !has {
			n++
			if n >= len(args) || strings.HasPrefix(args[n], "--") {
				return words, values, fmt.Errorf("--%s requires a value", key)
			}
			value = args[n]
		}
		values[key] = append(values[key], value)
	}
	return words, values, nil
}
