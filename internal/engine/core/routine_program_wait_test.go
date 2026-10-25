package core_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Real UNIQUE claims stop the worker after tuple/domain preparation and the
// earlier-OID nonunique index. No expression/provider callback is fabricated.
// A genuine volatile domain CHECK supplies an observable no-truth-replay fence.
func TestRoutineProgramOriginalCallsAfterUniqueWait(t *testing.T) {
	for _, recipe := range []string{"constant_key", "false_predicate", "domain"} {
		for _, outcome := range []string{"revoke", "cancel", "replacement"} {
			t.Run(recipe+"/"+outcome, func(t *testing.T) {
				e := engine.New()
				admin, finish := enginetest.RoleOperation(t, e, "postgres")
				defer finish()
				for _, sql := range []string{
					"CREATE ROLE programme_reader LOGIN",
					"CREATE SEQUENCE public.programme_steps START 1",
					"CREATE DOMAIN public.programme_checked AS integer CHECK(nextval('public.programme_steps'::regclass)>0 AND VALUE>0)",
					"CREATE TABLE public.programme_target(k integer,v public.programme_checked)",
					"GRANT USAGE ON SCHEMA public TO programme_reader",
					"GRANT USAGE ON DOMAIN public.programme_checked TO programme_reader",
					"GRANT USAGE ON SEQUENCE public.programme_steps TO programme_reader",
					"GRANT INSERT,SELECT ON public.programme_target TO programme_reader",
				} {
					enginetest.RoleExecute(t, e, admin, sql)
				}
				if recipe != "domain" {
					definition := "CREATE INDEX programme_constant ON public.programme_target((json_array_length('[1,2]'::json)))"
					if recipe == "false_predicate" {
						definition += " WHERE FALSE"
					}
					enginetest.RoleExecute(t, e, admin, definition)
				}
				enginetest.RoleExecute(t, e, admin, "CREATE UNIQUE INDEX programme_unique ON public.programme_target(k)")
				reader, readerDone := enginetest.RoleOperation(t, e, "programme_reader")
				defer readerDone()
				reader.Done()
				holder := enginetest.StartSession(t, e, nil)
				ref := enginetest.TransactionBegin(t, holder)
				enginetest.SessionSQL(t, holder, "INSERT INTO public.programme_target VALUES(7,1)")
				done, cancel := uniqueAsyncUnit(t, reader.Session, "INSERT INTO public.programme_target VALUES(7,1)")
				enginetest.LockPending(t, e, 1)
				sequence := enginetest.CatalogRef(t, e, e.CatalogRead(), "programme_steps")
				e.Mu.Lock()
				generator := e.Storage.Sequences[sequence.Address]
				before := *generator
				e.Mu.Unlock()
				if before.Last != 2 || !before.Called {
					t.Fatal("real candidate CHECK did not execute once before wait", before)
				}
				target := "pg_catalog.json_array_length(json)"
				name := "json_array_length"
				if recipe == "domain" {
					target = "pg_catalog.int4gt(integer,integer)"
					name = "int4gt"
				}
				switch outcome {
				case "revoke":
					enginetest.RoleExecute(t, e, admin, "REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC")
				case "cancel":
					cancel()
				case "replacement":
					ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
					defer stop()
					if err := engine.Reset(ctx, e); err != nil {
						t.Fatal(err)
					}
				}
				enginetest.TransactionEnd(t, holder, ref, false)
				select {
				case got := <-done:
					if got.result.Rows != nil || got.result.Count != 0 {
						t.Fatal("failed admission exposed output", got.result)
					}
					switch outcome {
					case "revoke":
						nullCountUnitDiagnostic(t, got.err, sqlstate.Error{Code: "42501", Message: "permission denied for function " + name})
					case "cancel":
						if !errors.Is(got.err, context.Canceled) {
							t.Fatal(got.err)
						}
					case "replacement":
						if !errors.Is(got.err, ids.ErrCatalogReplaced) {
							t.Fatal(got.err)
						}
					}
				case <-time.After(5 * time.Second):
					t.Fatal("actual unique waiter failed to join")
				}
				e.Mu.Lock()
				after := *generator
				e.Mu.Unlock()
				if after != before {
					t.Fatal("readmission replayed actual volatile CHECK truth", before, after)
				}
				uniqueClaimsEmpty(t, e)
				if outcome == "replacement" {
					return
				}
				rows := enginetest.SessionSQL(t, holder, "SELECT k FROM public.programme_target")
				var values [1]sqltype.Value
				if more, err := rows.Rows.Next(context.Background(), values[:]); err != nil || more {
					t.Fatal("denied candidate published", more, err)
				}
				rows.Rows.Close()
				enginetest.RoleExecute(t, e, admin, "GRANT EXECUTE ON FUNCTION "+target+" TO PUBLIC")
				if result := enginetest.SessionSQL(t, reader.Session, "INSERT INTO public.programme_target VALUES(7,1)"); result.Count != 1 {
					t.Fatal("fresh regrant retry", result)
				}
				e.Mu.Lock()
				last := generator.Last
				e.Mu.Unlock()
				if last != 3 {
					t.Fatal("regrant retry did not execute exactly one fresh CHECK", fmt.Sprint(last))
				}
			})
		}
	}
}

