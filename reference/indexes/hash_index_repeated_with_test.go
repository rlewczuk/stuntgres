package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceHashIndexRepeatedWithDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, query, index string
		existing           bool
	}{
		{"valid-first", "CREATE INDEX retry ON t USING hash(n) WITH(fillfactor=75) WITH(fillfactor=80)", "retry", false},
		{"invalid-first", "CREATE INDEX retry ON t USING hash(n) WITH(fillfactor=9) WITH(fillfactor=75)", "retry", false},
		{"existing-if-not-exists", "CREATE INDEX IF NOT EXISTS stable ON t USING hash(n) WITH(fillfactor=9) WITH(fillfactor=75)", "stable", true},
		{"unicode", "/*é*/ CREATE INDEX retry ON t USING hash(n) WITH(fillfactor=75) WITH(fillfactor=80)", "retry", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, plain := range []bool{false, true} {
				t.Run(fmt.Sprintf("plain=%v", plain), func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							var notices []fixture.Diagnostic
							db, _, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
							run := func(s fixture.Step) {
								s.NonContext = plain
								fixture.RunStep(t, p.Ctx, db, recorder, s)
							}
							hashIndexSeed(run)
							run(fixture.Exec("CREATE INDEX stable ON t USING hash(n)", 0))
							selectSQL := "SELECT n,s FROM t WHERE n=$1 ORDER BY s"
							updateSQL := "UPDATE t SET s=$1 WHERE n=$2"
							prepare := func(q string) (*sql.Stmt, error) {
								if plain {
									return db.Prepare(q)
								}
								return db.PrepareContext(p.Ctx, q)
							}
							selected, err := prepare(selectSQL)
							if err != nil {
								t.Fatal(err)
							}
							defer selected.Close()
							updated, err := prepare(updateSQL)
							if err != nil {
								t.Fatal(err)
							}
							defer updated.Close()
							heldSelect := fixture.HeldStatement{Executor: db, Statement: selected, SQL: selectSQL}
							heldUpdate := fixture.HeldStatement{Executor: db, Statement: updated, SQL: updateSQL}
							check := func() {
								run(fixture.Select("SELECT relname FROM pg_class WHERE relname='stable' OR relname='retry' ORDER BY relname", []string{"relname"}, []string{"NAME"}, [][]any{{"stable"}}))
								run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('stable'::regclass) AS definition", "CREATE INDEX stable ON public.t USING hash (n)"))
								run(fixture.Select("SELECT n,s FROM t ORDER BY n NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}, {nil, nil}}))
							}
							check()
							var before bytes.Buffer
							if !b.PG {
								if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
									t.Fatal(err)
								}
							}
							// Expected fields derive from the single-clause grammar and the
							// source token's Unicode character position, not native output.
							want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "WITH"`)
							want.Position = fixture.Position(utf8.RuneCountInString(tc.query[:strings.LastIndex(tc.query, "WITH")]) + 1)
							for _, prepared := range []bool{false, true} {
								if prepared {
									malformed, err := prepare(tc.query)
									if malformed != nil {
										malformed.Close()
										t.Fatal("syntax failure exposed a prepared handle", tc.query)
									}
									fixture.AssertDiagnostic(t, err, &want)
								} else {
									s := fixture.ErrorDiagnostic(tc.query, want)
									s.ErrorStage = "execute"
									run(s)
								}
								if len(notices) != 0 {
									t.Fatal("malformed IF NOT EXISTS emitted notices", notices)
								}
								check()
								if !b.PG {
									var after bytes.Buffer
									if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || after.String() != before.String() {
										t.Fatal("malformed declaration changed native state", err, after.String())
									}
								}
								// These are valid handles retained across failures, not an
								// impossible held execution of a syntax-invalid declaration.
								s := fixture.Select(selectSQL, []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "b"}, {int64(2), "b"}}, int64(2))
								s.NonContext = plain
								fixture.RunStep(t, p.Ctx, heldSelect, recorder, s)
								s = fixture.Exec(updateSQL, 0, "unused", int64(99))
								s.NonContext = plain
								fixture.RunStep(t, p.Ctx, heldUpdate, recorder, s)
							}
							if tc.existing {
								run(fixture.Exec("DROP INDEX stable", 0))
							}
							run(fixture.Exec("CREATE INDEX "+tc.index+" ON t USING hash(n)", 0))
							run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_indexdef('"+tc.index+"'::regclass) AS definition", "CREATE INDEX "+tc.index+" ON public.t USING hash (n)"))
							s := fixture.Exec(updateSQL, 1, "changed", int64(1))
							s.NonContext = plain
							fixture.RunStep(t, p.Ctx, heldUpdate, recorder, s)
							s = fixture.Select(selectSQL, []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "changed"}}, int64(1))
							s.NonContext = plain
							fixture.RunStep(t, p.Ctx, heldSelect, recorder, s)
							if len(notices) != 0 {
								t.Fatal(notices)
							}
						})
					}
				})
			}
		})
	}
}
