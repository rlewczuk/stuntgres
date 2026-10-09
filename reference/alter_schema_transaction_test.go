package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestReferenceAlterSchemaTransactionBoundary(t *testing.T) {
	p := newReferenceScenario(t)
	schemaReferenceCleanup(t, "s1", "s2")
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			r := backend.recorder
			referenceAlterSchemaSetup(t, p, c, r)
			before := ""
			if !backend.pg {
				before = referenceDDLNativeDump(t, p)
			}
			utility := "ALTER TABLE s1.t SET SCHEMA s2"
			if !backend.pg {
				canceled, cancel := context.WithCancel(p.ctx)
				cancel()
				_, err = c.ExecContext(canceled, utility)
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
			// pq Conn simple-protocol Exec may execute with an already-canceled context.
			// Native retains its canceled/no-effects phase; PG cancellation is witnessed
			// independently by the retained tagged server-cancel scenario.
			held, err := c.PrepareContext(p.ctx, utility)
			if err != nil {
				t.Fatal("autocommit utility Prepare", err)
			}
			defer held.Close()
			for _, readOnly := range []bool{false, true} {
				for _, route := range []string{"prepare_in_transaction", "direct", "held_prepared"} {
					func() {
						tx, e := c.BeginTx(p.ctx, &sql.TxOptions{ReadOnly: readOnly})
						if e != nil {
							t.Fatal(e)
						}
						defer tx.Rollback()
						x := referenceExecutor(tx)
						prepared := route == "prepare_in_transaction"
						if route == "held_prepared" {
							x = referenceHeldStatement{c, held, utility}
						}
						if backend.pg && !readOnly {
							s := referenceExec(utility, 0)
							s.prepared = prepared
							referenceRunStep(t, p.ctx, x, r, s)
							referenceRunStep(t, p.ctx, tx, r, referenceAlterSchemaRows("SELECT id,note FROM s2.t ORDER BY id"))
						} else {
							code, message, stage := "0A000", "statement is unsupported in an explicit transaction", "execute"
							if prepared && !backend.pg {
								stage = "prepare"
							} else if readOnly {
								code, message = "25006", "cannot execute ALTER TABLE in a read-only transaction"
							}
							referenceRunStep(t, p.ctx, x, r, roleReferenceError(utility, code, message, "", prepared, stage))
							follow := roleReferenceError("SELECT id FROM s1.t", "25P02", "current transaction is aborted, commands ignored until end of transaction block", "", false, "execute")
							follow.selectRows = true
							referenceRunStep(t, p.ctx, tx, r, follow)
						}
						if e = tx.Rollback(); e != nil {
							t.Fatal(e)
						}
						referenceRunStep(t, p.ctx, c, r, referenceSelect("SELECT id FROM s1.t WHERE id=7", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					}()
				}
			}
			if err = held.Close(); err != nil {
				t.Fatal(err)
			}
			if err = c.Close(); err != nil {
				t.Fatal(err)
			}
			if !backend.pg && referenceDDLNativeDump(t, p) != before {
				t.Fatal("rejected or canceled schema utility changed persistent closure")
			}
		})
	}
}
