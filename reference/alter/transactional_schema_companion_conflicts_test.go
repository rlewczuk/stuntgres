package reference_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalSchemaCompanionConflicts(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprint("commit=", commit), func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "tx_companion")
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					a, c := fixture.LockConnections(t, p.Ctx, b.DB)
					fixture.LockExec(t, p.Ctx, a, "CREATE SCHEMA tx_companion", 0)
					ap, cp := transactionalSchemaPID(t, p, b, a), transactionalSchemaPID(t, p, b, c)
					var namespace int64
					if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname='tx_companion'").Scan(&namespace); err != nil {
						t.Fatal(err)
					}
					fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
					defer a.ExecContext(p.Ctx, "ROLLBACK")
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE tx_companion.collision(v INT)", 0)
					fixture.LockExec(t, p.Ctx, a, "INSERT INTO tx_companion.collision VALUES(4)", 1)
					done := make(chan struct{})
					// The peer standalone publisher keeps its existing autocommit route.
					await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(done)
						result, err := c.ExecContext(ctx, "CREATE TYPE tx_companion.collision AS(v INT)")
						return fixture.RelationOutcome{Result: result, Err: err}
					})
					ctx, cancel := context.WithTimeout(p.Ctx, 3*time.Second)
					defer cancel()
					for {
						select {
						case <-done:
							o := await()
							t.Fatal("standalone publisher completed before conflict observation", o.Err)
						default:
						}
						var blocked bool
						if err := b.DB.QueryRowContext(ctx, "SELECT $1::integer = ANY(pg_blocking_pids($2::integer))", ap, cp).Scan(&blocked); err != nil {
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
						d.Detail = fmt.Sprintf("Key (typname, typnamespace)=(collision, %d) already exists.", namespace)
						d.Schema, d.Table, d.Constraint = "pg_catalog", "pg_type", "pg_type_typname_nsp_index"
						fixture.AssertDiagnostic(t, o.Err, &d)
					} else {
						if o.Err != nil {
							t.Fatal(o.Err)
						}
						n, err := o.Result.RowsAffected()
						if err != nil || n != 0 {
							t.Fatal(n, err)
						}
					}
					kind := "c"
					if commit {
						kind = "r"
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT c.relkind::text AS kind,t.typrelid=c.oid AS reciprocal FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.relnamespace=$1 AND c.relname='collision'", []string{"kind", "reciprocal"}, []string{"TEXT", "BOOL"}, [][]any{{kind, true}}, namespace))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_type WHERE typnamespace=$1 AND typname IN ('collision','_collision')", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}, namespace))
					if commit {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM tx_companion.collision", []string{"v"}, []string{"INT4"}, [][]any{{int64(4)}}))
					}
				})
			}
		})
	}
}
