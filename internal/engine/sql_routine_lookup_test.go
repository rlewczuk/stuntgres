package engine

import (
	"context"
	"iter"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

// Instrument the real staged reader rather than replacing its descriptors or
// provider behavior. Callable lookup must not invoke its full discovery API.
type sqlRoutineLookupReader struct {
	stageDefinitionReader
	fullEnumerations int
}

func (r *sqlRoutineLookupReader) descriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	r.fullEnumerations++
	return r.stageDefinitionReader.descriptors()
}

func TestSQLRoutineStagedLookup(t *testing.T) {
	e := New()
	emptyStage := e.BeginCatalogStage(e.CatalogRead())
	emptyReader := stageDefinitionReader{stage: emptyStage}
	if emptyReader.hasSQLProcedureName("abs") {
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
	reader := &sqlRoutineLookupReader{stageDefinitionReader: stageDefinitionReader{stage: stage}}
	check := func(namespace ids.ObjectAddress, name string, want ...*catalog.Procedure) {
		t.Helper()
		found := make(map[ids.ObjectAddress]*catalog.Procedure)
		for d := range reader.proceduresNamed(namespace, name) {
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
	if reader.hasSQLProcedureName("abs") || reader.hasSQLProcedureName("routine_unit_added") {
		t.Fatal("empty overlay promoted absent SQL name")
	}
	metadata := *first
	metadata.ObjectRevision++
	stage.Descriptors[first.Address()] = &metadata
	check(namespace, "routine_unit_index", &metadata, second)
	moved := metadata
	moved.ObjectName = "routine_unit_moved"
	moved.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	stage.Descriptors[first.Address()] = &moved
	check(namespace, "routine_unit_index", second)
	check(moved.Namespace(), moved.Name(), &moved)
	if !reader.hasSQLProcedureName(moved.Name()) {
		t.Fatal("staged rename lost SQL name")
	}
	stage.Descriptors[added.Address()] = added
	check(namespace, "routine_unit_added", added)
	if !reader.hasSQLProcedureName(added.Name()) {
		t.Fatal("staged addition lost SQL name")
	}
	stage.Removals = map[ids.ObjectAddress]catalog.Removal{
		first.Address():  {Ref: e.catalog.Domain.Ref(base.Epoch(), first.Address()), Descriptor: first},
		second.Address(): {Ref: e.catalog.Domain.Ref(base.Epoch(), second.Address()), Descriptor: second},
	}
	check(namespace, "routine_unit_index")
	check(moved.Namespace(), moved.Name())
	if reader.hasSQLProcedureName("routine_unit_index") || reader.hasSQLProcedureName(moved.Name()) {
		t.Fatal("removed/replaced SQL name retained")
	}
	if !base.State().HasSQLProcedureName("routine_unit_index") || base.State().HasSQLProcedureName(moved.Name()) || len(base.State().ProceduresNamed(namespace, "routine_unit_index")) != 2 || base.Get(first.Address()) != first {
		t.Fatal("staged discovery mutated captured index/root")
	}
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.op.definition = reader
	for _, query := range []string{"SELECT abs(1)", "SELECT sum(1)", "SELECT int4(1)"} {
		raw := statement(t, query).(*parser.Select).Targets[0]
		var handled bool
		var err error
		if call, ok := raw.(parser.FunctionExpr); ok {
			_, handled, err = b.bindSQLCallable(call)
		} else {
			_, handled, err = b.bindSQLRoutineSyntax(raw, expressionPolicy{context: projectionContext})
		}
		if handled || err != nil {
			t.Fatal("negative SQL gate changed builtin owner", query, handled, err)
		}
	}
	raw := statement(t, "SELECT routine_unit_added(1)").(*parser.Select).Targets[0].(parser.FunctionExpr)
	node, handled, err := b.bindSQLCallable(raw)
	if err != nil || !handled {
		t.Fatal("staged SQL addition did not bind", handled, err)
	}
	if call, ok := node.(sqlRoutineExpression); !ok || call.procedure != added {
		t.Fatal("selected wrong staged provider")
	}
	prepared := &preparedDrop{base: base, discovery: base, input: reader}
	identity := &preparedIdentityDrop{drop: prepared}
	for _, projected := range []definitionReader{prepared, identity} {
		if !projected.hasSQLProcedureName(added.Name()) {
			t.Fatal("projection lost staged SQL membership")
		}
		count := 0
		for d := range projected.proceduresNamed(namespace, added.Name()) {
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
