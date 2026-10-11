package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceInsertConflictPredicateSuffix(t *testing.T) {
	for _, source := range []string{
		"SELECT k FROM s WHERE active ON CONFLICT(k) DO NOTHING",
		"SELECT k FROM s WHERE active ON /* outer /* nested */ comment */ CONFLICT(k) DO NOTHING",
		"(SELECT k FROM s WHERE active) ON CONFLICT(k) DO NOTHING",
		"SELECT s.k FROM s JOIN other ON conflict WHERE active ON CONFLICT(k) DO NOTHING",
	} {
		t.Run(source, func(t *testing.T) {
			insertConflictSteps(t,
				fixture.Exec("CREATE TABLE s(k INT,active BOOL)", 0), fixture.Exec("INSERT INTO s VALUES(1,TRUE),(2,TRUE),(3,FALSE)", 3),
				fixture.Exec("CREATE TABLE other(conflict BOOL)", 0), fixture.Exec("INSERT INTO other VALUES(TRUE)", 1),
				fixture.Exec("CREATE TABLE t(k INT UNIQUE)", 0), fixture.Exec("INSERT INTO t VALUES(1)", 1),
				fixture.Exec("INSERT INTO t "+source, 1), insertConflictRows("SELECT k FROM t ORDER BY k", []any{int64(1)}, []any{int64(2)}))
		})
	}
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE s(k INT,active BOOL)", 0))
				run(fixture.Exec("INSERT INTO s VALUES(1,TRUE),(2,TRUE)", 2))
				run(fixture.Exec("CREATE TABLE t(k INT UNIQUE)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
				position := func(q, token string) string {
					return fixture.Position(utf8.RuneCountInString(q[:strings.LastIndex(q, token)]) + 1)
				}
				for _, query := range []string{"SELECT k FROM s WHERE active ON CONFLICT", "INSERT INTO t VALUES(9) ON CONFLICT DO NOTHING; SELECT k FROM s WHERE active ON CONFLICT"} {
					diagnostic := fixture.ControlsReferenceDiagnostic("0A000", "predicate syntax is unsupported")
					diagnostic.Position = position(query, "ON CONFLICT")
					if b.PG {
						diagnostic = fixture.ControlsReferenceDiagnostic("42601", "syntax error at or near \"ON\"")
						diagnostic.Position = position(query, "ON CONFLICT")
					}
					step := fixture.ErrorDiagnostic(query, diagnostic)
					step.Prepared = prepared
					step.ErrorStage = "execute"
					if prepared {
						step.ErrorStage = "prepare"
					}
					run(step)
					run(insertConflictRows("SELECT k FROM t", []any{int64(1)}))
				}
				query := "INSERT INTO t VALUES(1) ON \"CONFLICT\" DO NOTHING"
				diagnostic := fixture.ControlsReferenceDiagnostic("0A000", "INSERT suffix is unsupported")
				diagnostic.Position = position(query, "ON ")
				if b.PG {
					diagnostic = fixture.ControlsReferenceDiagnostic("42601", "syntax error at or near \"\"CONFLICT\"\"")
					diagnostic.Position = position(query, "\"CONFLICT\"")
				}
				step := fixture.ErrorDiagnostic(query, diagnostic)
				step.Prepared = prepared
				step.ErrorStage = "execute"
				if prepared {
					step.ErrorStage = "prepare"
				}
				run(step)
				run(insertConflictRows("SELECT k FROM t", []any{int64(1)}))
				if !prepared {
					script := "INSERT INTO t SELECT k FROM s WHERE active ON CONFLICT DO NOTHING; SELECT k FROM s WHERE active"
					if b.PG {
						if _, err := b.DB.ExecContext(p.Ctx, script); err != nil {
							t.Fatal(err)
						}
						run(insertConflictRows("SELECT k FROM t ORDER BY k", []any{int64(1)}, []any{int64(2)}))
					} else {
						boundary := fixture.ErrorDiagnostic(script, fixture.ControlsReferenceDiagnostic("0A000", "multiple statements are unsupported"))
						boundary.ErrorStage = "execute"
						run(boundary)
						run(insertConflictRows("SELECT k FROM t", []any{int64(1)}))
					}
				}
			})
		})
	}
}