// A benign catalog publication forces a real rebind between two different
// UNIQUE claims. Revocation at the second real wait must still see the original
// consumed constant call, including a key planned behind a false predicate.
func TestRoutineProgramOriginalCallsAfterSecondUniqueWait(t *testing.T) {
	for _, falsePredicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "constant_key", true: "false_predicate"}[falsePredicate], func(t *testing.T) {
			e := engine.New()
			admin, finish := enginetest.RoleOperation(t, e, "postgres")
			defer finish()
			for _, sql := range []string{
				"CREATE ROLE second_wait_reader LOGIN",
				"CREATE SEQUENCE public.second_wait_steps START 1",
				"CREATE DOMAIN public.second_wait_checked AS integer CHECK(nextval('public.second_wait_steps'::regclass)>0 AND VALUE>0)",
				"CREATE TABLE public.second_wait_rows(k integer,j integer,v public.second_wait_checked)",
				"GRANT USAGE ON SCHEMA public TO second_wait_reader",
				"GRANT USAGE ON DOMAIN public.second_wait_checked TO second_wait_reader",
				"GRANT USAGE ON SEQUENCE public.second_wait_steps TO second_wait_reader",
				"GRANT INSERT,SELECT ON public.second_wait_rows TO second_wait_reader",
			} {
				enginetest.RoleExecute(t, e, admin, sql)
			}
			indexSQL := "CREATE INDEX second_wait_constant ON public.second_wait_rows((json_array_length('[1,2]'::json)))"
			if falsePredicate {
				indexSQL += " WHERE FALSE"
			}
			enginetest.RoleExecute(t, e, admin, indexSQL)
			enginetest.RoleExecute(t, e, admin, "CREATE UNIQUE INDEX second_wait_first ON public.second_wait_rows(k)")
			enginetest.RoleExecute(t, e, admin, "CREATE UNIQUE INDEX second_wait_second ON public.second_wait_rows(j)")
			firstIndex := enginetest.CatalogRef(t, e, e.CatalogRead(), "second_wait_first")
			secondIndex := enginetest.CatalogRef(t, e, e.CatalogRead(), "second_wait_second")
			reader, readerDone := enginetest.RoleOperation(t, e, "second_wait_reader")
			defer readerDone()
			reader.Done()
			firstHolder, secondHolder := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			firstTx, secondTx := enginetest.TransactionBegin(t, firstHolder), enginetest.TransactionBegin(t, secondHolder)
			firstOpen, secondOpen := true, true
			defer func() {
				if firstOpen {
					enginetest.TransactionEnd(t, firstHolder, firstTx, false)
				}
			}()
			defer func() {
				if secondOpen {
					enginetest.TransactionEnd(t, secondHolder, secondTx, false)
				}
			}()
			enginetest.SessionSQL(t, firstHolder, "INSERT INTO public.second_wait_rows VALUES(7,8,1)")
			enginetest.SessionSQL(t, secondHolder, "INSERT INTO public.second_wait_rows VALUES(6,9,1)")
			done, cancel := uniqueAsyncUnit(t, reader.Session, "INSERT INTO public.second_wait_rows VALUES(7,9,1)")
			defer cancel()
			routinePendingUniqueIndex(t, e, firstIndex, reader.Session)
			sequence := enginetest.CatalogRef(t, e, e.CatalogRead(), "second_wait_steps")
			e.Mu.Lock()
			generator := e.Storage.Sequences[sequence.Address]
			before := generator.Last
			e.Mu.Unlock()
			if before != 3 {
				t.Fatal("candidate CHECK did not execute exactly once before first wait", before)
			}
			// This changes the actual catalog snapshot without changing either
			// original parent/index identity or any routine rights.
			enginetest.RoleExecute(t, e, admin, "CREATE SEQUENCE public.second_wait_catalog_drift")
			enginetest.TransactionEnd(t, firstHolder, firstTx, false)
			firstOpen = false
			routinePendingUniqueIndex(t, e, secondIndex, reader.Session)
			enginetest.RoleExecute(t, e, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM PUBLIC")
			enginetest.TransactionEnd(t, secondHolder, secondTx, false)
			secondOpen = false
			select {
			case got := <-done:
				if got.result.Rows != nil || got.result.Count != 0 {
					t.Fatal("second wait exposed a data/output prefix", got.result)
				}
				nullCountUnitDiagnostic(t, got.err, sqlstate.Error{Code: "42501", Message: "permission denied for function json_array_length"})
			case <-time.After(5 * time.Second):
				t.Fatal("second real waiter did not join")
			}
			e.Mu.Lock()
			after := generator.Last
			e.Mu.Unlock()
			if after != before {
				t.Fatal("rebind/readmission replayed volatile CHECK truth", before, after)
			}
			uniqueClaimsEmpty(t, e)
			rows := enginetest.SessionSQL(t, firstHolder, "SELECT k FROM public.second_wait_rows")
			var values [1]sqltype.Value
			if more, err := rows.Rows.Next(context.Background(), values[:]); err != nil || more {
				t.Fatal("denied second-wait candidate published", more, err)
			}
			rows.Rows.Close()
			enginetest.RoleExecute(t, e, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO PUBLIC")
			if result := enginetest.SessionSQL(t, reader.Session, "INSERT INTO public.second_wait_rows VALUES(7,9,1)"); result.Count != 1 {
				t.Fatal("fresh second-wait regrant retry", result)
			}
			e.Mu.Lock()
			last := generator.Last
			e.Mu.Unlock()
			if last != 4 {
				t.Fatal("fresh retry did not execute exactly one new CHECK", last)
			}
		})
	}
}

func routinePendingUniqueIndex(t *testing.T, e *engine.Engine, index ids.ObjectRef, worker *session.Session) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.Locks.Inspect()
		found := false
		for resource, entry := range lockState.Resources {
			if resource.ObjectRef != index {
				continue
			}
			for _, request := range entry.Pending {
				if !request.Terminal && request.Ticket.Ref().Session() == worker.SessionToken && request.Mode == lock.UniqueClaim {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if found {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("missing exact actual UNIQUE index waiter", index.Address)
		}
		runtime.Gosched()
	}
}
