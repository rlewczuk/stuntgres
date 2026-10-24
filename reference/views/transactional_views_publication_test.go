package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalViewsPublication(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			transactionalViewsRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
				base := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				prun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, peer, b.Recorder, s) }
				base(fixture.Exec("CREATE TABLE txv_source(id INT)", 0))
				base(fixture.Exec("INSERT INTO txv_source VALUES(1)", 1))
				base(fixture.Exec("CREATE TABLE txv_unrelated(id INT)", 0))
				e, finish := transactionalViewsBegin(t, p, b, c, mode)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
				run(fixture.Exec("INSERT INTO txv_source VALUES(2)", 1))
				transactionalViewsExec(t, p, b, e, held, fixture.Exec("CREATE VIEW txv_v AS SELECT id FROM txv_source", 0))
				transactionalViewsExec(t, p, b, e, held, fixture.Exec("CREATE MATERIALIZED VIEW txv_m AS SELECT id FROM txv_v", 2))
				prun(fixture.Exec("INSERT INTO txv_unrelated VALUES(99)", 1))
				prun(fixture.Exec("CREATE VIEW txv_peer AS SELECT id FROM txv_unrelated", 0))
				finish(commit)
				prun(fixture.Select("SELECT id FROM txv_peer", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
				if commit || mode == "autocommit" {
					for _, name := range []string{"txv_source", "txv_v", "txv_m"} {
						prun(fixture.Select("SELECT id FROM "+name+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
					}
				} else {
					prun(fixture.Select("SELECT id FROM txv_source", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
					fixture.QueryTableCreateAbsent(t, prun, "txv_v")
					fixture.QueryTableCreateAbsent(t, prun, "txv_m")
				}
			})
		})
	}
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprintf("same-name/commit=%v", commit), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					owner, waiter := fixture.LockConnections(t, p.Ctx, b.DB)
					stmt, err := waiter.PrepareContext(p.Ctx, "CREATE VIEW txv_contended AS SELECT 2 AS id")
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					tx := fixture.QueryLockBegin(t, p.Ctx, owner)
					defer tx.Rollback()
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("CREATE VIEW txv_contended AS SELECT 1 AS id", 0))
					pid, hpid := 0, 0
					var namespace int64
					if b.PG {
						pid = fixture.LockPID(t, p.Ctx, waiter)
						hpid = fixture.LockPID(t, p.Ctx, owner)
					}
					if err := waiter.QueryRowContext(p.Ctx, "SELECT 'public'::regnamespace::BIGINT").Scan(&namespace); err != nil {
						t.Fatal(err)
					}
					finished := make(chan struct{})
					join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(finished)
						r, err := stmt.ExecContext(ctx)
						return fixture.RelationOutcome{Result: r, Err: err}
					})
					fixture.RelationLockPending(t, p, b, pid, hpid, 0, "", finished)
					if commit {
						err = tx.Commit()
					} else {
						err = tx.Rollback()
					}
					if err != nil {
						t.Fatal(err)
					}
					out := join()
					if commit {
						want := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "pg_type_typname_nsp_index"`)
						want.Detail = fmt.Sprintf("Key (typname, typnamespace)=(txv_contended, %d) already exists.", namespace)
						want.Schema, want.Table, want.Constraint = "pg_catalog", "pg_type", "pg_type_typname_nsp_index"
						fixture.AssertDiagnostic(t, out.Err, &want)
					} else if out.Err != nil {
						t.Fatal(out.Err)
					}
					value := int64(2)
					if commit {
						value = 1
					}
					fixture.RunStep(t, p.Ctx, waiter, b.Recorder, fixture.Select("SELECT id FROM txv_contended", []string{"id"}, []string{"INT4"}, [][]any{{value}}))
				})
			}
		})
	}
}
