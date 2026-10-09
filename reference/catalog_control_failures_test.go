package reference_test

import (
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceControlFailedRestorePreservesCatalog(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "recorded", true: "source"}[nonContext], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					conn, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					executor := referenceExecutor(conn)
					if nonContext {
						executor = backend.db
					}
					run := func(s referenceStep) {
						s.nonContext = nonContext
						referenceRunStep(t, p.ctx, executor, backend.recorder, s)
					}
					run(referenceExec("CREATE TABLE sentinel(v INT NOT NULL)", 0))
					run(referenceExec("INSERT INTO sentinel VALUES(77)", 1))
					for _, tc := range []struct{ name, script, state string }{
						{"later insert", "CREATE TABLE replacement(v INT NOT NULL); INSERT INTO replacement VALUES(1),(NULL);", "23502"},
						{"duplicate", "CREATE TABLE replacement(v INT); CREATE TABLE replacement(v INT);", "42P07"},
						{"select", "CREATE TABLE replacement(v INT); SELECT v FROM replacement;", "0A000"},
						{"parameter", "CREATE TABLE replacement(v INT); INSERT INTO replacement VALUES($1);", "42P02"},
						{"syntax", "CREATE TABLE replacement(v INT); INSERT INTO replacement VALUES(;", "42601"},
					} {
						t.Run(tc.name, func(t *testing.T) {
							run := func(s referenceStep) {
								s.nonContext = nonContext
								referenceRunStep(t, p.ctx, executor, backend.recorder, s)
							}
							stage := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, backend.recorder, s) }
							defer conn.ExecContext(p.ctx, "ROLLBACK")
							if backend.pg {
								// Real SQL staging is transactional, not a fictitious PostgreSQL Restore.
								// A SELECT is valid SQL on PG but deliberately not a native replay command.
								stage(referenceExec("BEGIN", 0))
								_, err = conn.ExecContext(p.ctx, tc.script)
								state := tc.state
								if tc.name == "select" {
									state = ""
								}
								referenceState(t, err, state)
								stage(referenceExec("ROLLBACK", 0))
							} else {
								err = stuntgres.Restore(p.ctx, p.native, strings.NewReader(tc.script))
								referenceState(t, err, tc.state)
							}
							run(referenceSelect("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
							missing := referenceError("SELECT v FROM replacement", "42P01")
							missing.selectRows = nonContext
							run(missing)
						})
					}
				})
			}
			failure := errors.New("reader failed")
			if err := stuntgres.Restore(p.ctx, p.native, catalogControlFailingReader{failure}); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			canceled, cancel := context.WithCancel(p.ctx)
			defer cancel()
			if err := stuntgres.Restore(canceled, p.native, &catalogControlCancelReader{cancel: cancel}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(canceled, p.native); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			failure = errors.New("writer failed")
			if err := stuntgres.Dump(p.ctx, p.native, catalogControlFailingWriter{failure}); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			catalogControlTuples(t, p.native, "SELECT v FROM sentinel", [][]any{{int64(77)}})
		})
	}
}
