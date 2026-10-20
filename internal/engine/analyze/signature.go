package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// SameStatementUsage compares parameter Usages exactly, including the
// shape of anonymous record occurrences.
func SameStatementUsage(a, b sqltype.Usage) bool {
	if a.Type == nil || b.Type == nil {
		return a.Type == b.Type && a.Typmod == b.Typmod && a.Collation == b.Collation
	}
	// Parameter contracts retain exact occurrence shape, unlike public results.
	if sqltype.OccurrenceRecordArray(a.Type) || sqltype.OccurrenceRecordArray(b.Type) {
		return sqltype.OccurrenceRecordArray(a.Type) && sqltype.OccurrenceRecordArray(b.Type) && a.Typmod == b.Typmod && a.Collation == b.Collation && SameStatementUsage(a.Type.Element().Usage(), b.Type.Element().Usage())
	}
	if a.Type.ID() == 2249 && a.Type.IsComposite() || b.Type.ID() == 2249 && b.Type.IsComposite() {
		if !a.Type.IsComposite() || !b.Type.IsComposite() || a.Type.ID() != b.Type.ID() || a.Type.CompositeLen() != b.Type.CompositeLen() {
			return false
		}
		for i := range a.Type.CompositeLen() {
			left, right := a.Type.CompositeMember(i), b.Type.CompositeMember(i)
			if left.Attribute != right.Attribute || left.Name != right.Name || !SameStatementUsage(left.Usage, right.Usage) {
				return false
			}
		}
	}
	return a.Type.ID() == b.Type.ID() && a.Typmod == b.Typmod && a.Collation == b.Collation
}

// StatementCatalogType reports a type whose identity belongs to a catalog
// lifetime: user-defined, a domain, or a record or array containing one.
func StatementCatalogType(t *sqltype.Descriptor) bool {
	if t == nil {
		return false
	}
	if t.IsUserDefined() || t.IsDomain() {
		return true
	}
	if t.ID() == 2249 && t.IsComposite() {
		for i := range t.CompositeLen() {
			if StatementCatalogType(t.CompositeMember(i).Usage.Type) {
				return true
			}
		}
	}
	return t.IsArray() && StatementCatalogType(t.Element())
}

// Signature is what the held signature check compares: the public
// parameters and results and the catalog lifetime they were bound in.
type Signature struct {
	Parameters ParameterInfo
	Results    []catalog.Column
	Owner      *catalog.Owner
	Epoch      ids.CatalogEpoch
}

// CheckSignature fails with 0A000 when actual changes the held parameter or
// result signature, or replaces the catalog lifetime of a catalog-owned type.
func CheckSignature(expected, actual Signature) error {
	lifetimeChanged := expected.Owner != nil && (expected.Owner != actual.Owner || expected.Epoch != actual.Epoch)
	// Binding a catalog-owned parameter from a replaced lifetime must fail
	// before exposing or validating its result, matching the existing enum and
	// domain prepared-input fences.
	if lifetimeChanged {
		for _, t := range expected.Parameters.Types {
			if StatementCatalogType(t.Type) {
				return sqlError("0A000", "cached plan must not change parameter type")
			}
		}
	}
	changed := len(expected.Results) != len(actual.Results)
	if !changed {
		for i, c := range expected.Results {
			a := actual.Results[i]
			if c.Name != a.Name || !SameStatementResultUsage(c.Usage, a.Usage) || lifetimeChanged && StatementCatalogType(c.Usage.Type) {
				changed = true
				break
			}
		}
	}
	if changed {
		return sqlError("0A000", "cached plan must not change result type")
	}
	if expected.Parameters.Count != actual.Parameters.Count || len(expected.Parameters.Types) != len(actual.Parameters.Types) {
		return sqlError("0A000", "cached plan must not change parameter type")
	}
	for i, t := range expected.Parameters.Types {
		if !SameStatementUsage(t, actual.Parameters.Types[i]) {
			return sqlError("0A000", "cached plan must not change parameter type")
		}
	}
	return nil
}

// SameStatementResultUsage compares result Usages; public anonymous records
// compare by kind only.
func SameStatementResultUsage(a, b sqltype.Usage) bool {
	record := func(t *sqltype.Descriptor) bool { return t != nil && t.ID() == 2249 && t.IsComposite() }
	if record(a.Type) || record(b.Type) {
		return record(a.Type) && record(b.Type) && a.Typmod == b.Typmod && a.Collation == b.Collation
	}
	if sqltype.OccurrenceRecordArray(a.Type) || sqltype.OccurrenceRecordArray(b.Type) {
		return sqltype.OccurrenceRecordArray(a.Type) && sqltype.OccurrenceRecordArray(b.Type) && a.Typmod == b.Typmod && a.Collation == b.Collation
	}
	return SameStatementUsage(a, b)
}

// CheckLifetime applies the held signature check to a catalog owner
// or epoch replacement of the held description.
func (e Expected) CheckLifetime(owner *catalog.Owner, epoch ids.CatalogEpoch) error {
	expected := Signature{Parameters: e.Parameters, Results: e.Results, Owner: e.CatalogOwner, Epoch: e.CatalogEpoch}
	actual := expected
	actual.Owner, actual.Epoch = owner, epoch
	return CheckSignature(expected, actual)
}

// RefreshableTemporal reports whether a captured temporal constant is
// re-input when a prepared statement refreshes its temporal captures.
func RefreshableTemporal(c TemporalConstant) bool {
	t := c.Value.Type()
	if StatementCatalogType(t) {
		return false // Logical compounds retain even their embedded temporal fields.
	}
	id := c.TypeID
	if t != nil {
		for t.IsArray() {
			t = t.Element()
		}
		id = t.ID()
	}
	switch id {
	case sqltype.Date, sqltype.Time, sqltype.Timetz, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Interval, sqltype.DateRange, sqltype.TimestampRange, sqltype.TimestamptzRange, sqltype.Regrole:
		return true
	}
	return false
}
