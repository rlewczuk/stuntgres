package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceDeparsePartitionResolutionDiagnostics(t *testing.T) {
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE dp_resolution(k INT)", 0))
		for _, name := range []string{"pg_get_partkeydef", "pg_get_partition_constraintdef"} {
			for _, tc := range []struct{ call, signature string }{
				{name + "()", name + "()"},
				{name + "(0,true)", name + "(integer, boolean)"},
				{name + "(true)", name + "(boolean)"},
				{name + "('name'::text)", name + "(text)"},
				{name + "(NULL,true)", name + "(unknown, boolean)"},
				{name + "(\"table\" => NULL::oid)", name + "(table => oid)"},
				{"public." + name + "(0::oid)", "public." + name + "(oid)"},
				{`"` + strings.ToUpper(name) + `"(0::oid)`, strings.ToUpper(name) + "(oid)"},
			} {
				w := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
				w.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				w.Position = "8"
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("SELECT "+tc.call, w)
					s.SelectRows = true
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					run(s)
				}
			}
			for _, tc := range []struct{ query, code, message, marker string }{
				{"SELECT " + name + "('bad')", "22P02", `invalid input syntax for type oid: "bad"`, "'bad'"},
				{"SELECT " + name + "('4294967296')", "22003", `value "4294967296" is out of range for type oid`, "'4294967296'"},
				{"SELECT 'été', " + name + "('bad')", "22P02", `invalid input syntax for type oid: "bad"`, "'bad'"},
				{"SELECT absent_schema." + name + "(NULL)", "3F000", `schema "absent_schema" does not exist`, "absent_schema"},
				{"SELECT " + name + "('dp_absent'::regclass)", "42P01", `relation "dp_absent" does not exist`, "'dp_absent'"},
				{"SELECT " + name + "(missing) FROM dp_resolution", "42703", `column "missing" does not exist`, "missing"},
				{"SELECT " + name + "(NULL::oid,missing) FROM dp_resolution", "42703", `column "missing" does not exist`, "missing"},
			} {
				w := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				w.Position = fixture.Position(utf8.RuneCountInString(tc.query[:strings.Index(tc.query, tc.marker)]) + 1)
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(tc.query, w)
					s.SelectRows = true
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					run(s)
				}
			}
			for _, tc := range []struct{ value, code, message string }{
				{"bad", "22P02", `invalid input syntax for type oid: "bad"`},
				{"4294967296", "22003", `value "4294967296" is out of range for type oid`},
			} {
				q := "SELECT " + name + "($1)"
				w := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				w.Context = "unnamed portal parameter $1 = '...'"
				s := fixture.ErrorDiagnostic(q, w, tc.value)
				s.SelectRows = true
				run(s)
				held, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("parameter must prepare before conversion", err)
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, s)
				if err := held.Close(); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
}

func TestReferenceDeparsePartitionMetadata(t *testing.T) {
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE dp_metadata(k INT) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE dp_metadata_leaf PARTITION OF dp_metadata FOR VALUES FROM(0) TO(10)", 0))
		for _, tc := range []struct{ function, relation, want string }{
			{"pg_get_partkeydef", "dp_metadata", "RANGE (k)"},
			{"pg_get_partition_constraintdef", "dp_metadata_leaf", "((k IS NOT NULL) AND (k >= 0) AND (k < 10))"},
		} {
			id := fixture.DeparseDefinitionID(t, c, "SELECT '"+tc.relation+"'::regclass::oid")
			run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_catalog."+tc.function+"('"+tc.relation+"'::regclass) AS definition", tc.want))
			for _, operand := range []string{"'" + tc.relation + "'::regclass", "$1", "$1::oid"} {
				args := []any(nil)
				if strings.Contains(operand, "$1") {
					args = []any{id}
				}
				q := "SELECT " + tc.function + "(" + operand + ") AS definition"
				for _, empty := range []bool{false, true} {
					query := q
					if empty {
						query += " WHERE FALSE"
					}
					s := fixture.DeparseRelationsTextStep(t, b, query, tc.want, args...)
					if empty {
						s.Rows = nil
					}
					run(s)
					held, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					e := fixture.HeldStatement{Executor: c, Statement: held, SQL: query}
					fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
					if len(args) > 0 {
						s.Args = []any{nil}
						if !empty {
							s.Rows = [][]any{{nil}}
						}
						fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
					}
					if err := held.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		run(fixture.Exec("CREATE VIEW dp_metadata_view AS SELECT pg_get_partkeydef('dp_metadata'::regclass) AS keydef,pg_get_partition_constraintdef('dp_metadata_leaf'::regclass) AS constraintdef", 0))
		run(fixture.Select("SELECT keydef,constraintdef FROM dp_metadata_view", []string{"keydef", "constraintdef"}, []string{"TEXT", "TEXT"}, [][]any{{"RANGE (k)", "((k IS NOT NULL) AND (k >= 0) AND (k < 10))"}}))
		for _, tc := range []struct{ column, want string }{{"keydef", "RANGE (k)"}, {"constraintdef", "((k IS NOT NULL) AND (k >= 0) AND (k < 10))"}} {
			s := fixture.DeparseRelationsTextStep(t, b, "SELECT "+tc.column+" AS definition FROM dp_metadata_view", tc.want)
			run(s)
			run(fixture.Prepared(s))
		}
	})
}
