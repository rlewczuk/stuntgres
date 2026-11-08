package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

const enumLayoutCreateType = `CREATE TYPE grind_indexed_enum AS ENUM('pending')`
const enumLayoutCreateTable = `CREATE TABLE grind_enum_indexed(status grind_indexed_enum)`
const enumLayoutCreateIndex = `CREATE INDEX grind_enum_status_idx ON grind_enum_indexed(status)`
const enumLayoutAddValue = `ALTER TYPE grind_indexed_enum ADD VALUE 'inhibited'`
const enumLayoutAddColumn = `ALTER TABLE grind_enum_indexed ADD COLUMN extra text`
const enumLayoutInsert = `INSERT INTO grind_enum_indexed(status) VALUES($1)`
const enumLayoutRead = `SELECT status::text AS status FROM grind_enum_indexed WHERE status=$1 ORDER BY grind_enum_indexed.status`

type enumLayoutCase struct {
	t *testing.T
	p *fixture.Pair
	b fixture.Backend
}

func (c enumLayoutCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.b.Recorder, s)
}
func (c enumLayoutCase) exec(e fixture.Executor, q string, n int64, args ...any) {
	c.t.Helper()
	c.run(e, fixture.Exec(q, n, args...))
}
func (c enumLayoutCase) rows(e fixture.Executor, extra bool, rows [][]any) {
	c.t.Helper()
	q := `SELECT status::text AS status FROM grind_enum_indexed ORDER BY grind_enum_indexed.status`
	columns, types := []string{"status"}, []string{"TEXT"}
	if extra {
		q = `SELECT status::text AS status,extra FROM grind_enum_indexed ORDER BY grind_enum_indexed.status`
		columns, types = []string{"status", "extra"}, []string{"TEXT", "TEXT"}
	}
	c.run(e, fixture.Select(q, columns, types, rows))
}

type enumLayoutIdentity struct{ typ, member, parent, index int64 }

