package engine

import (
	"context"
	"database/sql/driver"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/lock"
)

func TestPGSequencesCTE(t *testing.T) {
	for _, source := range []string{"pg_sequences", "(SELECT sequencename,last_value FROM pg_sequences) d", "v", "nested"} {
		for _, hint := range []string{"", "MATERIALIZED", "NOT MATERIALIZED"} {
			prefix := "WITH q AS " + hint + " (SELECT sequencename,last_value FROM " + source + ") "
			for _, c := range []struct {
				name, tail string
				rows       [][]driver.Value
				used       bool
			}{
				{"single", "SELECT sequencename,last_value FROM q WHERE sequencename='a'", [][]driver.Value{{"a", nil}}, true},
				{"shared", "SELECT x.sequencename,x.last_value,y.last_value FROM q x JOIN q y ON x.sequencename=y.sequencename WHERE x.sequencename='a' AND y.sequencename='a'", [][]driver.Value{{"a", nil, nil}}, true},
				{"unused", "SELECT 7 AS n", [][]driver.Value{{int64(7)}}, false},
			} {
				t.Run(source+"/"+hint+"/"+c.name, func(t *testing.T) {
					e := New()
					a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
					for _, q := range []string{"CREATE SEQUENCE a", "CREATE SEQUENCE b", "CREATE VIEW v AS SELECT sequencename,last_value FROM pg_sequences", "CREATE VIEW nested AS SELECT sequencename,last_value FROM v"} {
						sessionSQL(t, a, q)
					}
					q := prefix + c.tail
					for _, prepared := range []bool{false, true} {
						tx := transactionBegin(t, a)
						func() {
							defer transactionEnd(t, a, tx, false)
							ast, description := sequencePreparedDescription(t, e, a, q)
							pgSequencesCTEHolder(t, e, a, false)
							var result Result
							if prepared {
								var err error
								result, err = sequencePreparedExecute(e, a, ast, &description)
								if err != nil {
									t.Fatal(err)
								}
							} else {
								result = sessionSQL(t, a, q)
							}
							got := tuples(result)
							result.Rows.Close()
							if !reflect.DeepEqual(got, c.rows) {
								t.Fatalf("prepared=%v rows=%#v want %#v", prepared, got, c.rows)
							}
							pgSequencesCTEHolder(t, e, a, c.used)
						}()
						lockEmpty(t, e)
					}
					if !c.used {
						return
					}
					// b is absent from the final rows, but must still complete behind
					// the volatile producer boundary. Observe the manager, not delay.
					tx := transactionBegin(t, b)
					active := true
					defer func() {
						if active {
							transactionEnd(t, b, tx, false)
						}
					}()
					sessionSQL(t, b, "ALTER SEQUENCE b RESTART 31")
					ref := catalogRef(t, e, e.CatalogRead(), "b")
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					worker := lockAsync(e, a, ctx, q, false)
					done, finished := make(chan error, 1), make(chan struct{})
					go func() {
						done <- <-worker
						close(finished)
					}()
					defer func() {
						cancel()
						<-finished
					}()
					ownedLockQueued(t, e, ref, lock.RowExclusive, done)
					cancel()
					lockAwait(t, done, context.Canceled)
					<-finished
					transactionEnd(t, b, tx, false)
					active = false
					// Recovery repeats the independently expected rows, including
					// shared output from one producer, after cancel/terminal cleanup.
					pgSequencesRows(t, a, q, c.rows)
					lockEmpty(t, e)
				})
			}
		}
	}
}

func pgSequencesCTEHolder(t *testing.T, e *Engine, s *Session, used bool) {
	t.Helper()
	for _, name := range []string{"a", "b"} {
		ref := catalogRef(t, e, e.CatalogRead(), name)
		lockState := e.locks.Inspect()
		entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
		var mode lock.Mode
		if entry != nil {
			mode = entry.Holders[s.transactions.ticket].Mode
		}
		var want lock.Mode
		if used {
			want = lock.RowExclusive
		}
		if mode != want {
			t.Errorf("sequence %s helper mode=%v want %v (used=%v)", name, mode, want, used)
		}
	}
}
