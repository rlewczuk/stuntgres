package core_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

func TestPgvisibilityCheckGeneratedTempGraft(t *testing.T) {
	// Reuse the genuine tableoid generation/CHECK random-provider repair fixture,
	// but enter through preserveTemporaryLocked: f.run bypasses the live graft.
	f := newGeneratedRepairFixture(t)
	e := f.target
	// The legacy repair-only helper owns an unbound phantom TEMP descriptor.
	// It is not a SQL-valid live incarnation and is not this witness's retained
	// heap. Retire only that fixture-owned setup before genuine SQL admission.
	e.Mu.Lock()
	current := catalog.CloneState(e.Catalog.State)
	phantom := current.MustRelation(f.temp)
	namespace := current.Get(phantom.Namespace())
	current.UnbindName(catalog.DescriptorNameKey(phantom))
	current.UnbindName(catalog.DescriptorNameKey(namespace))
	current.RemoveDescriptor(phantom.Address())
	current.RemoveDescriptor(phantom.Namespace())
	e.Storage.RemoveRelationLocked(f.temp)
	e.Catalog.PublishLocked(current)
	e.Mu.Unlock()
	owner := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE EXTENSION pg_visibility", "CREATE TEMP TABLE visibility_live_temp(n integer)", "INSERT INTO visibility_live_temp VALUES(7)", "VACUUM (FREEZE) visibility_live_temp", "SELECT * FROM pg_visibility_map_summary('pg_temp.visibility_live_temp'::regclass)"} {
		visibilityBoundarySQL(t, owner, q)
	}
	var temporary ids.ObjectAddress
	for address, relation := range e.CatalogRead().Relations() {
		if relation.Name() == "visibility_live_temp" {
			namespace, _ := e.CatalogRead().Namespace(relation.Namespace())
			if namespace != nil && namespace.Temporary == owner.SessionToken {
				temporary = address
			}
		}
	}
	if temporary == (ids.ObjectAddress{}) {
		t.Fatal("no genuine live TEMP owner/table")
	}
	live := e.Storage.Relations[temporary]
	root := live.Visibility
	if root == nil || root.Invalidated || root.Map.AllVisible != 1 || len(live.Versions) != 1 {
		t.Fatal("missing real frozen TEMP map")
	}
	rows, ends, headers, generation := live.Versions, live.Ends, live.Headers, live.Generation
	// A real imported/live-TEMP OID collision requires mapping and therefore
	// actual tableoid recomputation during preservation's detached repair.
	next := catalog.CloneState(f.next)
	old := next.MustRelation(f.imported)
	imported := *old
	next.UnbindName(catalog.DescriptorNameKey(old))
	next.RemoveDescriptor(old.Address())
	imported.ObjectAddress = temporary
	imported.ObjectName = "visibility_imported_repair"
	oid, _ := e.CatalogRead().LookupType(sqltype.Oid)
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	floating, _ := e.CatalogRead().LookupType(sqltype.Float8)
	leaf := plan.SystemAttributeExpr{SubID: -6, Info: expr.Info{Usage: oid.Usage(), Property: expr.Properties{RowDependent: true}}, Owner: e.Catalog.Domain.Ref(next.Epoch(), temporary)}
	imported.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {Kind: catalog.GenerationStored, Expression: leaf, Planned: leaf}}
	probe := functions.Random{Info: expr.Info{Usage: floating.Usage(), Property: expr.Properties{Volatility: expr.Volatile}}, Name: "random"}
	imported.Constraints.Check = []catalog.CheckConstraint{{Name: "real_repair_gate", Expression: plan.NullTestExpr{Info: expr.Info{Usage: boolean.Usage()}, Child: probe, Not: true}}}
	// The repair-only prototype has no definition identities. This candidate
	// proceeds through full replacement admission, so mint its generation and
	// CHECK identities through the ordinary CREATE TABLE allocator first.
	e.Mu.Lock()
	definitionErr := table.AssignDefinitionOIDs(core.DDLHost{E: e}, &imported)
	e.Mu.Unlock()
	if definitionErr != nil {
		t.Fatal("allocate imported definition identities", definitionErr)
	}
	if generationOID, checkOID := imported.ColumnMeta.Generations[2].OID, imported.Constraints.Check[0].OID; generationOID == 0 || checkOID == 0 || generationOID == checkOID {
		t.Fatal("imported generation and CHECK lack distinct definition identities", generationOID, checkOID)
	}
	next.SetDescriptor(imported.Address(), &imported)
	next.BindName(catalog.DescriptorNameKey(&imported), imported.Address())
	oldOID, err := sqltype.OIDValue(oid, uint32(temporary.ObjectID))
	if err != nil {
		t.Fatal(err)
	}
	values := append([]sqltype.Value(nil), f.source...)
	values[1] = oldOID
	relations := map[ids.ObjectAddress]*storage.Relation{temporary: {Versions: []storage.RowVersion{{Values: values}}}}
	sequences := map[ids.ObjectAddress]*storage.SequenceGenerator{}
	indexes := &index.Snapshot{}
	policy := enginetest.ReplacementProcedurePolicyFixture(e, e)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	joined := false
	var once sync.Once
	unblock := func() { once.Do(func() { close(f.gate.release) }) }
	defer func() {
		unblock()
		cancel()
		if !joined {
			select {
			case <-result:
			case <-time.After(30 * time.Second):
				t.Error("generated preservation worker did not join")
			}
		}
	}()
	go func() {
		e.ReplacementMu.Lock()
		defer e.ReplacementMu.Unlock()
		e.Mu.Lock()
		defer e.Mu.Unlock()
		oids := e.Catalog.OIDReservationLocked()
		err := core.EnginePreserveTemporaryLocked(e, ctx, next, relations, sequences, f.op, indexes, policy, &oids)
		if err == nil {
			var prepared index.RemintedIndexStorage
			prepared, err = core.EnginePrepareForeignIndexStorage(e, ctx, next, relations, indexes)
			if err == nil {
				err = core.EngineInstallForeignCatalogLocked(e, ctx, next, relations, sequences, e.Catalog.NextRevision(), prepared, oids)
			}
		}
		result <- err
	}()
	select {
	case <-f.gate.entered:
	case err := <-result:
		joined = true
		t.Fatal("actual demanded generated repair did not pause", err)
	case <-time.After(30 * time.Second):
		t.Fatal("generated repair not reached")
	}
	// Complete a genuine, legal owner invocation while both replacement/engine
	// locks are released. No row, descriptor, namespace or allocator is changed.
	visibilityBoundarySQL(t, owner, "SELECT pg_truncate_visibility_map('pg_temp.visibility_live_temp'::regclass)")
	empty := live.Visibility
	if empty == root || !empty.Invalidated || e.Storage.Relations[temporary] != live || !storage.SameVersionRoot(live.Versions, rows) || live.Ends != ends || live.Headers != headers || live.Generation != generation {
		t.Fatal("detached TEMP mutation was not VM-only")
	}
	unblock()
	select {
	case err = <-result:
		joined = true
	case <-time.After(30 * time.Second):
		t.Fatal("generated preservation did not resume")
	}
	if err != nil {
		t.Fatal("generated TEMP preservation or replacement installation failed", err)
	}
	installed := e.Storage.Relations[temporary]
	if installed != live || installed.Visibility != empty || !empty.Invalidated || !storage.SameVersionRoot(installed.Versions, rows) || installed.Ends != ends || installed.Headers != headers || installed.Generation != generation || root.Invalidated || root.Map.AllVisible != 1 {
		t.Fatal("graft restored stale TEMP pointer/map or mutated old roots")
	}
	address := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "visibility_imported_repair"})
	if address == (ids.ObjectAddress{}) || address == temporary {
		t.Fatal("fixture did not require authentic collision remapping", address)
	}
	importedRows := e.Storage.Relations[address]
	if importedRows == nil || len(importedRows.Versions) != 1 {
		t.Fatal("repaired imported heap missing")
	}
	repairedOID, err := importedRows.Versions[0].Values[1].OID()
	if err != nil || repairedOID != uint32(address.ObjectID) {
		t.Fatal("actual generated tableoid repair failed", repairedOID, address, err)
	}
	query := enginetest.SessionSQL(t, owner, "SELECT n FROM pg_temp.visibility_live_temp")
	var row [1]sqltype.Value
	present, err := query.Rows.Next(t.Context(), row[:])
	if err != nil || !present {
		t.Fatal("retained TEMP SELECT missing", present, err)
	}
	n, err := row[0].Integer()
	if err != nil || n != 7 {
		t.Fatal("retained TEMP SELECT changed", n, err)
	}
	if more, err := query.Rows.Next(t.Context(), row[:]); more || err != nil {
		t.Fatal("retained TEMP SELECT extra row", more, err)
	}
	if err := query.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	f.assertJoined(t)
}
