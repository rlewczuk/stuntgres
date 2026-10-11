package engine

import "stuntgres/internal/sqltype"

func sameStatementUsage(a, b sqltype.Usage) bool {
	if a.Type == nil || b.Type == nil {
		return a.Type == b.Type && a.Typmod == b.Typmod && a.Collation == b.Collation
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
	return t.IsArray() && statementCatalogType(t.Element())
}

func checkStatementSignature(expected, actual StatementDescription) error {
	lifetimeChanged := expected.catalogOwner != nil && (expected.catalogOwner != actual.catalogOwner || expected.catalogEpoch != actual.catalogEpoch)
	// Binding a catalog-owned parameter from a replaced lifetime must fail
	// before exposing or validating its result, matching the existing enum and
	// domain prepared-input fences.
	if lifetimeChanged {
		for _, t := range expected.Parameters.Types {
			if statementCatalogType(t.Type) {
				return sqlError("0A000", "cached plan must not change parameter type")
			}
		}
	}
	changed := len(expected.Results) != len(actual.Results)
	if !changed {
		for i, c := range expected.Results {
			a := actual.Results[i]
			if c.Name != a.Name || !sameStatementUsage(c.Usage, a.Usage) || lifetimeChanged && statementCatalogType(c.Usage.Type) {
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
		if !sameStatementUsage(t, actual.Parameters.Types[i]) {
			return sqlError("0A000", "cached plan must not change parameter type")
		}
	}
	return nil
}
