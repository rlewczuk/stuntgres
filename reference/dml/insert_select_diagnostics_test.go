package reference_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertSelectValuesDiagnostics(t *testing.T) {
	for _, tc := range []struct{ q, code, message, token string }{
		{"INSERT INTO t VALUES(1),(TRUE) ORDER BY 1", "42804", "VALUES types integer and boolean cannot be matched", "TRUE"},
		{"INSERT INTO t VALUES(TRUE),(1) ORDER BY 1", "42804", "VALUES types boolean and integer cannot be matched", "1)"},
		{"INSERT INTO t VALUES(TIME '00:00'),(TIMESTAMP 'epoch') ORDER BY 1", "42846", "VALUES could not convert type timestamp without time zone to time without time zone", "'epoch'"},
		{"INSERT INTO t VALUES(TIMESTAMP 'epoch'),(TIME '00:00') ORDER BY 1", "42846", "VALUES could not convert type time without time zone to timestamp without time zone", "'00:00'"},
		{"INSERT INTO t VALUES(1),(2,3) ORDER BY 1", "42601", "VALUES lists must all be the same length", "2,3"},
		{"INSERT INTO t VALUES(1) ORDER BY 0", "42P10", "ORDER BY position 0 is not in select list", "0"},
		{"INSERT INTO t VALUES(1) ORDER BY 2", "42P10", "ORDER BY position 2 is not in select list", "2"},
		{"INSERT INTO t VALUES(1) ORDER BY 'x'", "42601", "non-integer constant in ORDER BY", "'x'"},
		{"INSERT INTO t VALUES(1) ORDER BY TRUE", "42601", "non-integer constant in ORDER BY", "TRUE"},
		{"INSERT INTO t VALUES(1) ORDER BY NULL", "42601", "non-integer constant in ORDER BY", "NULL"},
		{"INSERT INTO t VALUES(1) ORDER BY 1.5", "42601", "non-integer constant in ORDER BY", "1.5"},
		{`INSERT INTO t VALUES(1) ORDER BY "1"`, "42703", `column "1" does not exist`, `"1"`},
		{"INSERT INTO t(i,b) SELECT $1,$1", "42P08", "inconsistent types deduced for parameter $1", "$1"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.InsertSelectRun(t, p.Ctx, backend.DB, backend.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(i INT,b BOOL)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(9,TRUE)", 1))
					index := strings.Index(tc.q, tc.token)
					if tc.code == "42P08" {
						index = strings.LastIndex(tc.q, tc.token)
					}
					want := fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: strconv.Itoa(index + 1)}
					if tc.code == "42P08" {
						want.Detail = "integer versus boolean"
					}
					for _, prepared := range []bool{false, true} {
						s := fixture.ImplicitError(tc.q, want)
						if tc.code == "42P08" {
							s.Args = []any{nil}
						}
						s.Prepared, s.ErrorStage = prepared, "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						run(s)
						run(fixture.Select("SELECT * FROM t", []string{"i", "b"}, []string{"INT4", "BOOL"}, [][]any{{int64(9), true}}))
					}
					if tc.code == "42P08" {
						plain := want
						if backend.PG {
							plain = fixture.Diagnostic{Code: "42P02", Message: "there is no parameter $1", Position: "27"}
						}
						s := fixture.ImplicitError(tc.q, plain)
						s.ErrorStage = "execute"
						run(s)
						run(fixture.Select("SELECT * FROM t", []string{"i", "b"}, []string{"INT4", "BOOL"}, [][]any{{int64(9), true}}))
					}
				})
			}
		})
	}
}

func TestReferenceInsertSelectAtomicConstraints(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.InsertSelectRun(t, p.Ctx, backend.DB, backend.Recorder, s) }
			for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE s(i INT)", 0), fixture.Exec("INSERT INTO s VALUES(1),(NULL)", 2), fixture.Exec("CREATE TABLE t(i INT NOT NULL)", 0), fixture.Exec("INSERT INTO t VALUES(9)", 1)} {
				run(s)
			}
			sentinel := fixture.Select("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}})
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("INSERT INTO t SELECT i FROM s ORDER BY i", fixture.Diagnostic{Code: "23502", Message: `null value in column "i" of relation "t" violates not-null constraint`, Schema: "public", Table: "t", Column: "i", Detail: "Failing row contains (null)."})
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				run(sentinel)
				s = fixture.ImplicitError("INSERT INTO t SELECT i FROM s WHERE i='bad'", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "39"})
				s.ErrorStage = "execute"
				run(s)
				run(sentinel)
				if prepared {
					s.Prepared, s.ErrorStage = true, "prepare"
					run(s)
					run(sentinel)
				}
			}
		})
	}
}

func TestReferenceInsertSelectPreCanceled(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.InsertSelectRun(t, p.Ctx, backend.DB, backend.Recorder, s) }
			for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE s(i INT)", 0), fixture.Exec("INSERT INTO s VALUES(1),(NULL)", 2), fixture.Exec("CREATE TABLE t(i INT NOT NULL)", 0), fixture.Exec("INSERT INTO t VALUES(9)", 1)} {
				run(s)
			}
			for _, prepared := range []bool{false, true} {
				query := "INSERT INTO t SELECT i FROM s"
				var x fixture.Executor = backend.DB
				if prepared {
					stmt, err := backend.DB.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					x = fixture.HeldStatement{backend.DB, stmt, query}
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				result, err := x.ExecContext(ctx, query)
				if result != nil || !errors.Is(err, context.Canceled) {
					t.Fatal(result, err)
				}
				if _, ok := err.(interface{ SQLState() string }); ok {
					t.Fatal("ordinary cancellation gained SQLSTATE", err)
				}
				run(fixture.Select("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
			}
		})
	}
}
