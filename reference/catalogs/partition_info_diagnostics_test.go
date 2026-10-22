package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferencePartitionInfoDiagnostics(t *testing.T) {
	partitionInfoScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		partitionInfoSeed(t, p, b, c)
		run := func(s fixture.Step) {
			route := "direct"
			if s.Prepared {
				route = "prepared"
			}
			t.Run(s.Query+"/"+route, func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) })
		}
		for _, prepared := range []bool{false, true} {
			steps := []fixture.Step{
				partitionInfoTree("SELECT * FROM pg_partition_tree('pinfo_l'::text)", [][]any{{"pinfo_l", "pinfo_m", true, int64(0)}}),
				fixture.Select("SELECT * FROM pg_partition_ancestors('pinfo_l'::text)", []string{"relid"}, []string{"REGCLASS"}, [][]any{{"pinfo_l"}, {"pinfo_m"}, {"pinfo_r"}}),
				fixture.Select("SELECT pg_partition_root('pinfo_l'::text)", []string{"pg_partition_root"}, []string{"REGCLASS"}, [][]any{{"pinfo_r"}}),
			}
			for _, s := range steps {
				s.Prepared = prepared
				s.Bag = true
				s.CheckMetadata = partitionInfoMetadata(b.PG, false)
				run(s)
			}
		}
		for _, name := range []string{"pg_partition_tree", "pg_partition_ancestors", "pg_partition_root"} {
			for _, tc := range []struct{ tail, code, message, hint string }{
				{"(VARIADIC ARRAY[0::regclass])", "42883", "function " + name + "(regclass[]) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts."},
				{"(DISTINCT 0::regclass)", "42809", "DISTINCT specified, but " + name + " is not an aggregate function", ""},
				{"(0::regclass ORDER BY 1)", "42809", "ORDER BY specified, but " + name + " is not an aggregate function", ""},
				{"(0::regclass) FILTER(WHERE true)", "42809", "FILTER specified, but " + name + " is not an aggregate function", ""},
				{"(0::regclass) OVER()", "42809", "OVER specified, but " + name + " is not a window function nor an aggregate function", ""},
			} {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Hint = tc.hint
				d.Position = "8"
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("SELECT "+name+tc.tail, d)
					s.SelectRows = true
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
				}
			}
			for _, tc := range []struct{ input, code, message string }{
				{"bad..name", "42602", "invalid name syntax"},
				{"4294967296", "22003", `value "4294967296" is out of range for type oid`},
			} {
				q := "SELECT " + name + "('" + tc.input + "'::regclass)"
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Position = fixture.Position(utf8.RuneCountInString("SELECT "+name+"(") + 1)
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, d)
					s.SelectRows = true
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
				}
			}
			for _, tc := range []struct{ args, signature string }{
				{"", ""}, {"0::regclass,0::regclass", "regclass, regclass"}, {"true", "boolean"},
				{"wrong => 0::regclass", "wrong => regclass"}, {"\"Rootrelid\" => 0::regclass", "Rootrelid => regclass"},
			} {
				for _, prefix := range []string{"", "/*é🙂*/ "} {
					q := prefix + "SELECT " + name + "(" + tc.args + ")"
					d := fixture.ControlsReferenceDiagnostic("42883", "function "+name+"("+tc.signature+") does not exist")
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					d.Position = fixture.Position(utf8.RuneCountInString(prefix) + 8)
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(q, d)
						s.SelectRows = true
						s.Prepared = prepared
						s.ErrorStage = "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						run(s)
					}
				}
			}
		}
		for _, q := range []string{"SELECT pg_partition_rooot(0::regclass)", "SELECT public.pg_partition_tree(0::regclass)", "SELECT pg_partition_root(rootrelid => 0::regclass)"} {
			signature := strings.TrimPrefix(q, "SELECT ")
			signature = strings.ReplaceAll(signature, "0::regclass", "regclass")
			d := fixture.ControlsReferenceDiagnostic("42883", "function "+signature+" does not exist")
			d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			d.Position = "8"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, d)
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
			}
		}
		for _, name := range []string{"pg_partition_tree", "pg_partition_ancestors", "pg_partition_root"} {
			q := "SELECT " + name + "('pinfo_missing'::regclass)"
			d := fixture.ControlsReferenceDiagnostic("42P01", `relation "pinfo_missing" does not exist`)
			d.Position = fixture.Position(utf8.RuneCountInString("SELECT "+name+"(") + 1)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, d)
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
			}
			q = "SELECT " + name + "($1::regclass)"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			d.Position = ""
			d.Context = "unnamed portal parameter $1 = '...'"
			s := fixture.ErrorDiagnostic(q, d, "pinfo_missing")
			s.SelectRows = true
			s.ErrorStage = "execute"
			t.Run(name+"/held-missing-input", func(t *testing.T) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) })
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}
