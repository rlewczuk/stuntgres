package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Exercise the owner seam synchronously: real committed versions and a real
// admitted command, with a peer commit between snapshot selection and first
// target-root capture. No timing, row-wait probe, or replacement provider is used.
func TestSnapshotStatementRootConsistency(t *testing.T) {
	ctx := context.Background()
	e, seed, read, relation, _ := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	integer, _ := read.LookupType(sqltype.Int4)
	triggerUnitAdmit(t, seed, relation)
	values := []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 10)}
	if count, err := dml.ApplyInsertValidated(seed.WriteHost(), seed.LockedRef, index.WriteBatch{Values: [][]sqltype.Value{values}}); err != nil || count != 1 {
		t.Fatal("seed real committed predecessor", count, err)
	}
	if err := seed.Finish(nil); err != nil {
		t.Fatal(err)
	}

	op, done, err := enginetest.Operation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	writer, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Finish(nil)
	update := enginetest.Statement(t, "UPDATE public."+relation.Name()+" SET v=v+1 WHERE id=1")
	if err := writer.Gate(update); err != nil {
		t.Fatal(err)
	}
	if err := writer.AcquireRelation(update, true); err != nil {
		t.Fatal(err)
	}
	if err := core.ScopeSnapshotPhase(writer, update); err != nil {
		t.Fatal(err)
	}
	snapshot, frontier := core.ScopeCurrentSnapshot(writer), core.ScopeSnapshotFrontier(writer)
	if snapshot == nil {
		t.Fatal("admitted writer has no command snapshot")
	}
	e.Mu.Lock()
	predecessor := e.Storage.Relations[relation.Address()].Versions[0]
	e.Mu.Unlock()

	// A separate real operation publishes the successor before this command
	// has acquired any target data view. This fixes the failing interleaving.
	peer, err := enginetest.Execute(e, ctx, enginetest.Statement(t, "UPDATE public."+relation.Name()+" SET v=20 WHERE id=1"), nil)
	if err != nil || peer.Count != 1 {
		t.Fatal("peer replacement commit", peer.Count, err)
	}
	e.Mu.Lock()
	successor := e.Storage.Relations[relation.Address()].Versions[0]
	e.Mu.Unlock()
	if successor.Row != predecessor.Row || successor.Version == predecessor.Version || successor.Born <= frontier {
		t.Fatal("peer did not publish a new committed version of the same row after the selected frontier")
	}
	view, err := writer.AcquireDataView(writer.LockedRef)
	if err != nil {
		t.Fatal(err)
	}
	source, err := exec.CaptureSource(ctx, view, writer.LockedRef)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if source.Count() != 1 {
		t.Fatal("selected snapshot lost its precommit candidate during delayed root capture", source.Count())
	}
	captured := source.VersionAt(source.RawIndex(0))
	if captured.Row != predecessor.Row || captured.Version != predecessor.Version || !sqltype.SameDatum(captured.Values[1], values[1]) {
		t.Fatal("captured candidate is not the original snapshot version")
	}
	if core.ScopeCurrentSnapshot(writer) != snapshot || core.ScopeSnapshotFrontier(writer) != frontier {
		t.Fatal("root capture refreshed the command snapshot instead of retaining its roots")
	}

	// Execute against that frozen candidate through the actual command owner.
	// The incumbent row-lock/latest-version seam must use peer's 20, not 10.
	result, err := core.EngineExecuteStatement(e, writer, update, 0, func(context.Context, analyze.ParameterInfo, []sqltype.Value, sqltype.InputEnvironment) error {
		return nil
	})
	if err != nil || result.Count != 1 {
		t.Fatal("latest-version arbitration must update exactly once", result.Count, err)
	}
	if err := writer.Finish(nil); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	committed := e.Storage.Relations[relation.Address()].Versions[0]
	e.Mu.Unlock()
	want := triggerUnitInt(t, integer, 21)
	if committed.Row != predecessor.Row || !sqltype.SameDatum(committed.Values[1], want) {
		t.Fatal("writer did not retain logical identity and apply SET to the committed latest predecessor")
	}
}

