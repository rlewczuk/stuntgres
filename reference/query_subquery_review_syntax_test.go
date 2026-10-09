package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres"
)

// A physical source used only by aggregate-local ORDER BY must be admitted
// even when an enclosing eligible EXISTS discards the entire nested target.
func TestReferenceQuerySubqueryAggregateOrderSources(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		run(referenceExec("CREATE TABLE a(id INT,label TEXT)", 0))
		run(referenceExec("INSERT INTO a VALUES(1,'a'),(2,'b')", 2))
		run(referenceExec("CREATE TABLE k(id INT)", 0))
		run(referenceExec("INSERT INTO k VALUES(1)", 1))
		run(referenceExec("CREATE TABLE demand_outer(id INT)", 0))
		run(referenceExec("INSERT INTO demand_outer VALUES(1),(2)", 2))

		ordered := "SELECT string_agg(a.label, ',' ORDER BY (SELECT k.id FROM k), a.id) AS s FROM a"
		ignored := "SELECT EXISTS(SELECT (SELECT string_agg(a.label, ',' ORDER BY (SELECT k.id FROM k), a.id) FROM a) FROM demand_outer) AS present"
		for _, step := range []referenceStep{
			referenceSelect(ordered, []string{"s"}, []string{"TEXT"}, [][]any{{"a,b"}}),
			referenceSelect("SELECT d.s FROM ("+ordered+") d", []string{"s"}, []string{"TEXT"}, [][]any{{"a,b"}}),
			referenceSelect(ignored, []string{"present"}, []string{"BOOL"}, [][]any{{true}}),
		} {
			subqueryBoth(run, step)
			stmt, err := c.PrepareContext(p.ctx, step.query)
			if err != nil {
				t.Fatal(err)
			}
			held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: step.query}
			for range 2 {
				referenceRunStep(t, p.ctx, held, b.recorder, step)
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
		subqueryBoth(run, referenceExec(ordered, 1))

		run(referenceExec("CREATE VIEW ordered_source_v AS "+ordered, 0))
		run(referenceExec("CREATE VIEW ignored_order_v AS "+ignored, 0))
		referenceViewCopy(t, p, b, "ordered_source_v", "ordered_source_copy")
		referenceViewCopy(t, p, b, "ignored_order_v", "ignored_order_copy")
		check := func() {
			for _, name := range []string{"ordered_source_v", "ordered_source_copy"} {
				subqueryBoth(run, referenceSelect("SELECT * FROM "+name, []string{"s"}, []string{"TEXT"}, [][]any{{"a,b"}}))
			}
			for _, name := range []string{"ignored_order_v", "ignored_order_copy"} {
				subqueryBoth(run, referenceSelect("SELECT * FROM "+name, []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
			}
		}
		check()
		run(referenceExec("ALTER TABLE k RENAME TO renamed_k", 0))
		run(referenceExec("ALTER TABLE renamed_k RENAME COLUMN id TO renamed_id", 0))
		check()
		// Reparse the actual post-rename deparser output, not a reconstructed SQL body.
		referenceViewCopySQL(t, p, b, "ordered_source_pretty", "SELECT pg_get_viewdef('ordered_source_v'::regclass,TRUE) AS definition")
		subqueryBoth(run, referenceSelect("SELECT * FROM ordered_source_pretty", []string{"s"}, []string{"TEXT"}, [][]any{{"a,b"}}))
		if !b.pg {
			dump := referenceViewDump(t, p)
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			referenceViewRestore(t, p, dump)
			check()
			subqueryBoth(run, referenceSelect("SELECT * FROM ordered_source_pretty", []string{"s"}, []string{"TEXT"}, [][]any{{"a,b"}}))
		}
		// Isolate the ignored graph's dependency: demanded/replayed peers no
		// longer exist, and the only remaining view must still protect k.
		for _, name := range []string{"ordered_source_pretty", "ordered_source_copy", "ordered_source_v", "ignored_order_copy"} {
			run(referenceExec("DROP VIEW "+name, 0))
		}
		dependency := controlsReferenceDiagnostic("2BP01", "cannot drop table renamed_k because other objects depend on it")
		dependency.Detail = "view ignored_order_v depends on table renamed_k"
		dependency.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		for _, prepared := range []bool{false, true} {
			step := referenceErrorDiagnostic("DROP TABLE renamed_k", dependency)
			step.selectRows, step.prepared, step.errorStage = true, prepared, "execute"
			run(step)
		}
		subqueryBoth(run, referenceSelect("SELECT * FROM ignored_order_v", []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
		missing := controlsReferenceDiagnostic("42P01", `relation "absent_order_source" does not exist`)
		missing.Position = "78"
		subqueryAnalysisFailure(run, "SELECT EXISTS(SELECT (SELECT string_agg(a.label, ',' ORDER BY (SELECT 1 FROM absent_order_source), a.id) FROM a) FROM demand_outer) AS present", missing)
	})
}

func TestReferenceQuerySubqueryQualifiedComparisons(t *testing.T) {
	for _, tc := range []struct {
		name, expression string
		value            any
	}{
		{"any", "1 OPERATOR(pg_catalog.=) ANY (SELECT 1)", true},
		{"some", "1 OPERATOR(pg_catalog.=) SOME (SELECT 1)", true},
		{"all", "1 OPERATOR(pg_catalog.=) ALL (SELECT 1)", true},
		{"not-equal-all", "1 OPERATOR(pg_catalog.<>) ALL (SELECT 2)", true},
		{"no-match", "1 OPERATOR(pg_catalog.=) ANY (SELECT 2)", false},
		{"null-any", "1 OPERATOR(pg_catalog.=) ANY (SELECT NULL::INT)", nil},
		{"null-all", "1 OPERATOR(pg_catalog.=) ALL (SELECT NULL::INT)", nil},
		{"empty-any", "NULL::INT OPERATOR(pg_catalog.=) ANY (SELECT 1 WHERE FALSE)", false},
		{"empty-all", "NULL::INT OPERATOR(pg_catalog.=) ALL (SELECT 1 WHERE FALSE)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				run := subqueryRun(t, p, b, c)
				q := "SELECT " + tc.expression + " AS matched"
				step := referenceSelect(q, []string{"matched"}, []string{"BOOL"}, [][]any{{tc.value}})
				subqueryBoth(run, step)
				stmt, err := c.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
				for range 2 {
					referenceRunStep(t, p.ctx, held, b.recorder, step)
				}
				// An empty outer result still publishes the quantified BOOL descriptor.
				subqueryBoth(run, referenceSelect(q+" WHERE FALSE", []string{"matched"}, []string{"BOOL"}, nil))
			})
		})
	}
	t.Run("missing-qualified-provider", func(t *testing.T) {
		subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
			want := controlsReferenceDiagnostic("42883", "operator does not exist: integer public.= integer")
			want.Position = "10"
			want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
			// The common runner checks full diagnostics and nil cursor/result/handle
			// at direct execution and prepared analysis boundaries independently.
			subqueryAnalysisFailure(subqueryRun(t, p, b, c), "SELECT 1 OPERATOR(public.=) ANY (SELECT 1) AS matched", want)
		})
	})
}
