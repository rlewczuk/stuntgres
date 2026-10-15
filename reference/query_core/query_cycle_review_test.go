package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceQueryCycleStoredTypedMarks(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := cycleConn(t, p, backend)
			run := subqueryRun(t, p, backend, c)
			var checks []fixture.Step
			for _, tc := range []struct {
				name, clause, anchor, closing string
			}{
				{"cycle_rounding_mark", "TO numeric(3,1) '1.25' DEFAULT numeric '0'", "0", "1.3"},
				{"cycle_rounding_default", "TO numeric '0' DEFAULT numeric(3,1) '1.25'", "1.3", "0"},
			} {
				body := "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n FROM r) CYCLE n SET cyc " + tc.clause + " USING path SELECT n,cyc,path::text AS path FROM r"
				run(fixture.Exec("CREATE VIEW "+tc.name+" AS "+body, 0))
				s := cteBag("SELECT * FROM "+tc.name, []string{"n", "cyc", "path"}, []string{"INT4", "NUMERIC", "TEXT"}, [][]any{{int64(1), tc.anchor, "{(1)}"}, {int64(1), tc.closing, "{(1),(1)}"}})
				s.CheckMetadata = cycleStoredMarkMetadata(backend, s.Columns, s.Types)
				checks = append(checks, s)
				t.Run(tc.name+" initial", func(t *testing.T) {
					recursiveCTERoutes(t, p, backend, c, s)
				})
			}
			if backend.PG {
				return
			}
			dump := fixture.ViewDump(t, p)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			fixture.ViewRestore(t, p, dump)
			t.Run("native restore", func(t *testing.T) {
				for _, s := range checks {
					recursiveCTERoutes(t, p, backend, c, s)
				}
			})
			foreign := fixture.NewFixture(t, false, false)
			fixture.ViewRestore(t, foreign, dump)
			t.Run("foreign restore", func(t *testing.T) {
				for _, fb := range foreign.Backends() {
					fc := cycleConn(t, foreign, fb)
					for _, s := range checks {
						recursiveCTERoutes(t, foreign, fb, fc, s)
					}
				}
			})
			if p.Postgres != nil {
				t.Run("actual emitted SQL on PostgreSQL", func(t *testing.T) {
					defer func() {
						if _, err := p.Postgres.ExecContext(p.Ctx, "DROP VIEW IF EXISTS cycle_rounding_mark,cycle_rounding_default"); err != nil {
							t.Error(err)
						}
					}()
					if _, err := p.Postgres.ExecContext(p.Ctx, string(dump)); err != nil {
						t.Fatal("actual native typed-mark dump PostgreSQL replay", err)
					}
					for _, s := range checks {
						// pq's truthful ScanType differs from native; use the same
						// independent metadata authority with the replay backend.
						s.CheckMetadata = cycleStoredMarkMetadata(fixture.Backend{PG: true}, s.Columns, s.Types)
						fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
						fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(s))
					}
				})
			}
		})
	}
}

func TestReferenceQueryCycleConflictPrecedence(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := cycleConn(t, p, backend)
			q := "/*é*/ WITH RECURSIVE r(p,m) AS (SELECT 1,2 UNION ALL SELECT 1,2 FROM r) CYCLE p SET m USING p SELECT * FROM r"
			want := cycleDiagnostic("42601", `cycle mark column name "m" already used in WITH query column list`, q, "CYCLE")
			cycleAssertDiagnostic(t, p, backend, c, q, want)
		})
	}
}

func TestReferenceQueryCycleViewReplacePrecedence(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := cycleConn(t, p, backend)
			body := "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n FROM r) CYCLE n SET cyc USING path "
			rename := fixture.ControlsReferenceDiagnostic("42P16", `cannot change name of view column "old_col" to "new_col"`)
			rename.Hint = "Use ALTER VIEW ... RENAME COLUMN ... to change name of view column instead."
			for _, tc := range []struct {
				name, projection string
				want             fixture.Diagnostic
			}{
				{"cycle_replace_rename", "SELECT path AS new_col FROM r", rename},
				{"cycle_replace_type", "SELECT path AS old_col FROM r", fixture.ControlsReferenceDiagnostic("42P16", `cannot change data type of view column "old_col" from integer to record[]`)},
				{"cycle_replace_append", "SELECT n AS old_col,path AS appended_path FROM r", fixture.ControlsReferenceDiagnostic("42P16", `column "appended_path" has pseudo-type record[]`)},
			} {
				t.Run(tc.name, func(t *testing.T) {
					run := subqueryRun(t, p, backend, c)
					run(fixture.Exec("CREATE VIEW "+tc.name+" AS SELECT 7 AS old_col", 0))
					original := cteBag("SELECT * FROM "+tc.name, []string{"old_col"}, []string{"INT4"}, [][]any{{int64(7)}})
					original.CheckMetadata = cycleStoredMarkMetadata(backend, original.Columns, original.Types)
					checkOriginal := func(t *testing.T) {
						t.Helper()
						recursiveCTERoutes(t, p, backend, c, original)
					}
					t.Run("original", checkOriginal)
					q := "CREATE OR REPLACE VIEW " + tc.name + " AS " + body + tc.projection
					for _, route := range []string{"query", "exec", "prepare query", "prepare exec"} {
						t.Run(route, func(t *testing.T) {
							// Run preservation even if this route's diagnostic fails.
							defer func() { t.Run("original unchanged", checkOriginal) }()
							var err error
							phase := "execute"
							query := func(stmt *sql.Stmt) {
								var rows *sql.Rows
								if stmt == nil {
									rows, err = c.QueryContext(p.Ctx, q)
								} else {
									rows, err = stmt.QueryContext(p.Ctx)
								}
								if rows != nil {
									rows.Close()
									t.Error("failed replacement published Rows")
								}
							}
							exec := func(stmt *sql.Stmt) {
								var result sql.Result
								if stmt == nil {
									result, err = c.ExecContext(p.Ctx, q)
								} else {
									result, err = stmt.ExecContext(p.Ctx)
								}
								if result != nil {
									t.Error("failed replacement published result")
								}
							}
							switch route {
							case "query":
								query(nil)
							case "exec":
								exec(nil)
							default:
								phase = "prepare"
								var stmt *sql.Stmt
								stmt, err = c.PrepareContext(p.Ctx, q)
								if stmt != nil {
									defer stmt.Close()
									if err != nil {
										t.Error("failed Prepare published statement")
									} else {
										phase = "execute held"
										if route == "prepare query" {
											query(stmt)
										} else {
											exec(stmt)
										}
									}
								} else if err == nil {
									t.Error("successful Prepare returned nil statement")
									return
								}
							}
							if err == nil {
								t.Error("invalid view replacement succeeded")
								return
							}
							got := fixture.ExtractDiagnostic(t, err)
							if !reflect.DeepEqual(got, tc.want) {
								t.Errorf("diagnostic got=%+v independently expected=%+v", got, tc.want)
							}
							wantPhase := "execute"
							if route == "prepare query" || route == "prepare exec" {
								wantPhase = "execute held"
							}
							if phase != wantPhase {
								t.Errorf("phase=%s want=%s", phase, wantPhase)
							}
						})
					}
				})
			}
		})
	}
}
