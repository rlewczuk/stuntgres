package reference_test

import (
	"bytes"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
	"unicode/utf8"
)

func TestReferenceDefaultPredicateControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			expression string
			want       bool
		}{
			{"NULL IS NULL", true}, {"1 IS NULL", false}, {"NULL IS NOT NULL", false}, {"1 IS NOT NULL", true},
			{"NULL IS TRUE", false}, {"TRUE IS TRUE", true}, {"FALSE IS NOT TRUE", true},
			{"NULL IS FALSE", false}, {"FALSE IS FALSE", true}, {"TRUE IS NOT FALSE", true},
			{"NULL IS UNKNOWN", true}, {"TRUE IS NOT UNKNOWN", true},
			{"NULL IS NOT TRUE", true}, {"NULL IS NOT FALSE", true}, {"NULL IS NOT UNKNOWN", false},
			{"NULL ISNULL", true}, {"NULL NOTNULL", false},
			{"1 IS DISTINCT FROM NULL", true}, {"NULL IS NOT DISTINCT FROM NULL", true},
			{"1 IS NOT DISTINCT FROM 1", true}, {"1 IS DISTINCT FROM 1", false}, {"1 IS NOT DISTINCT FROM '1'", true},
			{"(NULL IS NULL) IS NOT DISTINCT FROM TRUE", true},
			{"(NULL IS NULL) AND NOT ((FALSE IS TRUE) OR (1 IS DISTINCT FROM 1))", true},
		} {
			t.Run(fmt.Sprintf("prepared=%v/%s", prepared, tc.expression), func(t *testing.T) {
				p := newReferenceScenario(t)
				create := referenceExec("CREATE TABLE pd(id INT,b BOOL DEFAULT ("+tc.expression+"))", 0)
				empty := referenceSelect(`SELECT * FROM pd ORDER BY id`, []string{"id", "b"}, []string{"INT4", "BOOL"}, nil)
				populated := empty
				populated.rows = [][]any{{int64(1), tc.want}, {int64(2), tc.want}, {int64(3), tc.want}, {int64(4), nil}}
				// Specify real PostgreSQL values independently even while native CREATE fails.
				if p.postgres != nil {
					t.Run("postgres-oracle", func(t *testing.T) {
						run := func(s referenceStep) {
							s.prepared = prepared
							defaultReferenceRunStep(t, p, p.postgres, p.postgresRecord, s)
						}
						run(create)
						run(empty)
						run(referenceExec(`INSERT INTO pd(id) VALUES(1),(2)`, 2))
						run(referenceExec(`INSERT INTO pd VALUES(3,DEFAULT),(4,NULL)`, 2))
						run(populated)
					})
				}
				t.Run("native-controls-and-replay", func(t *testing.T) {
					native := func(s referenceStep) {
						s.prepared = prepared
						defaultReferenceRunStep(t, p, p.memory, p.memoryRecord, s)
					}
					both := func(s referenceStep) { s.prepared = prepared; defaultReferenceStep(t, p, s) }
					native(create)
					native(empty)
					roundtrip := func(want referenceStep) {
						t.Helper()
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
							t.Fatal(err)
						}
						if !strings.Contains(dump.String(), " DEFAULT (") {
							t.Fatal("predicate default structure lost", dump.String())
						}
						if err := stuntgres.Reset(p.ctx, p.native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
						var again bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || again.String() != dump.String() {
							t.Fatal("predicate redump", err, again.String())
						}
						if p.postgres != nil {
							if _, err := p.postgres.ExecContext(p.ctx, `DROP TABLE pd`); err != nil {
								t.Fatal(err)
							}
							if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
								t.Fatal("PostgreSQL predicate dump replay", err)
							}
						}
						both(want)
					}
					roundtrip(empty)
					both(referenceExec(`INSERT INTO pd(id) VALUES(1),(2)`, 2))
					both(referenceExec(`INSERT INTO pd VALUES(3,DEFAULT),(4,NULL)`, 2))
					both(populated)
					roundtrip(populated)
					var before bytes.Buffer
					if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
						t.Fatal(err)
					}
					err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(`CREATE TABLE failed(v BOOL DEFAULT (NULL IS NULL) NOT NULL); INSERT INTO failed VALUES(NULL);`))
					referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "v" of relation "failed" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "failed", Column: "v"})
					both(populated)
					var after bytes.Buffer
					if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || after.String() != before.String() {
						t.Fatal("failed restore changed predicate definitions/data", err, after.String())
					}
					both(referenceExec(`INSERT INTO pd(id) VALUES(5)`, 1))
					both(referenceExec(`INSERT INTO pd VALUES(6,DEFAULT)`, 1))
					populated.rows = append(populated.rows, []any{int64(5), tc.want}, []any{int64(6), tc.want})
					both(populated)
				})
			})
		}
	}
}

func TestReferenceDefaultPredicateProhibitions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ expression, token, code, message string }{
			{"b IS NULL", "b IS", "0A000", "cannot use column reference in DEFAULT expression"},
			{"$1 IS NULL", "$1", "42P02", "there is no parameter $1"},
			{"TRUE OR (b IS NOT DISTINCT FROM NULL)", "b IS", "0A000", "cannot use column reference in DEFAULT expression"},
			{"FALSE AND ($1 IS TRUE)", "$1", "42P02", "there is no parameter $1"},
			{"(1 IS TRUE) AND b", "1 IS", "42804", "argument of IS TRUE must be type boolean, not type integer"},
		} {
			t.Run(fmt.Sprintf("prepared=%v/%s", prepared, tc.expression), func(t *testing.T) {
				p := newReferenceScenario(t)
				q := "CREATE TABLE bad(b BOOL DEFAULT (" + tc.expression + "))"
				position := fmt.Sprint(utf8.RuneCountInString(q[:strings.Index(q, tc.token)]) + 1)
				s := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: position})
				s.prepared, s.errorStage = prepared, "execute"
				p.step(t, s)
				absent := referenceImplicitError(`SELECT * FROM bad`, referenceDiagnostic{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"})
				absent.prepared = prepared
				p.step(t, absent)
			})
		}
	}
}

func TestReferenceDefaultPredicateOuterSyntax(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ expression, token, position string }{
			{"NULL IS NULL", "NULL", "41"}, {"TRUE IS TRUE", "TRUE", "41"}, {"NULL ISNULL", "ISNULL", "38"}, {"NULL NOTNULL", "NOTNULL", "38"},
		} {
			t.Run(fmt.Sprintf("prepared=%v/%s", prepared, tc.expression), func(t *testing.T) {
				p := newReferenceScenario(t)
				s := referenceImplicitError("CREATE TABLE bad(b BOOL DEFAULT "+tc.expression+")", referenceDiagnostic{Code: "42601", Message: `syntax error at or near "` + tc.token + `"`, Position: tc.position})
				s.prepared = prepared
				s.errorStage = "execute"
				if prepared {
					s.errorStage = "prepare"
				}
				p.step(t, s)
				absent := referenceImplicitError(`SELECT * FROM bad`, referenceDiagnostic{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"})
				absent.prepared = prepared
				p.step(t, absent)
			})
		}
		t.Run(fmt.Sprintf("prepared=%v/outer-distinct", prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			run := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
			run(referenceExec(`CREATE TABLE outer_d(b BOOL DEFAULT 1 IS DISTINCT FROM 2,n BOOL DEFAULT NULL IS NOT DISTINCT FROM NULL)`, 0))
			run(referenceExec(`INSERT INTO outer_d DEFAULT VALUES`, 1))
			run(referenceSelect(`SELECT * FROM outer_d`, []string{"b", "n"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		})
	}
}
