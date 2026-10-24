package reference_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalSchemaQuotedNameConflicts(t *testing.T) {
	for _, name := range []struct{ label, sql, raw string }{
		{"quote", `"a""b"`, `a"b`},
		{"backslash", `"a\b"`, `a\b`},
	} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/commit=%t", name.label, commit), func(t *testing.T) {
				fixture.SchemaReferenceCleanup(t, "tx_quoted")
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
						fixture.LockExec(t, p.Ctx, a, "CREATE SCHEMA tx_quoted", 0)
						ap, pp := transactionalSchemaPID(t, p, b, a), transactionalSchemaPID(t, p, b, peer)
						var namespace int64
						if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname='tx_quoted'").Scan(&namespace); err != nil {
							t.Fatal(err)
						}
						target := "tx_quoted." + name.sql
						fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
						defer a.ExecContext(p.Ctx, "ROLLBACK")
						fixture.LockExec(t, p.Ctx, a, "CREATE TABLE "+target+"(v INT DEFAULT 7)", 0)
						fixture.LockExec(t, p.Ctx, a, "INSERT INTO "+target+" DEFAULT VALUES", 1)
						done := make(chan struct{})
						await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
							defer close(done)
							result, err := peer.ExecContext(ctx, "CREATE TABLE "+target+"(v INT DEFAULT 9)")
							return fixture.RelationOutcome{Result: result, Err: err}
						})
						ctx, cancel := context.WithTimeout(p.Ctx, 3*time.Second)
						defer cancel()
						for {
							select {
							case <-done:
								o := await()
								t.Fatal("quoted-name publisher completed before conflict observation", o.Err)
							default:
							}
							var blocked bool
							if err := b.DB.QueryRowContext(ctx, "SELECT $1::integer = ANY(pg_blocking_pids($2::integer))", ap, pp).Scan(&blocked); err != nil {
								t.Fatal(err)
							}
							if blocked {
								break
							}
						}
						end := "ROLLBACK"
						if commit {
							end = "COMMIT"
						}
						fixture.LockExec(t, p.Ctx, a, end, 0)
						o := await()
						if commit {
							d := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "pg_type_typname_nsp_index"`)
							// PostgreSQL nameout / BuildIndexValueDescription append
							// the decoded name verbatim, without identifier escaping.
							d.Detail = fmt.Sprintf("Key (typname, typnamespace)=(%s, %d) already exists.", name.raw, namespace)
							d.Schema, d.Table, d.Constraint = "pg_catalog", "pg_type", "pg_type_typname_nsp_index"
							fixture.AssertDiagnostic(t, o.Err, &d)
							if o.Result != nil {
								t.Fatal("failed quoted-name CREATE exposed a result", o.Result)
							}
						} else {
							if o.Err != nil {
								t.Fatal(o.Err)
							}
							n, err := o.Result.RowsAffected()
							if err != nil || n != 0 {
								t.Fatal(n, err)
							}
						}
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, peer, b.Recorder, s) }
						run(fixture.Select("SELECT c.relname,t.typname,a.typname AS array_name,t.typrelid=c.oid AS reciprocal FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray AND a.typelem=t.oid WHERE c.relnamespace=$1 AND c.relname=$2", []string{"relname", "typname", "array_name", "reciprocal"}, []string{"NAME", "NAME", "NAME", "BOOL"}, [][]any{{name.raw, name.raw, "_" + name.raw, true}}, namespace, name.raw))
						run(fixture.Select("SELECT count(*) FROM pg_type WHERE typnamespace=$1 AND typname IN ($2,$3)", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}, namespace, name.raw, "_"+name.raw))
						var before [][]any
						value := int64(9)
						if commit {
							before = [][]any{{int64(7)}}
							value = 7
						}
						run(fixture.Select("SELECT v FROM "+target, []string{"v"}, []string{"INT4"}, before))
						run(fixture.Exec("INSERT INTO "+target+" DEFAULT VALUES", 1))
						want := [][]any{{value}}
						if commit {
							want = append(want, []any{value})
						}
						run(fixture.Select("SELECT v FROM "+target+" ORDER BY v", []string{"v"}, []string{"INT4"}, want))
					})
				}
			})
		}
	}
}
