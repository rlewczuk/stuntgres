package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestTriggerDeferredTerminalAtomicity(t *testing.T) {
	for _, boundary := range []string{"commit", "native_commit", "native_finished_lease", "sql_commit", "late_body_error", "late_cancel", "finish", "prepare"} {
		t.Run(boundary, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			e, s, read, relation, procedure := triggerUnitFixtureContext(t, ctx, `BEGIN
			 CREATE TABLE deferred_owned_audit(id integer);
			 INSERT INTO deferred_owned_audit VALUES(NEW.id);
			 CREATE OR REPLACE FUNCTION deferred_owned_helper() RETURNS integer LANGUAGE plpgsql AS $body$BEGIN RETURN 99; END$body$;
			 RETURN NEW; END`)
			integer, _ := read.LookupType(sqltype.Int4)
			original := triggerUnitProcedure(t, e, s, "deferred_owned_helper", 9500004, integer, proc.OrdinaryInvocation, "BEGIN RETURN 7; END")
			definition := deferredUnitDefinition(t, read, relation, procedure)
			definitions := []stored.Trigger{definition}
			if boundary == "late_body_error" {
				trigger, _ := read.LookupType(2279)
				bad := triggerUnitProcedure(t, e, s, "deferred_owned_fail", 9500005, trigger, proc.DMLTriggerInvocation, "BEGIN PERFORM 1/0; RETURN NEW; END")
				p := definition.Parts()
				p.OID, p.Name, p.Constraint.OID, p.Constraint.Name = 9500012, "z_fail", 9500013, "z_fail"
				p.Procedure, p.References = read.Owner().Domain.Ref(read.Epoch(), bad.Address()), []ids.ObjectAddress{bad.Address()}
				failing, err := stored.NewTrigger(p)
				if err != nil {
					t.Fatal(err)
				}
				definitions = append(definitions, failing)
			}
			relation = triggerUnitInstall(t, e, relation, definitions...)
			triggerUnitAdmit(t, s, relation)
			if boundary != "finish" {
				s.Owner.State = session.TxActive
			}
			deferredUnitAccept(t, s, read, relation, 1)
			ref := s.TxRef
			published := e.Catalog.State
			if len(e.Storage.Relations[relation.Address()].Versions) != 0 || e.CatalogRead().Get(original.Address()) != original {
				t.Fatal("acceptance leaked data/catalog before terminal boundary")
			}
			if boundary == "commit" || boundary == "late_cancel" {
				if err := core.ScopeSettleTriggerTerminalTest(s); err != nil {
					t.Fatal(err)
				}
				e.Mu.Lock()
				private := e.PrivateCatalogLocked(s.PendingCatalog())
				replacement, _ := private.Get(original.Address()).(*catalog.Procedure)
				auditAddress := private.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: relation.Namespace(), Name: "deferred_owned_audit"})
				e.Mu.Unlock()
				if core.ScopeCurrentSnapshot(s) == nil || s.CatalogAuthority == nil || replacement == nil || replacement.SQL == original.SQL || replacement.SQL.Source != "BEGIN RETURN 99; END" || s.Owner.Catalog == nil {
					t.Fatal("terminal body lacks current snapshot, authority or real catalog journal")
				}
				if len(s.Owner.Writes[relation.Address()]) != 1 || len(s.Owner.Writes[auditAddress]) != 1 || len(s.Stage.Rows) != 0 {
					t.Fatal("terminal segment did not transfer both real row effects exactly once")
				}
				if e.Catalog.State != published || len(e.Storage.Relations[relation.Address()].Versions) != 0 || e.CatalogRead().Get(original.Address()) != original {
					t.Fatal("settlement published before the combined terminal frontier")
				}
				if boundary == "late_cancel" {
					cancel()
				}
			}
			var err error
			switch boundary {
			case "finish":
				err = s.Finish(nil)
			case "prepare":
				// The same live scope executes the incumbent PREPARE boundary;
				// assertions concern transferred owners, never SQL result rows.
				_, err = s.Execute(enginetest.Statement(t, "PREPARE TRANSACTION 'deferred_owner'"), 0, nil)
				if err == nil {
					err = s.Finish(nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				if e.Catalog.State != published || len(e.Storage.Relations[relation.Address()].Versions) != 0 || !core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Owner)).Zero() {
					t.Fatal("PREPARE published or retained executable obligations")
				}
				if core.EnginePreparedRef(e).Len() != 1 {
					t.Fatal("missing prepared owner")
				}
				if err := core.EngineCompletePrepared(e, s.Op, "deferred_owner", true); err != nil {
					t.Fatal(err)
				}
			case "native_commit", "native_finished_lease":
				if err := s.Finish(nil); err != nil {
					t.Fatal(err)
				}
				op := s.Op
				if boundary == "native_commit" {
					actor := op.Session
					op.Done()
					var acquireErr error
					op, acquireErr = actor.Acquire(ctx)
					if acquireErr != nil {
						t.Fatal(acquireErr)
					}
					defer op.Done()
				}
				lease, sqlFinished := op.Lease, op.SQLFinished
				_, err = e.Commit(op, ref)
				if op.Lease != lease || op.SQLFinished != sqlFinished {
					t.Fatal("native terminal did not restore its original operation lease")
				}
			case "sql_commit":
				_, err = s.Execute(enginetest.Statement(t, "COMMIT"), 0, nil)
				if finishErr := s.Finish(err); !errors.Is(finishErr, err) {
					t.Fatal(finishErr)
				}
			default:
				err = core.ScopeEndTriggerTerminalTest(s)
				if finishErr := s.Finish(err); !errors.Is(finishErr, err) {
					t.Fatal("terminal cleanup replaced primary", finishErr, err)
				}
			}
			failed := boundary == "late_body_error" || boundary == "late_cancel"
			if failed {
				if boundary == "late_cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal("late cancellation lost", err)
				}
				if boundary == "late_body_error" {
					var state interface{ SQLState() string }
					if !errors.As(err, &state) || state.SQLState() != "22012" {
						t.Fatal("late body failure lost", err)
					}
				}
				if e.Catalog.State != published || e.CatalogRead().Get(original.Address()) != original || len(e.Storage.Relations[relation.Address()].Versions) != 0 {
					t.Fatal("failed terminal body exposed original or earlier body effects")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				installed, _ := e.CatalogRead().Get(original.Address()).(*catalog.Procedure)
				if installed == original || installed.SQL == original.SQL || installed.SQL.Source != "BEGIN RETURN 99; END" || len(e.Storage.Relations[relation.Address()].Versions) != 1 {
					t.Fatal("combined publication lost catalog/data or duplicated original row")
				}
				audit := enginetest.RoleTableRef(t, e, "deferred_owned_audit")
				if len(e.Storage.Relations[audit.Address].Versions) != 1 {
					t.Fatal("body writes were lost or published twice")
				}
			}
			if s.Owner.Ref == ref || !core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Owner)).Zero() || core.ScopeIndexesSlot(s) != nil || s.CatalogAuthority != nil {
				t.Fatal("terminal completion retained old incarnation/event/authority")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
