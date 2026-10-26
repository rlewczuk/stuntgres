package core_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// The SET DEFAULT candidate genuinely consumes the callable default and volatile
// CHECK before its unique claim. The holder's child(20,1) is rolled back, so the
// only reason for the resumed action to fail is current child-owner authority.
func TestRIRoutineAuthorityAfterWait(t *testing.T) {
	for _, event := range []string{"delete", "update"} {
		for _, outcome := range []string{"owner_revoke", "invoker_revoke", "cancel", "replacement"} {
			t.Run(event+"/"+outcome, func(t *testing.T) {
				e := engine.New()
				admin, finish := enginetest.RoleOperation(t, e, "postgres")
				defer finish()
				for _, query := range []string{
					"CREATE ROLE ri_wait_owner LOGIN", "CREATE ROLE ri_wait_actor LOGIN",
					"GRANT USAGE,CREATE ON SCHEMA public TO ri_wait_owner",
					"GRANT USAGE ON SCHEMA public TO ri_wait_actor",
					"CREATE SEQUENCE public.ri_wait_steps START 1",
					"CREATE TABLE public.ri_wait_parent(id integer PRIMARY KEY)",
					"CREATE TABLE public.ri_wait_child(id integer,pid integer DEFAULT json_array_length('[1]'::json) REFERENCES public.ri_wait_parent(id) ON DELETE SET DEFAULT ON UPDATE SET DEFAULT, CHECK(nextval('public.ri_wait_steps'::regclass)>0))",
					"CREATE UNIQUE INDEX ri_wait_unique ON public.ri_wait_child(pid)",
					"INSERT INTO public.ri_wait_parent VALUES(1),(2)",
					"INSERT INTO public.ri_wait_child VALUES(10,2)",
					"ALTER TABLE public.ri_wait_child OWNER TO ri_wait_owner",
					"REVOKE CREATE ON SCHEMA public FROM ri_wait_owner",
					"GRANT USAGE ON SEQUENCE public.ri_wait_steps TO ri_wait_owner",
					"GRANT SELECT,DELETE,UPDATE ON public.ri_wait_parent TO ri_wait_actor",
					"REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM PUBLIC",
					"GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO ri_wait_owner,ri_wait_actor",
				} {
					enginetest.RoleExecute(t, e, admin, query)
				}
				actor, actorDone := enginetest.RoleOperation(t, e, "ri_wait_actor")
				defer actorDone()
				actor.Done()
				holder := enginetest.StartSession(t, e, nil)
				tx := enginetest.TransactionBegin(t, holder)
				open := true
				defer func() {
					if open {
						enginetest.TransactionEnd(t, holder, tx, false)
					}
				}()
				enginetest.SessionSQL(t, holder, "INSERT INTO public.ri_wait_child VALUES(20,1)")
				query := "DELETE FROM public.ri_wait_parent WHERE id=2"
				if event == "update" {
					query = "UPDATE public.ri_wait_parent SET id=3 WHERE id=2"
				}
				child := enginetest.CatalogRef(t, e, e.CatalogRead(), "ri_wait_child")
				definition, _ := e.CatalogRead().Relation(child.Address)
				original := definition.ColumnMeta.Defaults[definition.Attributes[1]]
				manifest := append([]ids.ObjectRef(nil), original.Dependencies...)
				done, cancel := uniqueAsyncUnit(t, actor.Session, query)
				defer cancel()
				routineWaitUniqueHeld(t, e, enginetest.CatalogRef(t, e, e.CatalogRead(), "ri_wait_unique"), actor.Session, holder.Transactions.Ticket)
				sequence := enginetest.CatalogRef(t, e, e.CatalogRead(), "ri_wait_steps")
				e.Mu.Lock()
				generator := e.Storage.Sequences[sequence.Address]
				before := *generator
				e.Mu.Unlock()
				if before.Last != 3 || !before.Called {
					t.Fatal("actual RI candidate did not consume exactly one CHECK", before)
				}
				var replacement *session.Session
				switch outcome {
				case "owner_revoke":
					enginetest.RoleExecute(t, e, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM ri_wait_owner")
				case "invoker_revoke":
					enginetest.RoleExecute(t, e, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM ri_wait_actor")
				case "cancel":
					cancel()
				case "replacement":
					ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
					defer stop()
					if err := engine.Reset(ctx, e); err != nil {
						t.Fatal(err)
					}
					replacement = enginetest.StartSession(t, e, nil)
					enginetest.SessionSQL(t, replacement, "CREATE TABLE public.ri_wait_parent(id integer PRIMARY KEY)")
					enginetest.SessionSQL(t, replacement, "CREATE TABLE public.ri_wait_child(id integer,pid integer)")
					enginetest.SessionSQL(t, replacement, "INSERT INTO public.ri_wait_parent VALUES(2)")
					enginetest.SessionSQL(t, replacement, "INSERT INTO public.ri_wait_child VALUES(10,9)")
				}
				enginetest.TransactionEnd(t, holder, tx, false)
				open = false
				var got deleteOutcome
				select {
				case got = <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("RI worker failed to join")
				}
				if outcome == "invoker_revoke" {
					if got.err != nil || got.result.Count != 1 || got.result.Rows != nil {
						t.Fatal("invoker rights replaced child-owner authority", got)
					}
				} else {
					if outcome == "owner_revoke" {
						nullCountUnitDiagnostic(t, got.err, sqlstate.Error{Code: "42501", Message: "permission denied for function json_array_length", Context: `SQL statement "UPDATE ONLY "public"."ri_wait_child" SET "pid" = DEFAULT WHERE $1 OPERATOR(pg_catalog.=) "pid""`})
					} else {
						routineWaitTerminal(t, got.err, outcome)
					}
					// This worker completes Engine.Execute, including Finish.
					// Failed admission must suppress the entire completed result;
					// the public reference fixture separately requires nil Result.
					if got.result.Rows != nil || got.result.Count != 0 {
						t.Fatal("failed RI exposed output", got.result, got.err)
					}
				}
				e.Mu.Lock()
				after := *generator
				e.Mu.Unlock()
				if after != before {
					t.Fatal("RI readmission replayed volatile truth", before, after)
				}
				uniqueClaimsEmpty(t, e)
				if outcome == "replacement" {
					routineWaitRows(t, replacement, "SELECT id FROM public.ri_wait_parent", [][]int64{{2}})
					routineWaitRows(t, replacement, "SELECT id,pid FROM public.ri_wait_child", [][]int64{{10, 9}})
					return
				}
				current, _ := e.CatalogRead().Relation(child.Address)
				if !reflect.DeepEqual(current.ColumnMeta.Defaults[current.Attributes[1]], original) || !reflect.DeepEqual(current.ColumnMeta.Defaults[current.Attributes[1]].Dependencies, manifest) {
					t.Fatal("RI mutated defining DEFAULT manifest")
				}
				wantPID, wantParent := int64(2), int64(2)
				if outcome == "invoker_revoke" {
					wantPID = 1
					if event == "delete" {
						wantParent = 0
					} else {
						wantParent = 3
					}
				}
				routineWaitRows(t, holder, "SELECT id,pid FROM public.ri_wait_child", [][]int64{{10, wantPID}})
				parents := [][]int64{{1}}
				if wantParent != 0 {
					parents = append(parents, []int64{wantParent})
				}
				routineWaitRows(t, holder, "SELECT id FROM public.ri_wait_parent ORDER BY id", parents)
				identity := enginetest.SessionSQL(t, actor.Session, "SELECT current_user")
				var value [1]sqltype.Value
				more, err := identity.Rows.Next(context.Background(), value[:])
				text, textErr := value[0].Text()
				if err != nil || !more || textErr != nil || text != "ri_wait_actor" {
					t.Fatal("RI authority escaped action", value, err, textErr)
				}
				identity.Rows.Close()
				if outcome == "owner_revoke" {
					enginetest.RoleExecute(t, e, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO ri_wait_owner")
					if result := enginetest.SessionSQL(t, actor.Session, query); result.Count != 1 {
						t.Fatal("RI regrant retry", result)
					}
					e.Mu.Lock()
					last := generator.Last
					e.Mu.Unlock()
					if last != before.Last+1 {
						t.Fatal("RI fresh retry did not consume one fresh CHECK", last)
					}
				}
			})
		}
	}
}

func TestCopyRoutineAuthorityAfterWait(t *testing.T) {
	for _, outcome := range []string{"revoke", "cancel", "replacement"} {
		t.Run(outcome, func(t *testing.T) {
			e := engine.New()
			admin, finish := enginetest.RoleOperation(t, e, "postgres")
			defer finish()
			for _, query := range []string{
				"CREATE ROLE copy_wait_owner LOGIN", "CREATE ROLE copy_wait_importer LOGIN",
				"GRANT USAGE,CREATE ON SCHEMA public TO copy_wait_owner",
				"GRANT USAGE ON SCHEMA public TO copy_wait_importer",
				"CREATE SEQUENCE public.copy_wait_steps START 1",
				"CREATE TABLE public.copy_wait_rows(k integer,j json, CONSTRAINT copy_wait_check CHECK(json_array_length(j)>0 AND nextval('public.copy_wait_steps'::regclass)>0))",
				"CREATE UNIQUE INDEX copy_wait_unique ON public.copy_wait_rows(k)",
				"ALTER TABLE public.copy_wait_rows OWNER TO copy_wait_owner",
				"REVOKE CREATE ON SCHEMA public FROM copy_wait_owner",
				"GRANT INSERT ON public.copy_wait_rows TO copy_wait_importer",
				"GRANT USAGE ON SEQUENCE public.copy_wait_steps TO copy_wait_importer",
				"REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM PUBLIC",
				"GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO copy_wait_owner,copy_wait_importer",
			} {
				enginetest.RoleExecute(t, e, admin, query)
			}
			importer, importerDone := enginetest.RoleOperation(t, e, "copy_wait_importer")
			defer importerDone()
			importer.Done()
			holder := enginetest.StartSession(t, e, nil)
			tx := enginetest.TransactionBegin(t, holder)
			open := true
			defer func() {
				if open {
					enginetest.TransactionEnd(t, holder, tx, false)
				}
			}()
			enginetest.SessionSQL(t, holder, "INSERT INTO public.copy_wait_rows VALUES(7,'[1]'::json)")
			sequence := enginetest.CatalogRef(t, e, e.CatalogRead(), "copy_wait_steps")
			e.Mu.Lock()
			generator := e.Storage.Sequences[sequence.Address]
			seed := *generator
			e.Mu.Unlock()
			if seed.Last != 1 || !seed.Called {
				t.Fatal("holder INSERT did not consume exactly one CHECK", seed)
			}
			target := enginetest.CatalogRef(t, e, e.CatalogRead(), "copy_wait_rows")
			defining := e.CatalogRead().MustRelation(target.Address)
			originalCheck := defining.Constraints.Check[0]
			checkRefs := append(originalCheck.Dependencies[:0:0], originalCheck.Dependencies...)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			syntax := enginetest.Statement(t, "COPY public.copy_wait_rows FROM STDIN").(*parser.Copy)
			done := make(chan deleteOutcome, 1)
			joined := make(chan struct{})
			go func() {
				defer close(joined)
				op, _, err := importer.Session.AcquireCommand(ctx)
				var count int64
				if err == nil {
					var scope *core.StatementScope
					scope, err = e.StartStatement(op)
					if err == nil {
						count, err = scope.Copy(syntax, strings.NewReader("7\t[1,2]\n8\t[3]\n"), nil)
						err = scope.Finish(err)
					}
					err = op.CommandError(err)
					op.Done()
				}
				done <- deleteOutcome{core.Result{Count: count}, err}
			}()
			defer func() {
				cancel()
				select {
				case <-joined:
				case <-time.After(5 * time.Second):
					t.Error("COPY worker did not join")
				}
			}()
			routineWaitUniqueHeld(t, e, enginetest.CatalogRef(t, e, e.CatalogRead(), "copy_wait_unique"), importer.Session, holder.Transactions.Ticket)
			// Both valid input rows consume CHECK during assembly. COPY's
			// buffered insertion flush then waits on the first row's unique
			// claim, after EOF, not immediately after that row's CHECK.
			const copyRows = 2
			e.Mu.Lock()
			if e.Storage.Sequences[sequence.Address] != generator {
				e.Mu.Unlock()
				t.Fatal("COPY replaced original sequence generator")
			}
			before := *generator
			e.Mu.Unlock()
			if before.Last != seed.Last+copyRows || !before.Called {
				t.Fatal("COPY buffered rows did not consume one CHECK each before wait", seed, before)
			}
			var replacement *session.Session
			switch outcome {
			case "revoke":
				enginetest.RoleExecute(t, e, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM copy_wait_importer")
			case "cancel":
				cancel()
			case "replacement":
				reset, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				if err := engine.Reset(reset, e); err != nil {
					t.Fatal(err)
				}
				replacement = enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, replacement, "CREATE TABLE public.copy_wait_rows(k integer,j json)")
				enginetest.SessionSQL(t, replacement, "INSERT INTO public.copy_wait_rows VALUES(9,'[9]'::json)")
			}
			enginetest.TransactionEnd(t, holder, tx, false)
			open = false
			var got deleteOutcome
			select {
			case got = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("COPY failed to join")
			}
			if got.result.Count != 0 || got.result.Rows != nil {
				t.Fatal("failed COPY exposed prefix", got.result)
			}
			if outcome == "revoke" {
				// CHECK truth and input conversion have already completed for
				// both rows. Buffered insertion owns only the saved origin line
				// of the first candidate, not its discarded raw input. Its
				// post-unique-wait admission error therefore has no row text.
				// Fresh input CHECK denial retains its separate row context in
				// the public pinned-oracle scenario.
				nullCountUnitDiagnostic(t, got.err, sqlstate.Error{Code: "42501", Message: "permission denied for function json_array_length", Context: "COPY copy_wait_rows, line 1"})
			} else {
				routineWaitTerminal(t, got.err, outcome)
			}
			e.Mu.Lock()
			after := *generator
			e.Mu.Unlock()
			if before != after {
				t.Fatal("COPY readmission replayed volatile truth", before, after)
			}
			uniqueClaimsEmpty(t, e)
			if outcome == "replacement" {
				routineWaitRows(t, replacement, "SELECT k FROM public.copy_wait_rows", [][]int64{{9}})
				return
			}
			current := e.CatalogRead().MustRelation(target.Address).Constraints.Check[0]
			if current.OID != originalCheck.OID || !reflect.DeepEqual(current.Dependencies, checkRefs) {
				t.Fatal("COPY replaced defining CHECK identity/manifest")
			}
			routineWaitRows(t, holder, "SELECT k FROM public.copy_wait_rows", nil)
			if outcome == "revoke" {
				enginetest.RoleExecute(t, e, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO copy_wait_importer")
				op, err := importer.Session.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				count, err := scope.Copy(syntax, strings.NewReader("7\t[1,2]\n"), nil)
				err = scope.Finish(err)
				if err != nil || count != 1 {
					t.Fatal("COPY fresh retry", count, err)
				}
				e.Mu.Lock()
				last := generator.Last
				e.Mu.Unlock()
				if last != before.Last+1 {
					t.Fatal("COPY fresh retry did not consume one CHECK", last)
				}
			}
		})
	}
}

func routineWaitTerminal(t *testing.T, err error, outcome string) {
	t.Helper()
	want := context.Canceled
	if outcome == "replacement" {
		want = ids.ErrCatalogReplaced
	}
	if !errors.Is(err, want) {
		t.Fatalf("%s: %v, want %v", outcome, err, want)
	}
}

func routineWaitRows(t *testing.T, s *session.Session, query string, want [][]int64) {
	t.Helper()
	result := enginetest.SessionSQL(t, s, query)
	defer result.Rows.Close()
	width := len(result.Rows.Schema())
	values := make([]sqltype.Value, width)
	for _, row := range want {
		more, err := result.Rows.Next(context.Background(), values)
		if err != nil || !more {
			t.Fatal("missing committed row", row, err)
		}
		for i, v := range row {
			actual, err := values[i].Integer()
			if err != nil || actual != v {
				t.Fatal("wrong committed value", values, row, err)
			}
		}
	}
	if more, err := result.Rows.Next(context.Background(), values); more || err != nil {
		t.Fatal("unexpected committed prefix", values, err)
	}
}

// The shared wait observer locates the real index/session request. Inspect its
// exact interned resource, queue ticket, owner registration and conflicting grant
// as well: a relation lock or a different unique key cannot satisfy this fence.
func routineWaitUniqueHeld(t *testing.T, e *core.Engine, index ids.ObjectRef, worker *session.Session, holder *lock.Ticket) {
	t.Helper()
	routinePendingUniqueIndex(t, e, index, worker)
	snapshot := e.Locks.Inspect()
	for resource, entry := range snapshot.Resources {
		if resource.ObjectRef != index || resource.Key() == nil || entry.Holders[holder].Mode != lock.UniqueClaim {
			continue
		}
		for _, request := range entry.Pending {
			if request.Terminal || request.Mode != lock.UniqueClaim || request.Resource != resource || request.Ticket.Ref().Session() != worker.SessionToken {
				continue
			}
			if request.Request == nil || snapshot.Owners[request.Ticket.Ref()] != request.Ticket {
				t.Fatal("UNIQUE request lacks authentic transaction ticket")
			}
			return
		}
	}
	t.Fatal("missing exact unique resource/holder/worker queue")
}
