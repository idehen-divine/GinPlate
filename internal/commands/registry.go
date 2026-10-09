// Package custom hosts clonable one-off commands (`ginplate make:command`)
// that self-register via init().
package custom

import (
	"sort"

	"github.com/spf13/cobra"
)

var (
	customRegistryNames []string
	customRegistry      = map[string]func() *cobra.Command{}
)

// RegisterCustom registers a constructor; later calls replace.
func RegisterCustom(use string, fn func() *cobra.Command) {
	if _, ok := customRegistry[use]; !ok {
		customRegistryNames = append(customRegistryNames, use)
	}
	customRegistry[use] = fn
}

// Registered returns constructors sorted by name.
func Registered() []*cobra.Command {
	names := append([]string(nil), customRegistryNames...)
	sort.Strings(names)
	out := make([]*cobra.Command, 0, len(names))
	for _, n := range names {
		if fn, ok := customRegistry[n]; ok && fn != nil {
			out = append(out, fn())
		}
	}
	return out
}
