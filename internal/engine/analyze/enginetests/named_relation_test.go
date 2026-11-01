package enginetests

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestNamedRelationLockAdmission(t *testing.T) {
	lookup := func(name string) (plan.NamedRelation, bool) { return plan.NamedRelation{Name: name}, name == "x" }
	for _, tc := range []struct {
		query      string
		named      int
		diagnostic bool
	}{
		{"SELECT * FROM x AS a JOIN t AS b ON true FOR UPDATE", 1, false},
		{"SELECT * FROM x AS a JOIN t AS b ON true FOR UPDATE OF a", 1, true},
		{"SELECT * FROM public.x FOR UPDATE", 0, false},
		{"WITH x AS(SELECT 1 AS id) SELECT * FROM x", 0, false},
	} {
		t.Run(tc.query, func(t *testing.T) {
			statements, err := parser.Parse(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			admission, tables, _, err := analyze.NewLockAdmission(statements[0], lookup)
			if err != nil {
				t.Fatal(err)
			}
			if len(admission.NamedRelations) != tc.named {
				t.Fatal("lexical CTE/schema precedence lost", admission.NamedRelations)
			}
			var failure error
			for _, query := range admission.Ordered {
				if query.Err != nil {
					failure = query.Err
					break
				}
			}
			if tc.diagnostic {
				var diagnostic *sqlstate.Error
				if !errors.As(failure, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "FOR UPDATE cannot be applied to a named tuplestore" || diagnostic.Position == "" {
					t.Fatal("explicit named target lost diagnostic/cursor", failure)
				}
				return
			}
			if failure != nil {
				t.Fatal(failure)
			}
			for _, table := range tables {
				mark := admission.TableMark(table)
				if admission.NamedRelations[table] && mark.Mode != 0 {
					t.Fatal("blanket row lock marked invocation data")
				}
				if table.Name.Parts[len(table.Name.Parts)-1].Text == "t" && mark.Mode != lock.ForUpdate {
					t.Fatal("named source suppressed sibling physical lock", mark)
				}
			}
		})
	}
}
