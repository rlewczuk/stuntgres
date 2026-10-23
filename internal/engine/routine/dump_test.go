package routine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

// dumpUnitView returns a catalog read holding the given descriptors, the
// public namespace and the int4 type.
func dumpUnitView(descriptors ...catalog.Descriptor) catalog.Read {
	state := catalog.NewState(1)
	public := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "public", ObjectRevision: 1}
	state.SetDescriptor(public.Address(), public)
	state.SetDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)}, &catalog.Type{SQLType: dumpUnitType(sqltype.Int4), ObjectRevision: 1})
	for _, d := range descriptors {
		state.SetDescriptor(d.Address(), d)
	}
	return catalog.NewRead(&catalog.Owner{Domain: &ids.Domain{}, Principals: &authorization.Domain{}}, state)
}

func dumpUnitType(id sqltype.ID) *sqltype.Descriptor {
	for _, d := range sqltype.Builtins() {
		if d.ID() == id {
			return d
		}
	}
	panic("missing builtin type")
}

func dumpUnitOperator(oid ids.OID, name string) *catalog.Operator {
	return &catalog.Operator{
		ObjectAddress:   ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: oid},
		ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID},
		ObjectRevision:  1, ObjectName: name, Left: ids.OID(sqltype.Int4), Right: ids.OID(sqltype.Int4), Function: 65, Result: 16,
	}
}

// dumpUnitResolver maps a type dependency to a chosen predecessor and records
// every request.
type dumpUnitResolver struct {
	predecessors map[ids.OID]ids.OID
	err          error
	requests     [][2]ids.ObjectAddress
}

func (r *dumpUnitResolver) Predecessor(_ catalog.Read, address, consumer ids.ObjectAddress) (ids.OID, error) {
	r.requests = append(r.requests, [2]ids.ObjectAddress{address, consumer})
	return r.predecessors[address.ObjectID], r.err
}

// dumpUnitPresenter renders a type as T<name> and a column as C<name>.
type dumpUnitPresenter struct{}

func (dumpUnitPresenter) TypeDeclaration(_ catalog.Read, _ catalog.DefinitionReader, u sqltype.Usage) (string, error) {
	return "T" + u.Type.Name(), nil
}

func (dumpUnitPresenter) ColumnDeclaration(_ catalog.Read, column catalog.Column) (string, error) {
	return "C" + column.Usage.Type.Name(), nil
}

func (dumpUnitPresenter) RoutineIdentity(catalog.Read, ids.OID) (catalog.RoutineIdentity, bool) {
	return catalog.RoutineIdentity{}, false
}

// Routine dumps render no domain expressions.
func (dumpUnitPresenter) DomainDefault(context.Context, catalog.Read, expr.Expr) (string, error) {
	return "", errors.New("unexpected domain default rendering")
}

func (dumpUnitPresenter) DomainCheck(context.Context, catalog.Read, sqltype.Usage, expr.Expr) (string, error) {
	return "", errors.New("unexpected domain check rendering")
}

func TestSQLRoutineDumpEventsUseResolver(t *testing.T) {
	d := &catalog.Procedure{
		ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 90001},
		SQL: &proc.Definition{
			Inputs: []sqltype.Usage{dumpUnitType(sqltype.Int4).Usage(), dumpUnitType(sqltype.Text).Usage()},
			Result: dumpUnitType(sqltype.Bool).Usage(),
		},
	}
	if err := d.SQL.SetFormals([]proc.Formal{{Mode: "i", Usage: d.SQL.Inputs[0]}, {Mode: "o", Usage: d.SQL.Inputs[1]}}); err != nil {
		t.Fatal(err)
	}
	resolver := &dumpUnitResolver{predecessors: map[ids.OID]ids.OID{ids.OID(sqltype.Int4): 91000, ids.OID(sqltype.Bool): 92000}}
	graph := dump.Graph{}
	if err := RoutineDumpEvents(resolver, catalog.Read{}, []*catalog.Procedure{d}, graph); err != nil {
		t.Fatal(err)
	}
	if want := []dump.Event{{OID: 91000}, {OID: 92000}}; !reflect.DeepEqual(graph[dump.Event{OID: 90001}], want) {
		t.Fatalf("predecessors = %v, want %v", graph[dump.Event{OID: 90001}], want)
	}
	var asked []ids.OID
	for _, request := range resolver.requests {
		if request[1] != d.Address() || request[0].ClassID != ids.TypeClassID {
			t.Fatalf("request %v", request)
		}
		asked = append(asked, request[0].ObjectID)
	}
	if want := []ids.OID{ids.OID(sqltype.Int4), ids.OID(sqltype.Text), ids.OID(sqltype.Bool)}; !reflect.DeepEqual(asked, want) {
		t.Fatalf("asked %v, want all formals then result %v", asked, want)
	}

	t.Run("pl_declaration_predecessors", func(t *testing.T) {
		pl := *d
		definition := *d.SQL
		definition.Kind = proc.PLpgSQLBody
		pl.SQL = &definition
		pl.DeclarationDependencies = []ids.ObjectAddress{
			{ClassID: ids.RelationClassID, ObjectID: 90002, SubID: 7},
			{ClassID: ids.RelationClassID, ObjectID: 90003},
			{ClassID: ids.TypeClassID, ObjectID: 90004},
			{ClassID: ids.CollationClassID, ObjectID: 90005},
		}
		declResolver := &dumpUnitResolver{predecessors: map[ids.OID]ids.OID{90002: 90002, 90003: 90003, 90004: 90004, 90005: 90005}}
		declGraph := dump.Graph{}
		if err := RoutineDumpEvents(declResolver, catalog.Read{}, []*catalog.Procedure{&pl}, declGraph); err != nil {
			t.Fatal(err)
		}
		want := []dump.Event{{OID: 90002}, {OID: 90003}, {OID: 90004}, {OID: 90005}}
		if !reflect.DeepEqual(declGraph[dump.Event{OID: 90001}], want) {
			t.Fatalf("declaration predecessors %v, want %v", declGraph, want)
		}
		requests := declResolver.requests[len(declResolver.requests)-len(pl.DeclarationDependencies):]
		for i, source := range pl.DeclarationDependencies {
			source.SubID = 0
			if requests[i] != [2]ids.ObjectAddress{source, pl.Address()} {
				t.Fatalf("declaration resolver request %v, want owning declaration %v", requests[i], source)
			}
		}
		if pl.DeclarationDependencies[0].SubID != 7 {
			t.Fatal("dump mutated stable member source")
		}
	})

	failure := errors.New("resolver failure")
	if err := RoutineDumpEvents(&dumpUnitResolver{err: failure}, catalog.Read{}, []*catalog.Procedure{d}, dump.Graph{}); !errors.Is(err, failure) {
		t.Fatalf("err = %v, want the resolver's error", err)
	}
}

func TestOperatorDumpLinksUsePresenter(t *testing.T) {
	a, b := dumpUnitOperator(90001, "#?#"), dumpUnitOperator(90002, "#@#")
	a.Commutator, b.Commutator = b.Address().ObjectID, a.Address().ObjectID
	view := dumpUnitView(a, b)
	text, err := operatorDumpLinks(dumpUnitPresenter{}, view, a)
	if err != nil {
		t.Fatal(err)
	}
	if want := `ALTER OPERATOR "public".#?# (Tint4, Tint4) SET (COMMUTATOR = OPERATOR("public".#@#));` + "\n"; text != want {
		t.Fatalf("links = %q, want %q", text, want)
	}
}
