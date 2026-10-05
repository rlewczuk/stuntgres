package engine

import "stuntgres/internal/sqltype"

func sameStatementUsage(a, b sqltype.Usage) bool {
	if a.Type == nil || b.Type == nil {
		return a.Type == b.Type && a.Typmod == b.Typmod && a.Collation == b.Collation
	}
	return a.Type.ID() == b.Type.ID() && a.Typmod == b.Typmod && a.Collation == b.Collation
}

func checkStatementSignature(expected, actual StatementDescription) error {
	changed := len(expected.Results) != len(actual.Results)
	if !changed {
		for i, c := range expected.Results {
			a := actual.Results[i]
			if c.Name != a.Name || !sameStatementUsage(c.Usage, a.Usage) {
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
