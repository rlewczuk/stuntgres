package core_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// SQL only supplies genuine routine descriptors here. The witnesses are the
// statement's actual journal, staged descriptor/storage and runtime owners;
// consumer-visible validator outcomes belong to the reference scenarios.
func TestFDWSQLValidatorJournalOwnership(t *testing.T) {
	for _, populated := range []bool{false, true} {
		for _, outcome := range []string{"success", "body_error", "null_result"} {
			t.Run(fmt.Sprintf("populated=%t/%s", populated, outcome), func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				for _, source := range []string{
					"CREATE ROLE fdw_journal_owner",
					"GRANT USAGE ON SCHEMA public TO fdw_journal_owner",
					"CREATE FUNCTION fdw_journal_accept(text[], oid) RETURNS integer LANGUAGE SQL SECURITY DEFINER SET application_name='validator-private' AS 'SELECT $2::integer'",
					"CREATE FUNCTION fdw_journal_error(text[], oid) RETURNS integer LANGUAGE SQL SECURITY DEFINER SET application_name='validator-private' AS 'SELECT 1/($2::integer-1417)'",
					"CREATE FUNCTION fdw_journal_null(text[], oid) RETURNS integer LANGUAGE SQL SECURITY DEFINER SET application_name='validator-private' AS 'SELECT NULL::integer'",
					"ALTER FUNCTION fdw_journal_accept(text[], oid) OWNER TO fdw_journal_owner",
					"ALTER FUNCTION fdw_journal_error(text[], oid) OWNER TO fdw_journal_owner",
					"ALTER FUNCTION fdw_journal_null(text[], oid) OWNER TO fdw_journal_owner",
				} {
					enginetest.SessionSQL(t, s, source)
				}
				read := e.CatalogRead()
				accept := routineTreeProcedure(t, read, "fdw_journal_accept")
				reject := accept
				if outcome == "body_error" {
					reject = routineTreeProcedure(t, read, "fdw_journal_error")
				} else if outcome == "null_result" {
					reject = routineTreeProcedure(t, read, "fdw_journal_null")
				}
				op := enginetest.TransactionOperation(t, s)
				defer op.Done()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer scope.Finish(errors.New("discard unfinished ownership witness"))
				if err := scope.Gate(&parser.CreateTable{}); err != nil {
					t.Fatal(err)
				}
				owner := scope.Owner
				ownerJournal := owner.Catalog
				if scope.Catalog != nil {
					t.Fatal("fixture already admitted a statement journal")
				}
				// Gate establishes transaction ownership, not a query's identity
				// capture. Start with the real caller capture rather than treating
				// the zero-value, not-yet-demanded snapshot as caller identity.
				e.Mu.Lock()
				caller, err := scope.RoutineAuthorizationLocked()
				if err == nil {
					err = scope.CaptureIdentityLocked(caller)
				}
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				roles, effective := op.RoleState(), op.EffectiveUser
				identity := scope.Identity
				settings := op.EffectiveSettings()
				level := settings.NestLevel
				app, ok := op.Setting("application_name")
				if !ok {
					t.Fatal("missing caller application_name")
				}
				demand := core.ScopeSQLRoutine(scope)
				input, output, rowSecurity := scope.Input, scope.Output, scope.RowSecurity
				assertUnwound := func(wantInput sqltype.InputEnvironment) {
					t.Helper()
					afterApp, settingOK := op.Setting("application_name")
					if !settingOK || afterApp != app {
						t.Errorf("application_name: got %q (present=%t), want %q", afterApp, settingOK, app)
					}
					if got := op.EffectiveSettings(); got != settings {
						t.Errorf("settings owner: got %p, want %p", got, settings)
					}
					// The first SAVE establishes transaction base level one;
					// only root Finish returns the idle store to level zero.
					if got, want := settings.NestLevel, max(level, 1); got != want {
						t.Errorf("settings SAVE level: got %d, want transaction base %d", got, want)
					}
					if got := op.RoleState(); got != roles {
						t.Errorf("caller roles: got %+v, want %+v", got, roles)
					}
					if op.EffectiveUser != effective {
						t.Errorf("effective actor: got %p, want %p", op.EffectiveUser, effective)
					}
					if !reflect.DeepEqual(scope.Identity, identity) {
						t.Errorf("caller identity: got %#v, want %#v", scope.Identity, identity)
					}
					if got := core.ScopeActiveSQLRoutineDepth(scope); got != 0 {
						t.Errorf("routine depth: got %d, want 0", got)
					}
					if !core.SameSQLRoutineDemand(demand, core.ScopeSQLRoutine(scope)) {
						t.Error("validator changed enclosing routine demand")
					}
					// Ordinary routine exit recaptures catalog inputs under caller
					// authority; closure identity is not presentation equality.
					routineTriggerPresentationFields(t, "input", scope.Input, wantInput)
					routineTriggerPresentationFields(t, "output", scope.Output, output)
					for _, id := range []sqltype.ID{sqltype.Int4, sqltype.Text} {
						typ, _ := read.LookupType(id)
						if scope.Input.TypeDiagnosticName == nil {
							t.Error("validator lost caller type-name capture")
						} else if got, want := scope.Input.TypeDiagnosticName(id), typ.DiagnosticName(); got != want {
							t.Errorf("input.TypeDiagnosticName(%v): got %q, want %q", id, got, want)
						}
					}
					if scope.RowSecurity != rowSecurity {
						t.Errorf("row_security: got %t, want %t", scope.RowSecurity, rowSecurity)
					}
					if scope.Owner != owner || owner.Catalog != ownerJournal || scope.Finished || scope.Completed {
						t.Errorf("root ownership: owner=%p want=%p journal=%p want=%p finished=%t completed=%t", scope.Owner, owner, owner.Catalog, ownerJournal, scope.Finished, scope.Completed)
					}
					if t.Failed() {
						t.FailNow()
					}
				}
				invoke := func(d *catalog.Procedure) error {
					t.Helper()
					// Match exit's authorized catalog recapture, including the
					// current private sentinel, without invoking the bridge.
					wantInput := input
					e.Mu.Lock()
					caller, captureErr := scope.RoutineAuthorizationLocked()
					if captureErr == nil {
						captureErr = e.CaptureElementInput(caller, op, &wantInput)
						wantInput.ACLItem = e.CaptureACLItemInput(caller, op)
						if captureErr == nil {
							wantInput.TypeDiagnosticName, captureErr = core.CaptureTypeDiagnosticName(caller)
						}
					}
					e.Mu.Unlock()
					if captureErr != nil {
						t.Fatal(captureErr)
					}
					// Match the orchestration seam: actual SQL runs unlocked.
					if scope.ReplacementRead {
						e.ReplacementMu.RUnlock()
						scope.ReplacementRead = false
					}
					err := scope.ValidateForeignOptions(d, catalog.ForeignServer, nil)
					assertUnwound(wantInput)
					return err
				}
				if populated {
					scope.Catalog = ownerJournal.Clone()
				} else {
					if err := invoke(accept); err != nil {
						t.Fatal(err)
					}
					if scope.Catalog == nil || scope.Catalog == ownerJournal || scope.PendingCatalog() != scope.Catalog || core.OperationCatalog(op) != scope.Catalog {
						t.Fatal("nil admission did not establish statement-private ownership")
					}
				}
				// Stage a real table and private storage through the normal catalog
				// publication seam before testing nonempty-journal admission.
				stage, relation := transactionalSchemaUnitStage(t, e, op, "fdw_journal_pending", scope.TxRef, 7)
				prepared, err := e.ValidateCatalogStage(op.Context(), stage)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := e.PublishCatalogOperation(op, scope, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
					t.Fatal(err)
				}
				journal := scope.Catalog
				storage := journal.Storage[relation.Address()]
				key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: relation.Namespace(), Name: relation.Name()}
				originalName, nameRecorded := journal.Names[key]
				if !nameRecorded || originalName != (ids.ObjectAddress{}) {
					t.Fatalf("new name precondition: got %v (recorded=%t), want recorded absence", originalName, nameRecorded)
				}
				// Initial rows belong to statement Stage.Rows; StorageChange
				// retains the private empty heap to receive them at root Finish.
				rows := scope.Stage.Rows[relation.Address()]
				if storage.Relation != relation || storage.Replacement == nil || len(rows) != 1 || len(rows[0].Values) != 1 {
					t.Fatalf("missing concrete staged storage/row witness: storage=%+v rows=%+v", storage, rows)
				}
				if got, err := rows[0].Values[0].Integer(); err != nil || got != 7 {
					t.Fatalf("staged row: got %d, error %v, want 7", got, err)
				}
				assertJournal := func() {
					t.Helper()
					if scope.Catalog != journal || scope.PendingCatalog() != journal || core.OperationCatalog(op) != journal || journal == ownerJournal {
						t.Fatalf("private journal ownership: scope=%p pending=%p operation=%p want=%p owner=%p", scope.Catalog, scope.PendingCatalog(), core.OperationCatalog(op), journal, ownerJournal)
					}
					e.Mu.Lock()
					private := e.PrivateCatalogLocked(journal)
					ownerPrivate := e.PrivateCatalogLocked(ownerJournal)
					privateStorage := e.PrivateStorageLocked(journal, relation.Address())
					e.Mu.Unlock()
					if got := journal.Descriptors[relation.Address()]; got != relation {
						t.Errorf("journal descriptor: got %p, want %p", got, relation)
					}
					if got := private.Get(relation.Address()); got != relation {
						t.Errorf("private descriptor: got %p, want %p", got, relation)
					}
					if got := private.NameAddress(key); got != relation.Address() {
						t.Errorf("private name binding: got %v, want %v", got, relation.Address())
					}
					// Names records the original committed binding for conflict
					// detection, not the new binding exposed by PrivateRead.
					if got, ok := journal.Names[key]; !ok || got != originalName {
						t.Errorf("name precondition: got %v (recorded=%t), want %v", got, ok, originalName)
					}
					if got, ok := journal.Storage[relation.Address()]; !ok || !reflect.DeepEqual(got, storage) {
						t.Errorf("staged storage change: got %+v (recorded=%t), want %+v", got, ok, storage)
					}
					if privateStorage != storage.Replacement {
						t.Errorf("private storage backing: got %p, want %p", privateStorage, storage.Replacement)
					}
					if got := scope.Stage.Rows[relation.Address()]; len(got) != 1 || len(got[0].Values) != 1 || got[0].Row != rows[0].Row || got[0].Version != rows[0].Version || !sqltype.SameDatum(got[0].Values[0], rows[0].Values[0]) {
						t.Errorf("staged row identity/datum: got %+v, want %+v", got, rows)
					}
					if ownerPrivate.Get(relation.Address()) != nil || ownerPrivate.NameAddress(key) != (ids.ObjectAddress{}) || e.CatalogRead().Get(relation.Address()) != nil || e.CatalogRead().NameAddress(key) != (ids.ObjectAddress{}) || e.Storage.Relations[relation.Address()] != nil {
						t.Error("validator published staged descriptor/name/storage outside statement journal")
					}
					if t.Failed() {
						t.FailNow()
					}
				}
				assertJournal()
				for range 2 {
					if err := invoke(accept); err != nil {
						t.Fatal(err)
					}
					assertJournal()
				}
				validationErr := invoke(reject)
				if outcome == "success" {
					if validationErr != nil {
						t.Fatal(validationErr)
					}
				} else {
					var diagnostic *sqlstate.Error
					code, message := "22012", "division by zero"
					if outcome == "null_result" {
						code, message = "XX000", fmt.Sprintf("function %d returned NULL", reject.Address().ObjectID)
					}
					if !errors.As(validationErr, &diagnostic) || diagnostic.Code != code || diagnostic.Message != message {
						t.Fatal("genuine validator did not reach expected failure", validationErr)
					}
				}
				assertJournal()
				scope.CompleteUtility()
				if scope.Completed {
					t.Fatal("journal-bearing utility bypassed root completion")
				}
				if finishErr := scope.Finish(validationErr); !errors.Is(finishErr, validationErr) {
					t.Fatal("root completion changed validator result", finishErr)
				}
				if !scope.Finished || owner.State != session.TxIdle || owner.Catalog != nil {
					t.Fatal("root did not retire implicit journal ownership")
				}
				if got := settings.NestLevel; got != level {
					t.Errorf("root Finish settings level: got %d, want idle level %d", got, level)
				}
				if outcome == "success" {
					if e.CatalogRead().Get(relation.Address()) != relation || e.Storage.Relations[relation.Address()] == nil {
						t.Fatal("root completion lost staged catalog/storage")
					}
					published := e.Storage.Relations[relation.Address()].Versions
					if len(published) != 1 || len(published[0].Values) != 1 || published[0].Row != rows[0].Row || published[0].Version != rows[0].Version || !sqltype.SameDatum(published[0].Values[0], rows[0].Values[0]) {
						t.Fatalf("root publication changed or duplicated staged row: got %+v, want %+v", published, rows)
					}
				} else if e.CatalogRead().Get(relation.Address()) != nil || e.Storage.Relations[relation.Address()] != nil {
					t.Fatal("failed root published the preserved private change")
				}
			})
		}
	}
}
