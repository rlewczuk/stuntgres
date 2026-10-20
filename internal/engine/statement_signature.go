package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func sameStatementUsage(a, b sqltype.Usage) bool {
	if a.Type == nil || b.Type == nil {
		return a.Type == b.Type && a.Typmod == b.Typmod && a.Collation == b.Collation
	}
	// Parameter contracts retain exact occurrence shape, unlike public results.
	if sqltype.OccurrenceRecordArray(a.Type) || sqltype.OccurrenceRecordArray(b.Type) {
		return sqltype.OccurrenceRecordArray(a.Type) && sqltype.OccurrenceRecordArray(b.Type) && a.Typmod == b.Typmod && a.Collation == b.Collation && sameStatementUsage(a.Type.Element().Usage(), b.Type.Element().Usage())
	}
	if a.Type.ID() == 2249 && a.Type.IsComposite() || b.Type.ID() == 2249 && b.Type.IsComposite() {
		if !a.Type.IsComposite() || !b.Type.IsComposite() || a.Type.ID() != b.Type.ID() || a.Type.CompositeLen() != b.Type.CompositeLen() {
			return false
		}
		for i := range a.Type.CompositeLen() {
			left, right := a.Type.CompositeMember(i), b.Type.CompositeMember(i)
			if left.Attribute != right.Attribute || left.Name != right.Name || !sameStatementUsage(left.Usage, right.Usage) {
				return false
			}
		}
	}
	return a.Type.ID() == b.Type.ID() && a.Typmod == b.Typmod && a.Collation == b.Collation
}

func statementCatalogType(t *sqltype.Descriptor) bool {
	if t == nil {
		return false
	}
	if t.IsUserDefined() || t.IsDomain() {
		return true
	}
	if t.ID() == 2249 && t.IsComposite() {
		for i := range t.CompositeLen() {
			if statementCatalogType(t.CompositeMember(i).Usage.Type) {
				return true
			}
		}
	}
	return t.IsArray() && statementCatalogType(t.Element())
}

// statementSignature is what the held signature check compares: the public
// parameters and results and the catalog lifetime they were bound in.
type statementSignature struct {
	parameters ParameterInfo
	results    []catalog.Column
	owner      *catalog.Owner
	epoch      ids.CatalogEpoch
}

func signatureOf(d StatementDescription) statementSignature {
	return statementSignature{parameters: d.Parameters, results: d.Results, owner: d.catalogOwner, epoch: d.catalogEpoch}
}

func checkStatementSignature(expected, actual StatementDescription) error {
	return checkSignature(signatureOf(expected), signatureOf(actual))
}

func checkSignature(expected, actual statementSignature) error {
	lifetimeChanged := expected.owner != nil && (expected.owner != actual.owner || expected.epoch != actual.epoch)
	// Binding a catalog-owned parameter from a replaced lifetime must fail
	// before exposing or validating its result, matching the existing enum and
	// domain prepared-input fences.
	if lifetimeChanged {
		for _, t := range expected.parameters.Types {
			if statementCatalogType(t.Type) {
				return sqlError("0A000", "cached plan must not change parameter type")
			}
		}
	}
	changed := len(expected.results) != len(actual.results)
	if !changed {
		for i, c := range expected.results {
			a := actual.results[i]
			if c.Name != a.Name || !sameStatementResultUsage(c.Usage, a.Usage) || lifetimeChanged && statementCatalogType(c.Usage.Type) {
				changed = true
				break
			}
		}
	}
	if changed {
		return sqlError("0A000", "cached plan must not change result type")
	}
	if expected.parameters.Count != actual.parameters.Count || len(expected.parameters.Types) != len(actual.parameters.Types) {
		return sqlError("0A000", "cached plan must not change parameter type")
	}
	for i, t := range expected.parameters.Types {
		if !sameStatementUsage(t, actual.parameters.Types[i]) {
			return sqlError("0A000", "cached plan must not change parameter type")
		}
	}
	return nil
}

func sameStatementResultUsage(a, b sqltype.Usage) bool {
	record := func(t *sqltype.Descriptor) bool { return t != nil && t.ID() == 2249 && t.IsComposite() }
	if record(a.Type) || record(b.Type) {
		return record(a.Type) && record(b.Type) && a.Typmod == b.Typmod && a.Collation == b.Collation
	}
	if sqltype.OccurrenceRecordArray(a.Type) || sqltype.OccurrenceRecordArray(b.Type) {
		return sqltype.OccurrenceRecordArray(a.Type) && sqltype.OccurrenceRecordArray(b.Type) && a.Typmod == b.Typmod && a.Collation == b.Collation
	}
	return sameStatementUsage(a, b)
}
