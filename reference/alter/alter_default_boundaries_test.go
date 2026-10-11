package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceAlterDefaultBoundaries(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(strconv.FormatBool(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			run := func(s fixture.Step) { s.Prepared = prepared; p.Step(t, s) }
			run(fixture.Exec(`CREATE TABLE "ét"(i INT DEFAULT 7)`, 0))
			for _, q := range []string{`ALTER TABLE "ét" ALTER i SET DEFAULT`, `ALTER TABLE "ét" ALTER i SET DEFAULT 9 garbage`} {
				want := fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: strconv.Itoa(utf8.RuneCountInString(q) + 1)}
				if strings.HasSuffix(q, "garbage") {
					want.Message = `syntax error at or near "garbage"`
					want.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "garbage")]) + 1)
				}
				s := fixture.ImplicitError(q, want)
				if prepared {
					s.ErrorStage = "prepare"
				} else {
					s.ErrorStage = "execute"
				}
				run(s)
			}
			run(fixture.Exec(`INSERT INTO "ét" DEFAULT VALUES`, 1))
			run(fixture.Select(`SELECT * FROM "ét"`, []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
			for _, tc := range []struct{ q, token, code, message string }{{"CREATE TABLE bad(i INT DEFAULT i)", "i)", "0A000", "cannot use column reference in DEFAULT expression"}, {"CREATE TABLE bad(i INT DEFAULT $1)", "$1", "42P02", "there is no parameter $1"}} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: strconv.Itoa(strings.Index(tc.q, tc.token) + 1)})
				s.ErrorStage = "execute"
				run(s)
			}
			for _, tc := range []struct{ expr, token string }{{"TRUE OR FALSE", "OR"}, {"NOT FALSE", "NOT"}, {"NULL IS NULL", "NULL"}} {
				q := "CREATE TABLE bad(b BOOL DEFAULT " + tc.expr + ")"
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "` + tc.token + `"`, Position: strconv.Itoa(strings.LastIndex(q, tc.token) + 1)})
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
				run(fixture.Exec("CREATE TABLE good(b BOOL DEFAULT ("+tc.expr+"))", 0))
				run(fixture.Exec("INSERT INTO good DEFAULT VALUES", 1))
				run(fixture.Select("SELECT * FROM good", []string{"b"}, []string{"BOOL"}, [][]any{{true}}))
				run(fixture.Exec("DROP TABLE good", 0))
			}
		})
	}
}

func TestReferenceAlterDefaultEmptyDemand(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(strconv.FormatBool(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			run := func(s fixture.Step) { s.Prepared = prepared; p.Step(t, s) }
			run(fixture.Exec("CREATE TABLE t(id INT,i INT DEFAULT 7)", 0))
			run(fixture.Exec("CREATE TABLE src(id INT)", 0))
			run(fixture.Exec("ALTER TABLE t ALTER i SET DEFAULT 2147483648", 0))
			s := fixture.ImplicitError("INSERT INTO t(id) SELECT id FROM src", fixture.Diagnostic{Code: "22003", Message: "integer out of range"})
			s.ErrorStage = "execute"
			run(s)
			run(fixture.Exec("INSERT INTO t(id,i) SELECT id,99 FROM src", 0))
			run(fixture.Exec("INSERT INTO t VALUES(0,99),(1,NULL)", 2))
			run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "i"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(99)}, {int64(1), nil}}))
		})
	}
}

func TestReferenceAlterDefaultEligibility(t *testing.T) {
	for _, readonly := range []bool{false, true} {
		for _, action := range []string{"SET DEFAULT 7", "DROP DEFAULT"} {
			t.Run(strconv.FormatBool(readonly)+action, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
						run(fixture.Exec("CREATE TABLE t(i INT DEFAULT 11)", 0))
						tx, err := backend.DB.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: readonly})
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						q := "ALTER TABLE t ALTER i " + action
						if backend.PG && !readonly {
							fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec(q, 0))
						} else {
							want := fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}
							if readonly {
								want = fixture.Diagnostic{Code: "25006", Message: "cannot execute ALTER TABLE in a read-only transaction"}
							}
							s := fixture.ImplicitError(q, want)
							s.ErrorStage = "execute"
							fixture.RunStep(t, p.Ctx, tx, backend.Recorder, s)
						}
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						run(fixture.Exec("INSERT INTO t DEFAULT VALUES", 1))
						run(fixture.Select("SELECT * FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(11)}}))
					})
				}
			})
		}
	}
}
