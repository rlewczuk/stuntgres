package reference_test

import (
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceControlFailedRestorePreservesCatalog(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "recorded", true: "source"}[nonContext], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					conn, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					executor := fixture.Executor(conn)
					if nonContext {
						executor = backend.DB
					}
					run := func(s fixture.Step) {
						s.NonContext = nonContext
						fixture.RunStep(t, p.Ctx, executor, backend.Recorder, s)
					}
					run(fixture.Exec("CREATE TABLE sentinel(v INT NOT NULL)", 0))
					run(fixture.Exec("INSERT INTO sentinel VALUES(77)", 1))
					for _, tc := range []struct{ name, script, state string }{
						{"later insert", "CREATE TABLE replacement(v INT NOT NULL); INSERT INTO replacement VALUES(1),(NULL);", "23502"},
						{"duplicate", "CREATE TABLE replacement(v INT); CREATE TABLE replacement(v INT);", "42P07"},
						{"select", "CREATE TABLE replacement(v INT); SELECT v FROM replacement;", "0A000"},
						{"parameter", "CREATE TABLE replacement(v INT); INSERT INTO replacement VALUES($1);", "42P02"},
						{"syntax", "CREATE TABLE replacement(v INT); INSERT INTO replacement VALUES(;", "42601"},
					} {
						t.Run(tc.name, func(t *testing.T) {
							run := func(s fixture.Step) {
								s.NonContext = nonContext
								fixture.RunStep(t, p.Ctx, executor, backend.Recorder, s)
							}
							stage := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
							defer conn.ExecContext(p.Ctx, "ROLLBACK")
							if backend.PG {
								// Real SQL staging is transactional, not a fictitious PostgreSQL Restore.
								// A SELECT is valid SQL on PG but deliberately not a native replay command.
								stage(fixture.Exec("BEGIN", 0))
								_, err = conn.ExecContext(p.Ctx, tc.script)
								state := tc.state
								if tc.name == "select" {
									state = ""
								}
								fixture.State(t, err, state)
								stage(fixture.Exec("ROLLBACK", 0))
							} else {
								err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(tc.script))
								fixture.State(t, err, tc.state)
							}
							run(fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
							missing := fixture.Error("SELECT v FROM replacement", "42P01")
							missing.SelectRows = nonContext
							run(missing)
						})
					}
				})
			}
			failure := errors.New("reader failed")
			if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{failure}); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			canceled, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			if err := stuntgres.Restore(canceled, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(canceled, p.Native); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			failure = errors.New("writer failed")
			if err := stuntgres.Dump(p.Ctx, p.Native, fixture.CatalogControlFailingWriter{failure}); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			fixture.CatalogControlTuples(t, p.Native, "SELECT v FROM sentinel", [][]any{{int64(77)}})
		})
	}
}
