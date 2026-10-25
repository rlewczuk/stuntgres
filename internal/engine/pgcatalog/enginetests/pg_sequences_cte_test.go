package enginetests_test

import (
	"context"
	"database/sql/driver"
	"reflect"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
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
					e := engine.New()
					a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
					for _, q := range []string{"CREATE SEQUENCE a", "CREATE SEQUENCE b", "CREATE VIEW v AS SELECT sequencename,last_value FROM pg_sequences", "CREATE VIEW nested AS SELECT sequencename,last_value FROM v"} {
						enginetest.SessionSQL(t, a, q)
					}
					q := prefix + c.tail
					for _, prepared := range []bool{false, true} {
						tx := enginetest.TransactionBegin(t, a)
						func() {
							defer enginetest.TransactionEnd(t, a, tx, false)
							ast, description := sequencePreparedDescription(t, e, a, q)
							pgSequencesCTEHolder(t, e, a, false)
							var result core.Result
							if prepared {
								var err error
								result, err = sequencePreparedExecute(e, a, ast, &description)
								if err != nil {
									t.Fatal(err)
								}
							} else {
								result = enginetest.SessionSQL(t, a, q)
							}
							got := enginetest.Tuples(result)
							result.Rows.Close()
							if !reflect.DeepEqual(got, c.rows) {
								t.Fatalf("prepared=%v rows=%#v want %#v", prepared, got, c.rows)
							}
							pgSequencesCTEHolder(t, e, a, c.used)
						}()
						enginetest.LockEmpty(t, e)
					}
					if !c.used {
						return
					}
					// b is absent from the final rows, but must still complete behind
					// the volatile producer boundary. Observe the manager, not delay.
					tx := enginetest.TransactionBegin(t, b)
					active := true
					defer func() {
						if active {
							enginetest.TransactionEnd(t, b, tx, false)
						}
					}()
					enginetest.SessionSQL(t, b, "ALTER SEQUENCE b RESTART 31")
					ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "b")
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					worker := enginetest.LockAsync(e, a, ctx, q, false)
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
					enginetest.LockAwait(t, done, context.Canceled)
					<-finished
					enginetest.TransactionEnd(t, b, tx, false)
					active = false
					// Recovery repeats the independently expected rows, including
					// shared output from one producer, after cancel/terminal cleanup.
					pgSequencesRows(t, a, q, c.rows)
					enginetest.LockEmpty(t, e)
				})
			}
		}
	}
}

func pgSequencesCTEHolder(t *testing.T, e *core.Engine, s *session.Session, used bool) {
	t.Helper()
	for _, name := range []string{"a", "b"} {
		ref := enginetest.CatalogRef(t, e, e.CatalogRead(), name)
		lockState := e.Locks.Inspect()
		entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
		var mode lock.Mode
		if entry != nil {
			mode = entry.Holders[s.Transactions.Ticket].Mode
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

func sequencePreparedDescription(t *testing.T, e *core.Engine, s *session.Session, query string) (parser.Statement, core.StatementDescription) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	ast := enginetest.Statement(t, query)
	d, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal(err)
	}
	return ast, d
}

func sequencePreparedExecute(e *core.Engine, s *session.Session, ast parser.Statement, d *core.StatementDescription) (core.Result, error) {
	op, err := s.Acquire(context.Background())
	if err != nil {
		return core.Result{}, err
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		return core.Result{}, err
	}
	scope.Expected = d
	r, err := scope.Execute(ast, 0, nil)
	err = scope.Finish(err)
	return r, err
}

func ownedLockQueued(t *testing.T, e *core.Engine, ref ids.ObjectRef, mode lock.Mode, done <-chan error) *lock.Request {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.Locks.Inspect()
		entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
		var request *lock.Request
		if entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == mode {
			request = entry.Pending[0].Request
		}
		if request != nil {
			return request
		}
		select {
		case err := <-done:
			t.Fatalf("OWNED BY operation completed before required %d admission on %v: %v", mode, ref, err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("required exact relation admission not observed")
		}
		runtime.Gosched()
	}
}

func pgSequencesRows(t *testing.T, s *session.Session, query string, want [][]driver.Value) {
	t.Helper()
	result := enginetest.SessionSQL(t, s, query)
	defer result.Rows.Close()
	if got := enginetest.Tuples(result); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %#v want %#v", query, got, want)
	}
}