// Metadata is constructed directly: these units exercise payloads, frames and
// write ownership rather than duplicating the public SQL scenarios.
func triggerUnitFixture(t *testing.T, source string) (*core.Engine, *core.StatementScope, catalog.Read, *catalog.Relation, *catalog.Procedure) {
	t.Helper()
	return triggerUnitFixtureContext(t, context.Background(), source)
}

func triggerUnitInt(t *testing.T, typ *sqltype.Descriptor, n int64) sqltype.Value {
	t.Helper()
	value, err := sqltype.Integer(typ, n)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func triggerUnitFixtureContext(t *testing.T, ctx context.Context, source string) (*core.Engine, *core.StatementScope, catalog.Read, *catalog.Relation, *catalog.Procedure) {
	t.Helper()
	e := engine.New()
	read := e.CatalogRead()
	namespace := read.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	owner := e.Roles().LookupRef("postgres")
	integer, _ := read.LookupType(sqltype.Int4)
	shape, err := sqltype.Composite(9500002, "trigger_unit_table", "public.trigger_unit_table", 0, []sqltype.CompositeMember{{Attribute: 1, Name: "id", Usage: integer.Usage()}, {Attribute: 2, Name: "v", Usage: integer.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 9500001}, ObjectNamespace: namespace, ObjectName: "trigger_unit_table", ObjectRevision: 1, RelationKind: catalog.TableRelation, Owner: owner, RowType: shape.ID(), Attributes: []int32{1, 2}, Index: map[string]int{"id": 0, "v": 1}, Columns: []catalog.Column{{Name: "id", Usage: integer.Usage(), Nullable: true}, {Name: "v", Usage: integer.Usage(), Nullable: true}}}
	relation.ColumnMeta.HighWater = 2
	rowtype := &catalog.Type{SQLType: shape, ObjectNamespace: namespace, ObjectRevision: 1, Owner: owner, OwningRelation: relation.Address()}
	e.Mu.Lock()
	next := catalog.CloneState(e.Catalog.State)
	next.SetDescriptor(relation.Address(), relation)
	next.BindName(catalog.DescriptorNameKey(relation), relation.Address())
	next.SetDescriptor(rowtype.Address(), rowtype)
	next.BindName(catalog.DescriptorNameKey(rowtype), rowtype.Address())
	e.Catalog.PublishLocked(next)
	e.Storage.Relations[relation.Address()] = &storage.Relation{Generation: 1}
	e.Mu.Unlock()
	op, done, err := enginetest.Operation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(done)
	s, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Finish(nil) })
	trigger, _ := e.CatalogRead().LookupType(2279)
	d := triggerUnitProcedure(t, e, s, "trigger_unit_fn", 9500003, trigger, proc.DMLTriggerInvocation, source)
	return e, s, e.CatalogRead(), relation, d
}

func triggerUnitProcedure(t *testing.T, e *core.Engine, s *core.StatementScope, name string, oid ids.OID, result *sqltype.Descriptor, invocation proc.InvocationKind, source string) *catalog.Procedure {
	t.Helper()
	read := e.CatalogRead()
	language, err := catalog.LookupLanguage(read, "plpgsql")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	namespace := read.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	d := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: namespace, ObjectName: name, ObjectRevision: 1, Owner: e.Roles().LookupRef("postgres"), Language: language.Address(), RoutineKind: catalog.FunctionRoutine, Result: ids.OID(result.ID()), Volatility: expr.Volatile, SQL: &proc.Definition{Kind: proc.PLpgSQLBody, Invocation: invocation, Name: name, Source: source, Result: result.Usage(), PLSyntax: raw}}
	if err := d.SQL.SetFormals(nil); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err == nil {
		err = e.CompilePLRoutineLocked(s.Op, auth, d)
		s.RoutineAuth = auth
	}
	if err == nil {
		next := catalog.CloneState(e.Catalog.State)
		// Direct fixture publication must maintain the same overload-candidate
		// index as catalog candidate application, not only exact name lookup.
		index := catalog.ProcedureIndexEdit{Index: next.ProcedureNames()}
		index.Replace(next.Get(d.Address()), d)
		next.SetProcedureNames(index.Index)
		next.SetDescriptor(d.Address(), d)
		next.BindName(catalog.DescriptorNameKey(d), d.Address())
		e.Catalog.PublishLocked(next)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return d
}
