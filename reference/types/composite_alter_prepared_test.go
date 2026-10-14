package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeAlterPrepared(t *testing.T) {
	for _, action := range []string{"add", "rename", "drop"} {
		for _, selection := range []string{"named", "array", "field", "expanded", "literal", "bound"} {
			t.Run(action+"_"+selection, func(t *testing.T) {
				fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE pair AS (id integer,note text)`, `CREATE TABLE holder(k integer,v pair,a pair[])`)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES(1,'(7,x)','{"(7,x)",NULL}')`, 1))
					q := `SELECT v FROM holder ORDER BY k`
					switch selection {
					case "array":
						q = `SELECT a AS v FROM holder ORDER BY k`
					case "field":
						q = `SELECT (v).note AS note FROM holder ORDER BY k`
					case "expanded":
						q = `SELECT (v).* FROM holder ORDER BY k`
					case "literal":
						q = `INSERT INTO holder(k,v) VALUES(2,'(8,y)')`
					case "bound":
						q = `INSERT INTO holder(k,v) VALUES(2,$1)`
					}
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					nativeType := ""
					if selection == "array" {
						nativeType = "_PAIR"
					}
					var eager *sql.Rows
					if selection == "named" || selection == "array" {
						eager, err = b.DB.QueryContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer eager.Close()
						fixture.EnumReferenceMetadata(b.PG, true, nativeType)(t, eager)
					}
					ddl := `ALTER TYPE pair ADD ATTRIBUTE extra text`
					value, array, input := "(7,x,)", `{"(7,x,)",NULL}`, "(8,y,z)"
					if action == "rename" {
						ddl = `ALTER TYPE pair RENAME ATTRIBUTE note TO label`
						value = "(7,x)"
						array = `{"(7,x)",NULL}`
						input = "(8,y)"
					}
					if action == "drop" {
						ddl = `ALTER TYPE pair DROP ATTRIBUTE note`
						value = "(7)"
						array = `{(7),NULL}`
						input = "(8)"
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(ddl, 0))
					if eager != nil {
						old := "(7,x)"
						if selection == "array" {
							old = `{"(7,x)",NULL}`
						}
						if !eager.Next() {
							t.Fatal(eager.Err())
						}
						var got any
						if err := eager.Scan(&got); err != nil {
							t.Fatal(err)
						}
						if fixture.Normalize(got) != old {
							t.Errorf("eager=%#v want=%q", got, old)
						}
						fixture.EnumReferenceMetadata(b.PG, true, nativeType)(t, eager)
						if eager.Next() || eager.Err() != nil {
							t.Fatal("eager tail", eager.Err())
						}
						eager.Close()
					}
					if selection == "literal" || selection == "bound" {
						var result sql.Result
						if selection == "bound" {
							result, err = stmt.ExecContext(p.Ctx, input)
						} else {
							result, err = stmt.ExecContext(p.Ctx)
						}
						// Pinned PostgreSQL preserves the analyzed literal's stable
						// attributes; current bound inputs decode the new shape.
						if err != nil {
							t.Fatalf("held %s diagnostic=%#v", selection, fixture.ExtractDiagnostic(t, err))
						}
						if n, e := result.RowsAffected(); e != nil || n != 1 {
							t.Fatal(n, e)
						}
						inserted := input
						if selection == "literal" && action == "add" {
							inserted = "(8,y,)"
						}
						fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{value}, {inserted}}, true)
						return
					}
					rows, err := stmt.QueryContext(p.Ctx)
					if err != nil {
						t.Fatalf("held diagnostic=%#v", fixture.ExtractDiagnostic(t, err))
					}
					defer rows.Close()
					want := []any{value}
					names := []string{"v"}
					if selection == "array" {
						want = []any{array}
					}
					if selection == "field" {
						want = []any{"x"}
						// The original selected slot survives as NULL after DROP.
						if action == "drop" {
							want = []any{nil}
						}
						names = []string{"note"}
					}
					if selection == "expanded" {
						want = []any{int64(7), "x"}
						names = []string{"id", "note"}
						if action == "drop" {
							want = []any{int64(7), nil}
						}
					}
					cols, e := rows.Columns()
					if e != nil || !reflect.DeepEqual(cols, names) {
						t.Errorf("columns=%v want=%v err=%v", cols, names, e)
					}
					if selection == "named" || selection == "array" {
						fixture.EnumReferenceMetadata(b.PG, true, nativeType)(t, rows)
					}
					got := make([]any, len(want))
					ptrs := make([]any, len(want))
					for i := range got {
						ptrs[i] = &got[i]
					}
					if !rows.Next() {
						t.Fatal(rows.Err())
					}
					if e := rows.Scan(ptrs...); e != nil {
						t.Fatal(e)
					}
					for i := range got {
						got[i] = fixture.Normalize(got[i])
					}
					if !reflect.DeepEqual(got, want) {
						t.Errorf("held rows=%#v want=%#v", got, want)
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal("held tail", rows.Err())
					}
				})
			})
		}
	}
}

