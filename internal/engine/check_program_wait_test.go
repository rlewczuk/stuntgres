package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func checkProgramExpression(folded bool) string {
	if folded {
		return "pg_catalog.json_array_length('[1,2]'::json)>0"
	}
	return "pg_catalog.json_array_length(j)>0"
}

func checkProgramRows(t *testing.T, e *Engine, query string, want ...int64) {
	t.Helper()
	s := sessionStart(t, e, nil)
	r := sessionSQL(t, s, query)
	defer r.Rows.Close()
	var values [1]sqltype.Value
	for _, expected := range want {
		more, err := r.Rows.Next(context.Background(), values[:])
		value, valueErr := values[0].Integer()
		if err != nil || valueErr != nil || !more || value != expected {
			t.Fatal("committed row mismatch", values, more, err, valueErr, expected)
		}
	}
	if more, err := r.Rows.Next(context.Background(), values[:]); err != nil || more {
		t.Fatal("unexpected published prefix", more, err)
	}
}

func TestCheckProgramOriginalCallsAfterUniqueWait(t *testing.T) {
	for _, folded := range []bool{false, true} {
		for _, canceled := range []bool{false, true} {
			t.Run(map[bool]string{false: "row", true: "folded"}[folded]+"/"+map[bool]string{false: "revoke", true: "cancel"}[canceled], func(t *testing.T) {
				e := New()
				admin, finish := roleOperation(t, e, "postgres")
				defer finish()
				for _, q := range []string{
					"CREATE ROLE check_reader LOGIN",
					"CREATE SEQUENCE public.check_steps START 1",
					"CREATE TABLE public.check_rows(k INT,j JSON,CONSTRAINT a_call CHECK(" + checkProgramExpression(folded) + "),CONSTRAINT z_truth CHECK(nextval('public.check_steps'::regclass)>0))",
					"CREATE UNIQUE INDEX check_unique ON public.check_rows(k)",
					"INSERT INTO public.check_rows VALUES(99,'[9]')",
					"ALTER SEQUENCE public.check_steps RESTART WITH 1",
					"GRANT USAGE ON SCHEMA public TO check_reader",
					"GRANT USAGE ON SEQUENCE public.check_steps TO check_reader",
					"GRANT INSERT,SELECT ON public.check_rows TO check_reader",
				} {
					roleExecute(t, e, admin, q)
				}
				reader, readerDone := roleOperation(t, e, "check_reader")
				defer readerDone()
				reader.Done()
				holder := sessionStart(t, e, nil)
				tx := transactionBegin(t, holder)
				open := true
				defer func() {
					if open {
						transactionEnd(t, holder, tx, false)
					}
				}()
				sessionSQL(t, holder, "INSERT INTO public.check_rows VALUES(7,'[1]')")
				done, cancel := uniqueAsyncUnit(t, reader.Session, "INSERT INTO public.check_rows VALUES(8,'[1,2]'),(7,'[1,2]') RETURNING k")
				defer cancel()
				index := catalogRef(t, e, e.CatalogRead(), "check_unique")
				routinePendingUniqueIndex(t, e, index, reader.Session)
				sequence := catalogRef(t, e, e.CatalogRead(), "check_steps")
				e.Mu.Lock()
				generator := e.Storage.Sequences[sequence.Address]
				before := *generator
				e.Mu.Unlock()
				if before.Last != 3 || !before.Called {
					t.Fatal("CHECK truth must precede unique queue exactly once per candidate", before)
				}
				if canceled {
					cancel()
				} else {
					roleExecute(t, e, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM PUBLIC")
					transactionEnd(t, holder, tx, false)
					open = false
				}
				select {
				case got := <-done:
					if got.result.Rows != nil {
						defer got.result.Rows.Close()
					}
					if got.result.Rows != nil || got.result.Count != 0 {
						t.Fatal("failed CHECK readmission exposed output", got.result)
					}
					if canceled {
						if !errors.Is(got.err, context.Canceled) {
							t.Fatal(got.err)
						}
					} else {
						nullCountUnitDiagnostic(t, got.err, sqlstate.Error{Code: "42501", Message: "permission denied for function json_array_length"})
					}
				case <-time.After(5 * time.Second):
					t.Fatal("CHECK unique waiter did not join")
				}
				lockPending(t, e, 0)
				owner := reader.Session.Transactions
				owner.Mu.Lock()
				retainedWrites, retainedDeletes := len(owner.Writes), len(owner.Deletes)
				retainedCatalog := owner.Catalog
				owner.Mu.Unlock()
				if retainedWrites != 0 || retainedDeletes != 0 || retainedCatalog != nil {
					t.Fatal("terminal worker retained a private journal", retainedWrites, retainedDeletes, retainedCatalog)
				}
				if canceled {
					transactionEnd(t, holder, tx, false)
					open = false
				}
				e.Mu.Lock()
				after := *generator
				e.Mu.Unlock()
				if after != before {
					t.Fatal("readmission replayed volatile truth", before, after)
				}
				uniqueClaimsEmpty(t, e)
				checkProgramRows(t, e, "SELECT k FROM public.check_rows ORDER BY k", 99)
				roleExecute(t, e, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO PUBLIC")
				if r := sessionSQL(t, reader.Session, "INSERT INTO public.check_rows VALUES(8,'[1]'),(7,'[1]')"); r.Count != 2 {
					t.Fatal(r)
				}
				checkProgramRows(t, e, "SELECT k FROM public.check_rows ORDER BY k", 7, 8, 99)
				e.Mu.Lock()
				last := generator.Last
				e.Mu.Unlock()
				if last != 5 {
					t.Fatal("fresh retry did not evaluate exactly two new rows", last)
				}
			})
		}
	}
}
