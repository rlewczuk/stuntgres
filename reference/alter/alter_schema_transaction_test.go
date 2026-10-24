package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterSchemaTransactionBoundary(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "s1", "s2")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			r := backend.Recorder
			referenceAlterSchemaSetup(t, p, c, r)
			before := ""
			if !backend.PG {
				before = referenceDDLNativeDump(t, p)
			}
			utility := "ALTER TABLE s1.t SET SCHEMA s2"
			if !backend.PG {
				canceled, cancel := context.WithCancel(p.Ctx)
				cancel()
				_, err = c.ExecContext(canceled, utility)
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
			// pq Conn simple-protocol Exec may execute with an already-canceled context.
			// Native retains its canceled/no-effects phase; PG cancellation is witnessed
			// independently by the retained tagged server-cancel scenario.
			held, err := c.PrepareContext(p.Ctx, utility)
			if err != nil {
				t.Fatal("autocommit utility Prepare", err)
			}
			defer held.Close()
			for _, readOnly := range []bool{false, true} {
				for _, route := range []string{"prepare_in_transaction", "direct", "held_prepared"} {
					func() {
						tx, e := c.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: readOnly})
						if e != nil {
							t.Fatal(e)
						}
						defer tx.Rollback()
						x := fixture.Executor(tx)
						prepared := route == "prepare_in_transaction"
						if route == "held_prepared" {
							x = fixture.HeldStatement{c, held, utility}
						}
						if !readOnly {
							s := fixture.Exec(utility, 0)
							s.Prepared = prepared
							fixture.RunStep(t, p.Ctx, x, r, s)
							fixture.RunStep(t, p.Ctx, tx, r, referenceAlterSchemaRows("SELECT id,note FROM s2.t ORDER BY id"))
						} else {
							code, message, stage := "25006", "cannot execute ALTER TABLE in a read-only transaction", "execute"
							fixture.RunStep(t, p.Ctx, x, r, fixture.RoleReferenceError(utility, code, message, "", prepared, stage))
							follow := fixture.RoleReferenceError("SELECT id FROM s1.t", "25P02", "current transaction is aborted, commands ignored until end of transaction block", "", false, "execute")
							follow.SelectRows = true
							fixture.RunStep(t, p.Ctx, tx, r, follow)
						}
						if e = tx.Rollback(); e != nil {
							t.Fatal(e)
						}
						fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT id FROM s1.t WHERE id=7", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					}()
				}
			}
			if err = held.Close(); err != nil {
				t.Fatal(err)
			}
			if err = c.Close(); err != nil {
				t.Fatal(err)
			}
			if !backend.PG && referenceDDLNativeDump(t, p) != before {
				t.Fatal("rolled-back or canceled schema utility changed persistent closure")
			}
		})
	}
}
