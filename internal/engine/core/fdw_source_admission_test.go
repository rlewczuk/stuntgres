package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
)

// This composes the existing zero-width scan/transaction unit harness with the
// actual installed external gateway and real Stage production. It never opens
// a transport or purports to prove remote SQL rows/authentication.
type fdwAdmissionRejector struct{}

func (*fdwAdmissionRejector) Open(context.Context, catalog.ExternalSQLConnectRequest) (catalog.ExternalSQLConnection, error) {
	return nil, errors.New("ownership-only unit rejects transport Open")
}

type fdwAdmissionTransaction struct {
	fdwUnitTransaction
	admissions []catalog.ExternalSQLSession
}

func (t *fdwAdmissionTransaction) BeginStatement(ctx context.Context) error {
	t.admissions = append(t.admissions, catalog.ExternalSQLContextCapability(ctx))
	return t.record("begin-statement")
}

type fdwAdmissionWrapper struct {
	fdwUnitWrapper
	requests     []catalog.ForeignRequest
	scans        []*fdwUnitScan
	transactions []*fdwAdmissionTransaction
}

func (*fdwAdmissionWrapper) ForeignTransactionCapabilities(context.Context, catalog.ForeignRequest) (catalog.ForeignTransactionCapabilities, error) {
	return catalog.ForeignTransactionCapabilities{StatementRollback: true, TransactionRollback: true, Savepoints: true}, nil
}
func (w *fdwAdmissionWrapper) BeginForeignTransaction(context.Context, catalog.ForeignRequest) (catalog.ForeignTransaction, error) {
	tx := &fdwAdmissionTransaction{}
	w.transactions = append(w.transactions, tx)
	return tx, nil
}
func (w *fdwAdmissionWrapper) BeginForeignScan(_ context.Context, request catalog.ForeignRequest, _ catalog.ForeignPlan) (catalog.ForeignScan, error) {
	w.requests = append(w.requests, request)
	scan := &fdwUnitScan{}
	w.scans = append(w.scans, scan)
	return scan, nil
}