func TestReferenceCompositeAlterAtomicity(t *testing.T) {
	for _, route := range []string{"direct", "prepared"} {
		for _, failure := range []string{"precanceled", "lock_timeout", "cancel"} {
			t.Run(route+"_"+failure, func(t *testing.T) {
				fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE pair AS (id integer,note text)`, `CREATE TABLE a OF pair`, `CREATE TABLE z OF pair`, `CREATE TABLE holder(v pair)`)
					for _, table := range []string{"a", "z"} {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO "+table+" VALUES(7,'x')", 1))
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES('(7,x)')`, 1))
					q := `ALTER TYPE pair ADD ATTRIBUTE extra text CASCADE`
					var stmt *sql.Stmt
					var err error
					if route == "prepared" {
						stmt, err = c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					exec := func(ctx context.Context) error {
						if stmt != nil {
							_, e := stmt.ExecContext(ctx)
							return e
						}
						_, e := c.ExecContext(ctx, q)
						return e
					}
					if failure == "precanceled" {
						if b.PG {
							return
						} // pq fast utility precancellation is not guaranteed.
						ctx, cancel := context.WithCancel(p.Ctx)
						cancel()
						err = exec(ctx)
						if !errors.Is(err, context.Canceled) {
							t.Fatal(err)
						}
					} else {
						tx, e := b.DB.BeginTx(p.Ctx, nil)
						if e != nil {
							t.Fatal(e)
						}
						defer tx.Rollback()
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select(`SELECT id FROM z`, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
						if failure == "lock_timeout" {
							fixture.CompositeReferenceSetup(t, p, b, c, `SET lock_timeout='50ms'`)
							err = exec(p.Ctx)
							want := fixture.CompositeReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
							if err == nil {
								t.Fatal("blocked ALTER succeeded")
							}
							if got := fixture.ExtractDiagnostic(t, err); got != want {
								t.Errorf("diagnostic=%#v want=%#v", got, want)
							}
						} else {
							fixture.RelationLockActiveCancel(t, p, b, c, q, func(ctx context.Context) fixture.RelationOutcome {
								var result sql.Result
								var e error
								if stmt != nil {
									result, e = stmt.ExecContext(ctx)
								} else {
									result, e = c.ExecContext(ctx, q)
								}
								return fixture.RelationOutcome{Result: result, Err: e}
							})
						}
						if e := tx.Rollback(); e != nil {
							t.Fatal(e)
						}
						fixture.CompositeReferenceSetup(t, p, b, c, `SET lock_timeout=0`)
					}
					for _, table := range []string{"a", "z"} {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT * FROM "+table, []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "x"}}))
					}
					fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder`, [][]any{{"(7,x)"}}, true)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
					fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder`, [][]any{{"(7,x,)"}}, false)
				})
			})
		}
	}
}
