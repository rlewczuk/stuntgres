package engine

import (
	"context"
	"iter"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

// Instrument the real staged reader rather than replacing its descriptors or
// provider behavior. Callable lookup must not invoke its full discovery API.
type sqlRoutineLookupReader struct {
	StageDefinitionReader
	fullEnumerations int
}

func (r *sqlRoutineLookupReader) Descriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	r.fullEnumerations++
	return r.StageDefinitionReader.Descriptors()
}

func TestSQLRoutineStagedLookup(t *testing.T) {
	e := New()
	emptyStage := e.BeginCatalogStage(e.CatalogRead())
	emptyReader := StageDefinitions(emptyStage)
	if emptyReader.HasDynamicProcedureName("abs") {
		t.Fatal("empty SQL index promoted builtin")
	}
	e.DiscardCatalogStage(emptyStage)
	run(t, e, "CREATE FUNCTION routine_unit_index(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$")
	run(t, e, "CREATE FUNCTION routine_unit_index(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1$$")
	base := e.CatalogRead()
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	addresses := base.State().ProceduresNamed(namespace, "routine_unit_index")
	if len(addresses) != 2 {
		t.Fatal("missing real overloads")
	}
	first, second := base.MustProcedure(addresses[0]), base.MustProcedure(addresses[1])
	// A genuine later declaration supplies the staged addition, absent from base.
	run(t, e, "CREATE FUNCTION routine_unit_added(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$")
	current := e.CatalogRead()
	added := current.MustProcedure(current.State().ProceduresNamed(namespace, "routine_unit_added")[0])
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	reader := &sqlRoutineLookupReader{StageDefinitionReader: StageDefinitions(stage)}
	check := func(namespace ids.ObjectAddress, name string, want ...*catalog.Procedure) {
		t.Helper()
		found := make(map[ids.ObjectAddress]*catalog.Procedure)
		for d := range reader.ProceduresNamed(namespace, name) {
			if found[d.Address()] != nil {
				t.Fatal("duplicate staged candidate", d.Address())
			}
			found[d.Address()] = d
		}
		if len(found) != len(want) {
			t.Fatal("wrong staged membership", name, len(found), len(want))
		}
		for _, d := range want {
			if found[d.Address()] != d {
				t.Fatal("stale or missing staged descriptor", name, d.Address())
			}
		}
	}
	check(namespace, "routine_unit_index", first, second)
	if reader.HasDynamicProcedureName("abs") || reader.HasDynamicProcedureName("routine_unit_added") {
		t.Fatal("empty overlay promoted absent SQL name")
	}
	metadata := *first
	metadata.ObjectRevision++
	stage.UpdateDescriptor(&metadata)
	check(namespace, "routine_unit_index", &metadata, second)
	moved := metadata
	moved.ObjectName = "routine_unit_moved"
	moved.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	stage.UpdateDescriptor(&moved)
	check(namespace, "routine_unit_index", second)
	check(moved.Namespace(), moved.Name(), &moved)
	if !reader.HasDynamicProcedureName(moved.Name()) {
		t.Fatal("staged rename lost SQL name")
	}
	stage.UpdateDescriptor(added)
	check(namespace, "routine_unit_added", added)
	if !reader.HasDynamicProcedureName(added.Name()) {
		t.Fatal("staged addition lost SQL name")
	}
	stage.RecordRemoval(e.Catalog.Domain.Ref(base.Epoch(), first.Address()), first)
	stage.RecordRemoval(e.Catalog.Domain.Ref(base.Epoch(), second.Address()), second)
	check(namespace, "routine_unit_index")
	check(moved.Namespace(), moved.Name())
	if reader.HasDynamicProcedureName("routine_unit_index") || reader.HasDynamicProcedureName(moved.Name()) {
		t.Fatal("removed/replaced SQL name retained")
	}
	if !base.State().HasDynamicProcedureName("routine_unit_index") || base.State().HasDynamicProcedureName(moved.Name()) || len(base.State().ProceduresNamed(namespace, "routine_unit_index")) != 2 || base.Get(first.Address()) != first {
		t.Fatal("staged discovery mutated captured index/root")
	}
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	binderOperation(b).Definition = reader
	for _, query := range []string{"SELECT abs(1)", "SELECT sum(1)"} {
		raw := statement(t, query).(*parser.Select).Targets[0]
		var handled bool
		var err error
		if call, ok := raw.(parser.FunctionExpr); ok {
			_, handled, err = b.BindSQLCallable(call)
		} else {
			_, handled, err = b.BindSQLRoutineSyntax(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		}
		if handled || err != nil {
			t.Fatal("negative SQL gate changed builtin owner", query, handled, err)
		}
	}
	raw := statement(t, "SELECT routine_unit_added(1)").(*parser.Select).Targets[0].(parser.FunctionExpr)
	node, handled, err := b.BindSQLCallable(raw)
	if err != nil || !handled {
		t.Fatal("staged SQL addition did not bind", handled, err)
	}
	if call, ok := node.(plan.SQLRoutineExpr); !ok || call.Procedure != added {
		t.Fatal("selected wrong staged provider")
	}
	prepared := &table.DropPreparation{Base: base, Discovery: base, Input: reader}
	identity := &preparedIdentityDrop{drop: prepared}
	for _, projected := range []catalog.DefinitionReader{prepared, identity} {
		if !projected.HasDynamicProcedureName(added.Name()) {
			t.Fatal("projection lost staged SQL membership")
		}
		count := 0
		for d := range projected.ProceduresNamed(namespace, added.Name()) {
			if d != added {
				t.Fatal("projection lost current staged provider")
			}
			count++
		}
		if count != 1 {
			t.Fatal("projection duplicated or omitted staged addition", count)
		}
	}
	if reader.fullEnumerations != 0 {
		t.Fatal("routine lookup scanned all descriptors", reader.fullEnumerations)
	}
}
