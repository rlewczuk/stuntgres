package reference_test

import (
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCastDefinitionTransactionConflicts(t *testing.T) {
	for _, kind := range []string{"same_name"} {
		t.Run(kind, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, holder := fixture.LockConnections(t, p.Ctx, b.DB)
					peer := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					for _, q := range []string{`CREATE TYPE conflict_src AS ENUM ('red')`, `CREATE TYPE conflict_dst AS ENUM ('red')`} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec("CREATE TABLE conflict_unrelated(v integer)", 0))
					fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("BEGIN", 0))
					defer holder.ExecContext(p.Ctx, "ROLLBACK")
					fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("SAVEPOINT claim", 0))
					fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec(`CREATE CAST (conflict_src AS conflict_dst) WITH INOUT`, 0))
					q := `CREATE CAST (conflict_src AS conflict_dst) WITH INOUT`
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("SAVEPOINT waiting", 0))
					fixture.DefinitionActiveCancel(t, p, b, c, holder, q, "transactionid", "ShareLock", 0, 0, "pg_cast_source_target_index", func(ctx context.Context) {
						fixture.RunStep(t, ctx, peer, b.Recorder, fixture.Exec("INSERT INTO conflict_unrelated VALUES(42)", 1))
						fixture.RunStep(t, ctx, peer, b.Recorder, fixture.Select(`SELECT count(*) AS n FROM pg_cast WHERE castsource='conflict_src'::regtype AND casttarget='conflict_dst'::regtype`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
					}, nil)
					run(fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
					run(fixture.Exec("ROLLBACK TO waiting", 0))
					run(fixture.Exec("SET LOCAL lock_timeout='100ms'", 0))
					_, err := c.ExecContext(p.Ctx, q)
					fixture.DefinitionIndexWaitDiagnostic(t, err, "55P03", "canceling statement due to lock timeout", "pg_cast_source_target_index", b.PG)
					run(fixture.Exec("ROLLBACK TO waiting", 0))
					fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("ROLLBACK TO claim", 0))
					// The canceled request is removed, the holder released, and the same
					// physical owner can now publish without stealing another identity.
					run(fixture.Exec(q, 0))
					run(fixture.Exec("RELEASE waiting", 0))
					run(fixture.Exec("COMMIT", 0))
					fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("COMMIT", 0))
					run(fixture.Select("SELECT v FROM conflict_unrelated", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
					if kind == "same_name" {
						run(fixture.Select(`SELECT count(*) AS n FROM pg_cast WHERE castsource='conflict_src'::regtype AND casttarget='conflict_dst'::regtype`, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
					}
				})
			}
		})
	}
	// Pair private cast creation with an autocommit function-retirement waiter.
	// The reverse interleaving also has independent canonical stage/lock coverage.
	for _, kind := range []string{"same_pair_commit", "drop_vs_drop", "new_edge_vs_function_drop"} {
		t.Run(kind, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, holder := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(x fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, x, b.Recorder, s) }
					run(c, fixture.Exec("CREATE TYPE queued_src AS ENUM ('red')", 0))
					run(c, fixture.Exec("CREATE TYPE queued_dst AS ENUM ('red')", 0))
					var source, target, oid int64
					if err := c.QueryRowContext(p.Ctx, "SELECT 'queued_src'::regtype::oid::bigint,'queued_dst'::regtype::oid::bigint").Scan(&source, &target); err != nil {
						t.Fatal(err)
					}
					create := "CREATE CAST (queued_src AS queued_dst) WITH INOUT"
					drop := "DROP CAST (queued_src AS queued_dst)"
					q, holderSQL, end, lockKind, mode, class, object := create, create, "COMMIT", "transactionid", "ShareLock", int64(0), int64(0)
					if kind == "drop_vs_drop" {
						run(c, fixture.Exec(create, 0))
						if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_cast WHERE castsource=$1 AND casttarget=$2", source, target).Scan(&oid); err != nil {
							t.Fatal(err)
						}
						q, holderSQL, end, lockKind, mode, class, object = drop, drop, "ROLLBACK", "object", "AccessExclusiveLock", 2605, oid
					}
					if kind == "new_edge_vs_function_drop" {
						run(c, fixture.Exec("CREATE FUNCTION queued_score(queued_src) RETURNS integer LANGUAGE SQL AS $$ SELECT 41 $$", 0))
						if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_proc WHERE proname='queued_score'").Scan(&oid); err != nil {
							t.Fatal(err)
						}
						create = "CREATE CAST (queued_src AS integer) WITH FUNCTION queued_score(queued_src)"
						q, holderSQL, lockKind, mode, class, object = "DROP FUNCTION queued_score(queued_src) RESTRICT", create, "object", "AccessExclusiveLock", 1255, oid
					}
					run(holder, fixture.Exec("BEGIN", 0))
					run(holder, fixture.Exec(holderSQL, 0))
					if kind != "new_edge_vs_function_drop" {
						run(c, fixture.Exec("BEGIN", 0))
					}
					join := fixture.DefinitionLockOperation(t, p.Ctx, p, b, c, holder, q, lockKind, mode, class, object)
					run(holder, fixture.Exec(end, 0))
					outcome := join()
					switch kind {
					case "same_pair_commit":
						want := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "pg_cast_source_target_index"`)
						want.Schema, want.Table, want.Constraint = "pg_catalog", "pg_cast", "pg_cast_source_target_index"
						want.Detail = fmt.Sprintf("Key (castsource, casttarget)=(%d, %d) already exists.", source, target)
						fixture.AssertDiagnostic(t, outcome.Err, &want)
					case "new_edge_vs_function_drop":
						want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop function queued_score(queued_src) because other objects depend on it")
						want.Detail, want.Hint = "cast from queued_src to integer depends on function queued_score(queued_src)", "Use DROP ... CASCADE to drop the dependent objects too."
						fixture.AssertDiagnostic(t, outcome.Err, &want)
					default:
						if outcome.Err != nil {
							t.Fatal(outcome.Err)
						}
						fixture.LockAffected(t, outcome.Result, 0)
						if kind == "drop_vs_drop" {
							run(c, fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource=$1 AND casttarget=$2", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, source, target))
						}
					}
					if kind == "same_pair_commit" || kind == "new_edge_vs_function_drop" {
						if outcome.Result != nil {
							t.Fatal("failed queued DDL returned result")
						}
						if kind == "same_pair_commit" {
							run(c, fixture.ErrorDiagnostic("SELECT 1", fixture.TransactionAborted()))
						}
					}
					if kind == "new_edge_vs_function_drop" {
						// Failed autocommit retirement leaves no aborted block or partial drop.
						run(c, fixture.Select("SELECT 1 AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
					} else {
						run(c, fixture.Exec("ROLLBACK", 0))
					}
					if kind == "same_pair_commit" || kind == "drop_vs_drop" {
						run(c, fixture.Select("SELECT 'red'::queued_src::queued_dst::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"red"}}))
						if kind == "drop_vs_drop" {
							run(c, fixture.Select("SELECT oid::bigint FROM pg_cast WHERE castsource=$1 AND casttarget=$2", []string{"oid"}, []string{"INT8"}, [][]any{{oid}}, source, target))
						}
						run(c, fixture.Exec(drop, 0))
					} else {
						run(c, fixture.Select("SELECT oid::bigint FROM pg_proc WHERE proname='queued_score'", []string{"oid"}, []string{"INT8"}, [][]any{{oid}}))
						if kind == "new_edge_vs_function_drop" {
							run(c, fixture.Select("SELECT 'red'::queued_src::integer AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(41)}}))
							run(c, fixture.Exec("DROP CAST (queued_src AS integer)", 0))
						}
						run(c, fixture.Exec("DROP FUNCTION queued_score(queued_src)", 0))
					}
					if kind == "new_edge_vs_function_drop" {
						run(c, fixture.Exec("CREATE FUNCTION queued_score(queued_src) RETURNS integer LANGUAGE SQL AS $$ SELECT 41 $$", 0))
						run(c, fixture.Exec(create, 0))
						run(c, fixture.Select("SELECT 'red'::queued_src::integer AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(41)}}))
						run(c, fixture.Exec("DROP CAST (queued_src AS integer)", 0))
						run(c, fixture.Exec("DROP FUNCTION queued_score(queued_src)", 0))
					} else {
						run(c, fixture.Exec(create, 0))
						run(c, fixture.Exec(drop, 0))
					}
				})
			}
		})
	}
}
