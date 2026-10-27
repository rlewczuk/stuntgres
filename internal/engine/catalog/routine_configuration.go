package catalog

import "strings"

// RoutineConfiguration owns ordered, normalized name=value declarations. Its
// backing is immutable after construction and may be shared by catalog copies.
type RoutineConfiguration struct {
	entries []string
}

func NewRoutineConfiguration(entries []string) *RoutineConfiguration {
	if len(entries) == 0 {
		return nil
	}
	return &RoutineConfiguration{entries: append([]string(nil), entries...)}
}

func (c *RoutineConfiguration) Len() int {
	if c == nil {
		return 0
	}
	return len(c.entries)
}

func (c *RoutineConfiguration) At(index int) string { return c.entries[index] }

// SameRoutineConfiguration compares declaration values across logical replay.
func SameRoutineConfiguration(a, b *RoutineConfiguration) bool {
	if a == b {
		return true
	}
	if a.Len() != b.Len() {
		return false
	}
	for i := range a.Len() {
		if a.At(i) != b.At(i) {
			return false
		}
	}
	return true
}

// Valid checks the immutable serialized declaration shape, not session access.
func (c *RoutineConfiguration) Valid() bool {
	for i := range c.Len() {
		name, _, ok := strings.Cut(c.At(i), "=")
		if !ok || name == "" || name != strings.ToLower(name) {
			return false
		}
		for j := range i {
			previous, _, _ := strings.Cut(c.At(j), "=")
			if previous == name {
				return false
			}
		}
	}
	return true
}
