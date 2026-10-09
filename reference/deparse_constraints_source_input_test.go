package reference_test

import "testing"

// The original non-Context handle survives every failed bind; newly prepared
// recorded companions remain in TestReferenceDeparseConstraintsPreparedInput.
func TestReferenceDeparseConstraintsPreparedInputSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			const query = "SELECT pg_get_expr($1,$2,$3)"
			stmt, err := b.db.Prepare(query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, tc := range []struct {
				args                   []any
				code, message, context string
			}{
				{[]any{"x", 0, false}, "0A000", "cannot accept a value of type pg_node_tree", "unnamed portal parameter $1 = '...'"},
				{[]any{"", 0, false}, "0A000", "cannot accept a value of type pg_node_tree", "unnamed portal parameter $1 = ''"},
				{[]any{nil, "bad", false}, "22P02", `invalid input syntax for type oid: "bad"`, "unnamed portal parameter $2 = '...'"},
				{[]any{nil, 0, "bad"}, "22P02", `invalid input syntax for type boolean: "bad"`, "unnamed portal parameter $3 = '...'"},
			} {
				rows, err := stmt.Query(tc.args...)
				if rows != nil {
					rows.Close()
					t.Fatal("failed binding exposed cursor")
				}
				want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Context: tc.context}
				referenceAssertDiagnostic(t, err, &want)
			}
			var value any
			if err := stmt.QueryRow(nil, 0, false).Scan(&value); err != nil || value != nil {
				t.Fatal("same held statement did not recover", value, err)
			}
		})
	}
}
