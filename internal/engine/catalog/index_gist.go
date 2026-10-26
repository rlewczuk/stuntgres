package catalog

// Comparator-based exclusive claims are sound only for the original equality
// providers. Overlap admissions use existing dirty projections/completion waits.
func (d *IndexDefinition) ExactEqualityClaims() bool {
	if d.Method == GistMethod || d.Method == SpgistMethod {
		return false
	}
	for _, operator := range d.Exclusion {
		if !operator.IsEquality() {
			return false
		}
	}
	return true
}