func fdwAdmissionFixture(t *testing.T) (*core.Engine, *session.Session, *fdwAdmissionWrapper) {
	t.Helper()
	e := engine.New()
	w := &fdwAdmissionWrapper{fdwUnitWrapper: fdwUnitWrapper{paths: []catalog.ForeignPath{{TotalCost: 1}}}}
	definition, err := fdw.ValidateWrapper(fdw.WrapperDefinition{HandlerName: catalog.ForeignFunctionName{Name: "admission_handler"}, Wrapper: w, ExternalProvider: "admission-unit"})
	if err != nil {
		t.Fatal(err)
	}
	providers := []catalog.ExternalSQLProviderDefinition{{Name: "admission-unit", Version: "1", Provider: &fdwAdmissionRejector{}}}
	wrappers, bindings, err := fdw.BindExternalProviders([]fdw.WrapperDefinition{definition}, providers, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.InstallForeignDefinitions(bindings, wrappers, nil, fdwIntegerImport, fdwIntegerExport); err != nil {
		t.Fatal(err)
	}
	s := enginetest.StartSession(t, e, nil)
	for _, query := range []string{
		"CREATE ROLE admission_reader",
		"CREATE FOREIGN DATA WRAPPER admission_wrapper HANDLER admission_handler",
		"CREATE SERVER admission_server FOREIGN DATA WRAPPER admission_wrapper OPTIONS(marker 'server')",
		"CREATE USER MAPPING FOR PUBLIC SERVER admission_server OPTIONS(marker 'public')",
		"CREATE FOREIGN TABLE admission_table() SERVER admission_server",
		"GRANT USAGE ON SCHEMA public TO admission_reader",
		"GRANT USAGE ON FOREIGN SERVER admission_server TO admission_reader",
		"GRANT SELECT ON admission_table TO admission_reader",
	} {
		enginetest.SessionSQL(t, s, query)
	}
	return e, s, w
}

func TestForeignScanStatementAdmission(t *testing.T) {
	t.Run("repeated_production", func(t *testing.T) {
		e, s, w := fdwAdmissionFixture(t)
		enginetest.SessionSQL(t, s, "BEGIN")
		defer enginetest.SessionSQL(t, s, "ROLLBACK")
		op, _, err := s.AcquireCommand(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		query, source, release := enginetest.HashCaptureQuery(t, e, op, "SELECT 1 FROM admission_table")
		defer release()
		ctx := op.Context()
		stage, err := exec.OpenNode(ctx, &exec.State{Source: source}, query.Root)
		if err != nil {
			t.Fatal(err)
		}
		defer stage.Close()
		var row [1]sqltype.Value
		if present, err := stage.Next(ctx, row[:]); err != nil || !present {
			t.Fatal("actual first production", present, err)
		}
		if source.Foreign == nil || len(source.Foreign.Executions) != 1 || len(w.requests) != 1 {
			t.Fatal("actual gateway did not retain one source execution")
		}
		execution := source.Foreign.Executions[0]
		captured := w.requests[0]
		if captured.ExternalSQL == nil || len(captured.ServerOptions) != 1 || len(captured.MappingOptions) != 1 {
			t.Fatal("actual external admission omitted owned capability/options")
		}
		scope := core.ActiveScopeOfLease(op.Lease)
		if scope == nil || scope.Op != op || scope.Owner != s.Transactions {
			t.Fatal("actual producer lost its admitted statement/transaction owner")
		}
		key := fdw.ParticipantKey{Handler: captured.HandlerOID, Server: captured.ServerOID, Mapping: captured.MappingOID, Role: captured.RoleOID}
		foreign := core.OwnerExtensionForeign(core.OwnerExtensionOrZero(scope.Owner))
		participant := foreign.Participant(key)
		if participant == nil || participant.Tx != captured.Transaction {
			t.Fatal("actual producer omitted its genuine transaction participant")
		}
		serverBacking, mappingBacking := &captured.ServerOptions[0], &captured.MappingOptions[0]
		host := source.LiveHost()
		if host == nil {
			t.Fatal("actual source lost live statement host")
		}
		var touchError error
		allocations, bytes := util.AllocationsPerCall(64, func() {
			if touchError == nil {
				touchError = host.TouchForeignScan(ctx, source.Foreign)
			}
		})
		if touchError != nil {
			t.Fatal(touchError)
		}
		for range 2 {
			if present, err := stage.Next(ctx, row[:]); err != nil || !present {
				t.Fatal("actual resumed production", present, err)
			}
		}
		if present, err := stage.Next(ctx, row[:]); err != nil || present {
			t.Fatal("actual source exhaustion", present, err)
		}
		if source.Foreign.Executions[0] != execution || execution.Scan != w.scans[0] || len(w.requests) != 1 || w.requests[0].ExternalSQL != captured.ExternalSQL || &w.requests[0].ServerOptions[0] != serverBacking || &w.requests[0].MappingOptions[0] != mappingBacking {
			t.Fatal("repeated production replaced immutable execution ownership/backing")
		}
		currentForeign := core.OwnerExtensionForeign(core.OwnerExtensionOrZero(scope.Owner))
		if current := currentForeign.Participant(key); current != participant {
			t.Fatal("repeated source touch replaced participant ownership")
		}
		if allocations != 0 || bytes != 0 {
			t.Fatalf("already-admitted source touch reconstructed capability artifacts: %d allocations/%d bytes per touch; want 0/0", allocations, bytes)
		}
		if len(w.transactions) != 1 || len(w.transactions[0].admissions) != 1 || w.transactions[0].admissions[0] != captured.ExternalSQL {
			t.Fatal("actual source admission was not owned by its participant context")
		}
		if err := core.ScopeFinishForeignStatements(scope, nil); err != nil {
			t.Fatal(err)
		}
		if err := host.TouchForeignScan(ctx, source.Foreign); err != nil {
			t.Fatal(err)
		}
		admissions := w.transactions[0].admissions
		if len(admissions) != 2 || admissions[1] == nil || admissions[1] == admissions[0] {
			t.Fatal("statement completion retained stale source admission authority")
		}
		if err := core.CommitForeign(scope.Owner, ctx, scope.TxRef); err != nil {
			t.Fatal(err)
		}
		if err := host.TouchForeignScan(ctx, source.Foreign); err == nil || err.Error() != "stuntgres: foreign transaction is already disposed" {
			t.Fatal("closed admitted participant was silently skipped or reopened", err)
		}
	})
	t.Run("fresh_caller_and_fetch", func(t *testing.T) {
		e, s, w := fdwAdmissionFixture(t)
		enginetest.SessionSQL(t, s, "BEGIN")
		defer enginetest.SessionSQL(t, s, "ROLLBACK")
		capture := func() catalog.ForeignRequest {
			t.Helper()
			op, _, err := s.AcquireCommand(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			query, source, release := enginetest.HashCaptureQuery(t, e, op, "SELECT 1 FROM admission_table")
			defer release()
			stage, err := exec.OpenNode(op.Context(), &exec.State{Source: source}, query.Root)
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			var row [1]sqltype.Value
			if present, err := stage.Next(op.Context(), row[:]); err != nil || !present {
				t.Fatal("actual source admission", present, err)
			}
			return w.requests[len(w.requests)-1]
		}
		first := capture()
		second := capture()
		if first.ExternalSQL == nil || second.ExternalSQL == nil || first.ExternalSQL == second.ExternalSQL || first.MappingOID != second.MappingOID || first.RoleOID != second.RoleOID {
			t.Fatal("new statement reused prior capability authority or changed mapping identity")
		}
		enginetest.SessionSQL(t, s, "SET ROLE admission_reader")
		reader := capture()
		if reader.ExternalSQL == nil || reader.ExternalSQL == second.ExternalSQL || reader.MappingOID != first.MappingOID || reader.RoleOID == first.RoleOID {
			t.Fatal("distinct genuine PUBLIC caller did not receive its own admission")
		}
		enginetest.SessionSQL(t, s, "RESET ROLE")
		enginetest.SessionSQL(t, s, "DECLARE admission_cursor NO SCROLL CURSOR FOR SELECT 1 FROM admission_table")
		// Observe the cursor's genuine participant, not a concatenation grouped
		// by transaction: the reader's separate participant is not the latest
		// postgres admission merely because it appears last in that grouping.
		cursorTransaction, ok := first.Transaction.(*fdwAdmissionTransaction)
		if !ok {
			t.Fatal("actual postgres source omitted its transaction owner")
		}
		admissions := func() []catalog.ExternalSQLSession { return cursorTransaction.admissions }
		before := len(admissions())
		enginetest.SessionSQL(t, s, "FETCH 1 FROM admission_cursor")
		firstFetch := admissions()
		if len(firstFetch) != before+1 || firstFetch[len(firstFetch)-1] == nil {
			t.Fatal("first actual FETCH did not own current statement admission")
		}
		if w.requests[len(w.requests)-1].Transaction != cursorTransaction {
			t.Fatal("cursor changed the genuine mapping/caller transaction owner")
		}
		scan := w.scans[len(w.scans)-1]
		enginetest.SessionSQL(t, s, "FETCH 1 FROM admission_cursor")
		secondFetch := admissions()
		if len(secondFetch) != len(firstFetch)+1 || secondFetch[len(secondFetch)-1] == nil || secondFetch[len(secondFetch)-1] == firstFetch[len(firstFetch)-1] || w.scans[len(w.scans)-1] != scan {
			t.Fatal("later FETCH reused stale authority or replaced retained scan ownership")
		}
		enginetest.SessionSQL(t, s, "CLOSE admission_cursor")
	})
}
