package clientconfig

import (
	"fmt"
	"sort"
	"strings"
)

// The clients the dashboard can configure, and the provider id written into
// their config files.
//
// ProviderKey is the one place the product name reaches disk. Every adapter
// below that writes a named provider takes it from here, so renaming the
// gateway renames what the clients see, instead of leaving a provider named
// after the upstream fork in the user's file.
//
// ponytail: the key is fixed at build time rather than read from the request.
// A client-side value would let a crafted POST pick an arbitrary object key to
// splice into the user's config, and the dashboard has no use case for more
// than one name.
const ProviderKey = "NOX9GO"

// adapterFactories lists the clients in the order the dashboard shows them.
var adapterFactories = []func() Adapter{
	func() Adapter { return newOpenCodeAdapter(ProviderKey) },
	func() Adapter { return newPiAdapter(ProviderKey) },
}

// adapterAliases maps every accepted spelling to its canonical id. The aliases
// exist because users type both forms, and refusing one is a needless failure.
var adapterAliases = map[string]string{
	"opencode":  "opencode",
	"open-code": "opencode",
	"pi":        "pi",
}

// registry is built once: the adapters are stateless values, so a shared map is
// safe and keeps Resolve from allocating on every lookup.
var registry = func() map[string]Adapter {
	m := make(map[string]Adapter, len(adapterFactories))
	for _, f := range adapterFactories {
		a := f()
		m[a.ID()] = a
	}
	return m
}()

// IDs returns the canonical ids in their declared order.
func IDs() []string {
	out := make([]string, 0, len(adapterFactories))
	for _, f := range adapterFactories {
		out = append(out, f().ID())
	}
	return out
}

// Resolve turns a user-typed tool name into an adapter.
//
// An unknown name is an error naming what is accepted, rather than a silent
// skip: an apply that quietly ignored a misspelled tool would configure nothing
// and report success.
func Resolve(name string) (Adapter, error) {
	id, ok := adapterAliases[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return nil, fmt.Errorf("%w: unknown tool %q (accepted: %s)",
			ErrUnsupportedStructure, name, strings.Join(knownNames(), ", "))
	}
	return registry[id], nil
}

// knownNames lists the canonical ids and aliases for an error message.
func knownNames() []string {
	out := make([]string, 0, len(adapterAliases))
	for k := range adapterAliases {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
