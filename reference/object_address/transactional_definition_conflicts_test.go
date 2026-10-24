package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalDefinitionNameTransitions(t *testing.T) {
	for _, kind := range []string{"FAMILY", "CLASS"} {
		for _, move := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/move_%t/commit_%t", kind, move, commit), func(t *testing.T) {
					for _, s := range []string{"tx_conflict_a", "tx_conflict_b", "tx_conflict_dest"} {
						fixture.SchemaReferenceCleanup(t, s)
					}
					definitionScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn) {
						c, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						defer c.ExecContext(p.Ctx, `ROLLBACK`)
						runA := definitionRun(t, p, b, a)
						runC := definitionRun(t, p, b, c)
						for _, s := range []string{"tx_conflict_a", "tx_conflict_b", "tx_conflict_dest"} {
							runA(fixture.Exec(`CREATE SCHEMA `+s, 0))
						}
						first, second, target := "public.tx_conflict_first", "public.tx_conflict_second", "tx_conflict_target"
						destination := "public"
						if move {
							first = "tx_conflict_a.tx_conflict_target"
							second = "tx_conflict_b.tx_conflict_target"
							destination = "tx_conflict_dest"
						}
						create := func(name string) string {
							q := `CREATE OPERATOR ` + kind + ` ` + name
							if kind == "CLASS" {
								q += ` FOR TYPE integer`
							}
							q += ` USING btree`
							if kind == "CLASS" {
								q += ` AS STORAGE integer`
							}
							return q
						}
						runA(fixture.Exec(create(first), 0))
						runA(fixture.Exec(create(second), 0))
						table, col, constraint := "pg_opfamily", "opfname", "pg_opfamily_am_name_nsp_index"
						oidcol, methodcol, nspcol := "oid", "opfmethod", "opfnamespace"
						if kind == "CLASS" {
							table = "pg_opclass"
							col = "opcname"
							constraint = "pg_opclass_am_name_nsp_index"
							methodcol = "opcmethod"
							nspcol = "opcnamespace"
						}
						firstName, firstSchema := "tx_conflict_first", "public"
						secondName, secondSchema := "tx_conflict_second", "public"
						if move {
							firstName = target
							firstSchema = "tx_conflict_a"
							secondName = target
							secondSchema = "tx_conflict_b"
						}
						firstID := definitionID(t, p, a, fmt.Sprintf(`SELECT %s::bigint FROM %s WHERE %s=$1 AND %s=$2::regnamespace`, oidcol, table, col, nspcol), firstName, firstSchema)
						secondID := definitionID(t, p, a, fmt.Sprintf(`SELECT oid::bigint FROM %s WHERE %s=$1 AND %s=$2::regnamespace`, table, col, nspcol), secondName, secondSchema)
						namespace := definitionID(t, p, a, `SELECT oid::bigint FROM pg_namespace WHERE nspname=$1`, destination)
						suffix := ` RENAME TO ` + target
						if move {
							suffix = ` SET SCHEMA ` + destination
						}
						holder := `ALTER OPERATOR ` + kind + ` ` + first + ` USING btree` + suffix
						query := `ALTER OPERATOR ` + kind + ` ` + second + ` USING btree` + suffix
						held := definitionHeld(t, p, c, query)
						defer held.Statement.Close()
						ap, cp := 0, 0
						if b.PG {
							ap = fixture.LockPID(t, p.Ctx, a)
							cp = fixture.LockPID(t, p.Ctx, c)
						}
						runA(fixture.Exec(`BEGIN`, 0))
						runA(fixture.Exec(holder, 0))
						runC(fixture.Exec(`BEGIN`, 0))
						finished := make(chan struct{})
						await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
							defer close(finished)
							r, err := held.Statement.ExecContext(ctx)
							return fixture.RelationOutcome{Result: r, Err: err}
						})
						fixture.RelationLockPending(t, p, b, cp, ap, 0, "", finished)
						end := "ROLLBACK"
						if commit {
							end = "COMMIT"
						}
						runA(fixture.Exec(end, 0))
						o := await()
						winner := secondID
						if commit {
							want := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "`+constraint+`"`)
							want.Detail = fmt.Sprintf("Key (%s, %s, %s)=(403, %s, %d) already exists.", methodcol, col, nspcol, target, namespace)
							want.Schema = "pg_catalog"
							want.Table = table
							want.Constraint = constraint
							fixture.AssertDiagnostic(t, o.Err, &want)
							if o.Result != nil {
								t.Fatal("failed name move exposed result")
							}
							runC(fixture.ErrorDiagnostic(`SELECT 1`, definitionAborted()))
							runC(fixture.Exec(`ROLLBACK`, 0))
							winner = firstID
						} else {
							fixture.DeleteReferenceCount(t, o.Result, o.Err, 0)
							runC(fixture.Exec(`COMMIT`, 0))
						}
						runA(fixture.Select(fmt.Sprintf(`SELECT oid::bigint,%s::bigint FROM %s WHERE %s=$1 AND %s=$2::regnamespace`, methodcol, table, col, nspcol), []string{"oid", methodcol}, []string{"INT8", "INT8"}, [][]any{{winner, int64(403)}}, target, destination))
						// The source that lost the collision retains its original identity.
						loserName, loserSchema, loser := firstName, firstSchema, firstID
						if commit {
							loserName, loserSchema, loser = secondName, secondSchema, secondID
						}
						runA(fixture.Select(fmt.Sprintf(`SELECT oid::bigint FROM %s WHERE %s=$1 AND %s=$2::regnamespace`, table, col, nspcol), []string{"oid"}, []string{"INT8"}, [][]any{{loser}}, loserName, loserSchema))
					})
				})
			}
		}
	}
}
