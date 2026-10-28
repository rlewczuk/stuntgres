package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceFTSConvertDiagnostics(t *testing.T) {
	t.Run("grammar", func(t *testing.T) {
		ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, explicit := range []bool{false, true} {
				defer ftsConvertSetting(t, p, c)()
				prefix := ""
				if explicit {
					prefix = "'english'::regconfig,"
				}
				query := "SELECT to_tsquery(" + prefix + "$1::text) AS normalized"
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				for _, tc := range []struct{ input, code, message string }{
					{"a b", "42601", `syntax error in tsquery: "a b"`},
					{"a &", "42601", `no operand in tsquery: "a &"`},
					{"the &", "42601", `no operand in tsquery: "the &"`},
					{"'the a' &", "42601", `no operand in tsquery: "'the a' &"`},
					{"a\\", "42601", "there is no escaped character: \"a\\\""},
					{"a <16385> b", "22023", "distance in phrase operator must be an integer value between zero and 16384 inclusive"},
				} {
					s := fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic(tc.code, tc.message), tc.input)
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}, b.Recorder, s)
				}
				stmt.Close()
			}
		})
	})
	t.Run("configuration_and_overloads", func(t *testing.T) {
		ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, name := range ftsConvertNames {
				for _, prepared := range []bool{false, true} {
					for _, config := range []string{"0::regconfig", "$1::regconfig", "$1::text::regconfig", "'convert_missing'::regconfig"} {
						d := fixture.ControlsReferenceDiagnostic("42704", `text search configuration "convert_missing" does not exist`)
						var args []any
						stage := "execute"
						switch config {
						case "0::regconfig":
							d = fixture.ControlsReferenceDiagnostic("XX000", "cache lookup failed for text search configuration 0")
						case "$1::regconfig":
							args = []any{"convert_missing"}
							d.Context = "unnamed portal parameter $1 = '...'"
						case "$1::text::regconfig":
							args = []any{"convert_missing"}
						default:
							d.Position = fmt.Sprint(9 + len(name))
							if prepared {
								stage = "prepare"
							}
						}
						s := fixture.ErrorDiagnostic("SELECT "+name+"("+config+",'word'::text)", d, args...)
						s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, stage
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
					for _, args := range []struct{ sql, types string }{{"", ""}, {"true,'word'::text", "boolean, text"}, {"'english'::regconfig,'word'::text,'extra'::text", "regconfig, text, text"}, {"NULL::regconfig,true", "regconfig, boolean"}} {
						d := fixture.ControlsReferenceDiagnostic("42883", "function "+name+"("+args.types+") does not exist")
						d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
						d.Position = "8"
						s := fixture.ErrorDiagnostic("SELECT "+name+"("+args.sql+")", d)
						s.Prepared, s.SelectRows = prepared, true
						if prepared {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
				}
			}
		})
	})
	t.Run("routine_authority", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"convert_actor"})
		ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE ROLE convert_actor", 0))
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, err := c.ExecContext(ctx, "RESET ROLE"); err != nil {
					t.Error(err)
				}
				for _, name := range ftsConvertNames {
					for _, signature := range []string{"text", "regconfig,text"} {
						if _, err := c.ExecContext(ctx, "GRANT EXECUTE ON FUNCTION pg_catalog."+name+"("+signature+") TO PUBLIC"); err != nil {
							t.Error(err)
						}
					}
				}
			}()
			for _, name := range ftsConvertNames {
				for _, explicit := range []bool{false, true} {
					signature, prefix := "text", ""
					if explicit {
						signature, prefix = "regconfig,text", "'english'::regconfig,"
					}
					query := "SELECT pg_catalog." + name + "(" + prefix + "$1::text) AS normalized"
					stmt, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog."+name+"("+signature+") FROM PUBLIC", 0))
					run(fixture.Exec("SET ROLE convert_actor", 0))
					s := fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+name), "Cat")
					s.SelectRows, s.ErrorStage = true, "execute"
					run(s)
					run(fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}, b.Recorder, s)
					run(fixture.Exec("RESET ROLE", 0))
					stmt.Close()
					run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog."+name+"("+signature+") TO PUBLIC", 0))
				}
			}
		})
	})
}
