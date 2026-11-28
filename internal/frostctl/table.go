package frostctl

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
)

func renderTable(out io.Writer, value any) error {
	writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	var walk func(string, any)
	walk = func(path string, value any) {
		switch v := value.(type) {
		case map[string]any:
			keys := make([]string, 0, len(v))
			for key := range v {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				next := key
				if path != "" {
					next = path + "." + key
				}
				walk(next, v[key])
			}
		case []any:
			if len(v) == 0 {
				fmt.Fprintf(writer, "%s\t(empty)\n", path)
			}
			for n, item := range v {
				walk(fmt.Sprintf("%s[%d]", path, n), item)
			}
		default:
			text := fmt.Sprint(value)
			text = strings.NewReplacer("\t", " ", "\r", " ", "\n", " ↵ ", "\x1b", "").Replace(text)
			fmt.Fprintf(writer, "%s\t%s\n", path, text)
		}
	}
	walk("", value)
	return writer.Flush()
}
