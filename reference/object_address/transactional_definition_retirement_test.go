package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalDefinitionRetirement(t *testing.T) {
	for _, kind := range []string{"method_family", "family_class", "family_member"} {
		for _, creatorFirst := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/creator_first_%t/commit_%t", kind, creatorFirst, commit), func(t *testing.T) {
					definitionScenario(t, []string{"tx_retire_am"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn) {
						c, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						defer c.ExecContext(p.Ctx, `ROLLBACK`)
						runA := definitionRun(t, p, b, a)
						runC := definitionRun(t, p, b, c)
						peer := definitionRun(t, p, b, b.DB)
						runA(fixture.Exec(`CREATE ACCESS METHOD tx_retire_am TYPE INDEX HANDLER bthandler`, 0))
						targetClass := int64(2601)
						target := definitionID(t, p, a, `SELECT oid::bigint FROM pg_am WHERE amname='tx_retire_am'`)
						create := `CREATE OPERATOR FAMILY public.tx_retire_family USING tx_retire_am`
						drop := `DROP ACCESS METHOD tx_retire_am RESTRICT`
						label := "access method"
						family := int64(0)
						if kind != "method_family" {
							runA(fixture.Exec(create, 0))
							family = definitionID(t, p, a, `SELECT oid::bigint FROM pg_opfamily WHERE opfname='tx_retire_family'`)
							targetClass, target, label = 2753, family, "operator family"
							drop = `DROP OPERATOR FAMILY public.tx_retire_family USING tx_retire_am RESTRICT`
							create = `CREATE OPERATOR CLASS public.tx_retire_class FOR TYPE integer USING tx_retire_am FAMILY public.tx_retire_family AS ` + definitionClassItems
							if kind == "family_member" {
								create = `ALTER OPERATOR FAMILY public.tx_retire_family USING tx_retire_am ADD FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`
							}
						}
						query, heldQuery := drop, create
						if creatorFirst {
							query, heldQuery = create, drop
						}
						held := definitionHeld(t, p, c, heldQuery)
						defer held.Statement.Close()
						ap, cp := 0, 0
						if b.PG {
							ap = fixture.LockPID(t, p.Ctx, a)
							cp = fixture.LockPID(t, p.Ctx, c)
						}
						runA(fixture.Exec(`BEGIN`, 0))
						runC(fixture.Exec(`BEGIN`, 0))
						runA(fixture.Exec(query, 0))
						// The owner sees its coherent private transition, never a partial group.
						if creatorFirst {
							if kind == "method_family" {
								runA(definitionCount(`SELECT count(*) FROM pg_opfamily WHERE opfname='tx_retire_family'`, 1))
								peer(definitionCount(`SELECT count(*) FROM pg_opfamily WHERE opfname='tx_retire_family'`, 0))
							} else if kind == "family_class" {
								runA(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname='tx_retire_class'`, 1))
								peer(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname='tx_retire_class'`, 0))
							} else {
								runA(definitionCount(`SELECT count(*) FROM pg_amproc WHERE amprocfamily=$1::oid`, 1, family))
								peer(definitionCount(`SELECT count(*) FROM pg_amproc WHERE amprocfamily=$1::oid`, 0, family))
							}
						}
						finished := make(chan struct{})
						await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
							defer close(finished)
							r, err := held.Statement.ExecContext(ctx)
							return fixture.RelationOutcome{Result: r, Err: err}
						})
						mode := "AccessShareLock"
						if creatorFirst {
							mode = "AccessExclusiveLock"
						}
						fixture.ObjectLockPending(t, p, b, cp, ap, targetClass, target, mode, finished)
						peer(fixture.Exec(`CREATE OPERATOR FAMILY public.tx_retire_unrelated USING hash`, 0))
						end := "ROLLBACK"
						if commit {
							end = "COMMIT"
						}
						runA(fixture.Exec(end, 0))
						o := await()
						if !creatorFirst && commit {
							if o.Result != nil {
								t.Fatal("retired-provider failure exposed result")
							}
							want := fixture.ControlsReferenceDiagnostic("42704", `referenced `+label+` was concurrently dropped`)
							fixture.AssertDiagnostic(t, o.Err, &want)
							runC(fixture.ErrorDiagnostic(`SELECT 1`, definitionAborted()))
							runC(fixture.Exec(`ROLLBACK`, 0))
						} else {
							fixture.DeleteReferenceCount(t, o.Result, o.Err, 0)
							if creatorFirst {
								runC(definitionCount(`SELECT count(*) FROM pg_opfamily WHERE opfname='tx_retire_family'`, 0))
								runC(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname='tx_retire_class'`, 0))
							}
							runC(fixture.Exec(`COMMIT`, 0))
						}
						retired := creatorFirst || commit
						if retired {
							peer(definitionCount(`SELECT count(*) FROM pg_opfamily WHERE opfname='tx_retire_family'`, 0))
							peer(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname='tx_retire_class'`, 0))
							peer(definitionCount(`SELECT count(*) FROM pg_depend WHERE refclassid=$1::oid AND refobjid=$2::oid`, 0, targetClass, target))
							if family != 0 {
								peer(definitionCount(`SELECT count(*) FROM pg_amproc WHERE amprocfamily=$1::oid`, 0, family))
								peer(definitionCount(`SELECT count(*) FROM pg_amop WHERE amopfamily=$1::oid`, 0, family))
							}
						} else {
							peer(definitionCount(`SELECT count(*) FROM pg_opfamily WHERE opfname='tx_retire_family'`, 1))
							if kind == "family_class" {
								peer(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname='tx_retire_class'`, 1))
								peer(definitionCount(`SELECT count(*) FROM pg_amop WHERE amopfamily=$1::oid`, 5, family))
								peer(definitionCount(`SELECT count(*) FROM pg_amproc WHERE amprocfamily=$1::oid`, 1, family))
							}
							if kind == "family_member" {
								peer(fixture.Select(`SELECT amprocnum,amproc::oid::bigint FROM pg_amproc WHERE amprocfamily=$1::oid`, []string{"amprocnum", "amproc"}, []string{"INT2", "INT8"}, [][]any{{int64(1), int64(351)}}, family))
							}
						}
						peer(definitionCount(`SELECT count(*) FROM pg_opfamily WHERE opfname='tx_retire_unrelated' AND opfmethod=405`, 1))
						runC(fixture.Exec(`CREATE OPERATOR FAMILY public.tx_retire_reuse USING btree`, 0))
						runC(fixture.Exec(`DROP OPERATOR FAMILY public.tx_retire_reuse USING btree`, 0))
					})
				})
			}
		}
	}
}
