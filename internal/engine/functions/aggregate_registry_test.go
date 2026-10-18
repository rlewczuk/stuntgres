package functions

import "testing"

func TestAggregateDefinitionsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, definition := range aggregateDefinitions {
		if len(definition.Names) == 0 || definition.Bind == nil {
			t.Fatalf("incomplete aggregate definition %v", definition.Names)
		}
		for _, name := range definition.Names {
			// The parser folds aggregate names; matching is exact.
			if name == "" || seen[name] || name != toLowerASCII(name) {
				t.Fatalf("empty, duplicate or non-lowercase aggregate name %q", name)
			}
			seen[name] = true
		}
	}
}

func toLowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}
