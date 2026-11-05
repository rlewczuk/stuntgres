package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func intarraySortDefaults(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ia_sort_rows(id,a) VALUES (4,'{9,2,2}')", 1))
	intarrayBoth(t, p, b, c, intarraySelect(b, "SELECT d,u FROM ia_sort_rows WHERE id=4", []string{"d", "u"}, []string{"_INT4", "_INT4"}, []bool{true, true}, [][]any{{"{3,2,1,1}", "{1,2,3}"}}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM ia_sort_rows WHERE id=4", 1))
	for _, tc := range []struct {
		query      string
		diagnostic fixture.Diagnostic
	}{
		{"INSERT INTO ia_sort_rows(id,a) VALUES (10,'{2,1}'),(11,'{NULL}')", intarraySetNullError()},
		{"INSERT INTO ia_sort_rows(id,a) VALUES (10,'{NULL}'),(11,'{2,1}')", intarraySetNullError()},
		{"INSERT INTO ia_sort_rows(id,a,direction) VALUES (10,'{2,1}','ASC'),(11,'{2,1}','invalid')", intarraySortDirectionError()},
		{"INSERT INTO ia_sort_rows(id,a,direction) VALUES (10,'{2,1}','invalid'),(11,'{2,1}','ASC')", intarraySortDirectionError()},
	} {
		for _, prepared := range []bool{false, true} {
			intarrayError(t, p, b, c, tc.query, prepared, "execute", tc.diagnostic)
			intarrayBoth(t, p, b, c, intarraySortStoredCheck(b))
		}
	}
}

func intarraySortPresentation(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	var held []fixture.HeldStatement
	for _, column := range []string{"sorted", "directed", "ascending", "descending", "adjacent"} {
		q := "SELECT " + column + " AS v FROM ia_sort_view WHERE id=$1::integer"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held = append(held, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q})
	}
	check := func() {
		intarrayBoth(t, p, b, c, intarraySortStoredCheck(b))
		for i, h := range held {
			s := intarraySelect(b, h.SQL, []string{"v"}, []string{"_INT4"}, []bool{true}, [][]any{{intarraySortCases[i].want}}, int64(1))
			fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
		}
	}
	check()
	reader, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	old := intarraySortRetain(t, p, b, reader)
	defer old.rows.Close()
	run := func(q string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
	run("CREATE SCHEMA ia_collision")
	for _, tc := range intarraySortCases {
		run("ALTER FUNCTION ia_provider." + tc.name + "(" + strings.Join(tc.types, ",") + ") RENAME TO renamed_" + tc.label)
		check()
	}
	run("ALTER EXTENSION intarray SET SCHEMA ia_collision")
	check()
	intarraySortConsume(t, b, old)
	for _, tc := range intarraySortCases {
		q := strings.Replace(tc.expression(tc.literals(tc.args)), "ia_provider."+tc.name, "ia_collision.renamed_"+tc.label, 1)
		intarrayBoth(t, p, b, c, intarraySortResult(b, "SELECT "+q+" AS v", tc.want))
	}
	intarraySortDefaults(t, p, b, c)
	run("ALTER EXTENSION intarray SET SCHEMA ia_provider")
	for _, tc := range intarraySortCases {
		run("ALTER FUNCTION ia_provider.renamed_" + tc.label + "(" + strings.Join(tc.types, ",") + ") RENAME TO " + tc.name)
	}
	check()
}

func TestReferenceIntarraySortLifecycle(t *testing.T) {
	t.Run("stored_and_presentation", func(t *testing.T) {
		intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			intarraySortSeed(t, p, b, c)
			intarraySortDefaults(t, p, b, c)
			intarraySortPresentation(t, p, b, c)
		})
	})
	t.Run("rollback_and_detached_originals", func(t *testing.T) {
		intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			intarraySortSeed(t, p, b, c)
			run := func(q string) { fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0)) }
			ids := intarraySortCatalog(t, p, b, c, intarrayFixtureOwner(b))
			run("CREATE SCHEMA ia_collision")
			run("BEGIN")
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run("ALTER FUNCTION ia_provider.sort(integer[],text) RENAME TO rolled_back_sort")
			run("ALTER EXTENSION intarray SET SCHEMA ia_collision")
			intarrayBoth(t, p, b, c, intarraySortStoredCheck(b))
			run("DROP EXTENSION intarray CASCADE")
			run("ROLLBACK")
			intarrayBoth(t, p, b, c, intarraySortStoredCheck(b))
			for i, tc := range intarraySortCases {
				q := "SELECT 'ia_provider." + tc.name + "(" + strings.Join(tc.types, ",") + ")'::regprocedure::oid=$1::oid AS original"
				intarrayBoth(t, p, b, c, fixture.Select(q, []string{"original"}, []string{"BOOL"}, [][]any{{true}}, ids[i]))
			}
			for _, tc := range intarraySortCases {
				signature := "(" + strings.Join(tc.types, ",") + ")"
				run("ALTER EXTENSION intarray DROP FUNCTION ia_provider." + tc.name + signature)
				run("ALTER FUNCTION ia_provider." + tc.name + signature + " RENAME TO original_" + tc.label)
				run("ALTER FUNCTION ia_provider.original_" + tc.label + signature + " SET SCHEMA ia_collision")
			}
			run("DROP EXTENSION intarray CASCADE")
			intarrayBoth(t, p, b, c, intarraySortStoredCheck(b))
			intarraySortDefaults(t, p, b, c)
			run("CREATE EXTENSION intarray SCHEMA ia_provider VERSION '1.5'")
			intarrayBoth(t, p, b, c, intarraySortStoredCheck(b))
			intarraySortDefaults(t, p, b, c)
			for i, tc := range intarraySortCases {
				signature := "(" + strings.Join(tc.types, ",") + ")"
				q := "SELECT 'ia_collision.original_" + tc.label + signature + "'::regprocedure::oid=$1::oid AS original,'ia_provider." + tc.name + signature + "'::regprocedure::oid<>$1::oid AS distinct_installation"
				intarrayBoth(t, p, b, c, fixture.Select(q, []string{"original", "distinct_installation"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, ids[i]))
				expression := strings.Replace(tc.expression(tc.literals(tc.args)), "ia_provider."+tc.name, "ia_collision.original_"+tc.label, 1)
				intarrayBoth(t, p, b, c, intarraySortResult(b, "SELECT "+expression+" AS v", tc.want))
				intarrayBoth(t, p, b, c, intarraySortResult(b, "SELECT "+tc.expression(tc.literals(tc.args))+" AS v", tc.want))
			}
			// A STRICT NULL cannot rescue retired original authority from the new
			// installation's same-name function. Stored dependents disappear too.
			q := "SELECT ia_collision.original_sort_binary($1::integer[],$2::text) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, intarraySortResult(b, q, nil, nil, "ASC"))
			run("DROP FUNCTION ia_collision.original_sort_binary(integer[],text) CASCADE")
			want := fixture.ControlsReferenceDiagnostic("42883", "function ia_collision.original_sort_binary(integer[], text) does not exist")
			want.Hint, want.Position = "No function matches the given name and argument types. You might need to add explicit type casts.", "8"
			failure := fixture.ErrorDiagnostic(q, want, nil, "ASC")
			failure.SelectRows, failure.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, failure)
			intarrayBoth(t, p, b, c, fixture.Select("SELECT relname FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='ia_sort_view'", []string{"relname"}, []string{"NAME"}, [][]any{}))
			intarrayBoth(t, p, b, c, intarraySortResult(b, "SELECT ia_provider.sort('{2,1,2}'::integer[],'DESC') AS v", "{2,2,1}"))
			run("DROP EXTENSION intarray CASCADE")
			intarrayBoth(t, p, b, c, intarraySelect(b, "SELECT id,a FROM ia_sort_rows ORDER BY id", []string{"id", "a"}, []string{"INT4", "_INT4"}, []bool{false, true}, [][]any{{int64(1), intarraySortA}, {int64(2), "{}"}, {int64(3), nil}}))
		})
	})
}