func (c enumLayoutCase) identity(e fixture.Executor) enumLayoutIdentity {
	c.t.Helper()
	rows, err := e.QueryContext(c.p.Ctx, `SELECT t.oid::bigint,m.oid::bigint,'grind_enum_indexed'::regclass::bigint,'grind_enum_status_idx'::regclass::bigint FROM pg_type t JOIN pg_enum m ON m.enumtypid=t.oid WHERE t.typname='grind_indexed_enum' AND m.enumlabel='pending'`)
	if err != nil {
		c.t.Fatal(err)
	}
	defer rows.Close()
	var id enumLayoutIdentity
	if !rows.Next() {
		c.t.Fatal("missing identities")
	}
	if err := rows.Scan(&id.typ, &id.member, &id.parent, &id.index); err != nil {
		c.t.Fatal(err)
	}
	if rows.Next() {
		c.t.Fatal("duplicate identities")
	}
	if err := rows.Err(); err != nil {
		c.t.Fatal(err)
	}
	if id.typ <= 0 || id.member <= 0 || id.parent <= 0 || id.index <= 0 || id.parent == id.index {
		c.t.Fatalf("invalid identities: %+v", id)
	}
	return id
}
func (c enumLayoutCase) check(e fixture.Executor, id enumLayoutIdentity, added bool) {
	c.t.Helper()
	if got := c.identity(e); got != id {
		c.t.Fatalf("identity changed: %+v -> %+v", id, got)
	}
	labels := [][]any{{"pending", id.typ}}
	if added {
		labels = append(labels, []any{"inhibited", id.typ})
	}
	c.run(e, fixture.Select(`SELECT enumlabel,enumtypid::bigint FROM pg_enum WHERE enumtypid=$1::oid ORDER BY enumsortorder`, []string{"enumlabel", "enumtypid"}, []string{"NAME", "INT8"}, labels, id.typ))
	c.run(e, fixture.Select(`SELECT atttypid::bigint FROM pg_attribute WHERE attrelid=$1::oid AND attname='status' AND NOT attisdropped`, []string{"atttypid"}, []string{"INT8"}, [][]any{{id.typ}}, id.parent))
	c.run(e, fixture.Select(`SELECT indrelid::bigint,indkey::text,indisunique,indisvalid,indisready,indislive FROM pg_index WHERE indexrelid=$1::oid`, []string{"indrelid", "indkey", "indisunique", "indisvalid", "indisready", "indislive"}, []string{"INT8", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{id.parent, "1", false, true, true, true}}, id.index))
	c.run(e, fixture.Select(`SELECT pg_get_indexdef($1::oid) AS definition`, []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE INDEX grind_enum_status_idx ON public.grind_enum_indexed USING btree (status)"}}, id.index))
	c.run(e, fixture.Select(`SELECT objsubid,refobjsubid,deptype FROM pg_depend WHERE classid='pg_class'::regclass AND objid=$1::oid AND refclassid='pg_class'::regclass AND refobjid=$2::oid ORDER BY refobjsubid`, []string{"objsubid", "refobjsubid", "deptype"}, []string{"INT4", "INT4", "CHAR"}, [][]any{{int64(0), int64(1), "a"}}, id.index, id.parent))
}
func (c enumLayoutCase) absent(e fixture.Executor) {
	c.t.Helper()
	c.run(e, fixture.Select(`SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('grind_enum_indexed','grind_enum_status_idx')`, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	c.run(e, fixture.Select(`SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname='grind_indexed_enum'`, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
}

func TestReferenceEnumIndexLayoutGeneration(t *testing.T) {
	for _, populated := range []bool{false, true} {
		for _, goTx := range []bool{false, true} {
			for _, route := range []string{"direct", "held-before", "held-inside"} {
				t.Run(fmt.Sprintf("populated=%t/go=%t/%s", populated, goTx, route), func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, backend := range p.Backends() {
						t.Run(backend.Name, func(t *testing.T) {
							c := enumLayoutCase{t, p, backend}
							owner, peer := fixture.LockConnections(t, p.Ctx, backend.DB)
							held := map[string]*sql.Stmt{}
							prepare := func(e fixture.Executor) {
								for _, q := range []string{enumLayoutCreateType, enumLayoutCreateTable, enumLayoutCreateIndex, enumLayoutAddValue, enumLayoutAddColumn} {
									s, err := e.PrepareContext(p.Ctx, q)
									if err != nil {
										t.Fatal(q, err)
									}
									held[q] = s
								}
							}
							// Statements must close before borrowed connections return.
							defer func() {
								for _, s := range held {
									s.Close()
								}
							}()
							if route == "held-before" {
								prepare(owner)
							}
							var e fixture.Executor = owner
							var tx *sql.Tx
							if goTx {
								var err error
								tx, err = owner.BeginTx(p.Ctx, nil)
								if err != nil {
									t.Fatal(err)
								}
								defer tx.Rollback()
								e = tx
							} else {
								c.exec(owner, "BEGIN", 0)
								defer owner.ExecContext(p.Ctx, "ROLLBACK")
							}
							if route == "held-inside" {
								prepare(e)
							}
							ddl := func(q string) {
								var target fixture.Executor = e
								if s := held[q]; s != nil {
									if goTx && route == "held-before" {
										s = tx.StmtContext(p.Ctx, s)
										defer s.Close()
									}
									target = fixture.HeldStatement{Executor: e, Statement: s, SQL: q}
								}
								c.exec(target, q, 0)
							}
							ddl(enumLayoutCreateType)
							ddl(enumLayoutCreateTable)
							if populated {
								c.exec(e, enumLayoutInsert, 1, "pending")
							}
							ddl(enumLayoutCreateIndex)
							id := c.identity(e)
							c.check(e, id, false)
							insert, err := e.PrepareContext(p.Ctx, enumLayoutInsert)
							if err != nil {
								t.Fatal(err)
							}
							defer insert.Close()
							read, err := e.PrepareContext(p.Ctx, enumLayoutRead)
							if err != nil {
								t.Fatal(err)
							}
							defer read.Close()
							ddl(enumLayoutAddValue)
							c.check(e, id, true)
							ddl(enumLayoutAddColumn)
							c.check(e, id, true)
							c.absent(peer)
							if populated {
								c.exec(fixture.HeldStatement{Executor: e, Statement: insert, SQL: enumLayoutInsert}, enumLayoutInsert, 1, "inhibited")
								c.run(fixture.HeldStatement{Executor: e, Statement: read, SQL: enumLayoutRead}, fixture.Select(enumLayoutRead, []string{"status"}, []string{"TEXT"}, [][]any{{"inhibited"}}, "inhibited"))
								c.rows(e, true, [][]any{{"pending", nil}, {"inhibited", nil}})
							} else {
								c.rows(e, true, nil)
							}
							if goTx {
								if err := tx.Commit(); err != nil {
									t.Fatal(err)
								}
							} else {
								c.exec(owner, "COMMIT", 0)
							}
							c.check(peer, id, true)
							if !populated {
								c.rows(peer, true, nil)
								c.exec(peer, enumLayoutInsert, 1, "pending")
								c.exec(peer, enumLayoutInsert, 1, "inhibited")
							}
							c.rows(peer, true, [][]any{{"pending", nil}, {"inhibited", nil}})
							c.run(peer, fixture.Select(`SELECT 'pending'::grind_indexed_enum < 'inhibited'::grind_indexed_enum AS ordered,'pending'::grind_indexed_enum = 'pending'::grind_indexed_enum AS equal`, []string{"ordered", "equal"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
							c.exec(peer, `UPDATE grind_enum_indexed SET extra='updated' WHERE status='inhibited'`, 1)
							c.exec(peer, `DELETE FROM grind_enum_indexed WHERE status='pending'`, 1)
							c.rows(peer, true, [][]any{{"inhibited", "updated"}})
						})
					}
				})
			}
		}
	}
}
