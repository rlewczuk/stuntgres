package table

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func builtinType(t *testing.T, id sqltype.ID) *sqltype.Descriptor {
	t.Helper()
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == id {
			return typ
		}
	}
	t.Fatalf("missing builtin %v", id)
	return nil
}

// deparseFixture is public.t (id, v) with t_id_seq owned by t.id; the
// resolver finds only t and records the names it was asked for.
type deparseFixture struct {
	in       DeparseInputs
	text     *sqltype.Descriptor
	resolved []parser.ObjectName
}

func newDeparseFixture(t *testing.T) *deparseFixture {
	domain := &ids.Domain{}
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}
	table := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 100}, ObjectNamespace: namespace, ObjectName: "t", RelationKind: catalog.TableRelation,
		Columns: []catalog.Column{{Name: "id"}, {Name: "v"}}, Attributes: []int32{1, 2}}
	owned := table.Address()
	owned.SubID = 1
	sequence := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 101}, ObjectNamespace: namespace, ObjectName: "t_id_seq", RelationKind: catalog.SequenceRelation,
		Sequence: &catalog.SequenceOptions{Owned: owned}}
	state := catalog.NewState(3)
	state.SetDescriptor(namespace, &catalog.Namespace{ObjectAddress: namespace, ObjectName: "public"})
	state.SetDescriptor(table.Address(), table)
	state.SetDescriptor(sequence.Address(), sequence)
	f := &deparseFixture{text: builtinType(t, sqltype.Text)}
	f.in = DeparseInputs{
		Catalog:  catalog.NewRead(&catalog.Owner{Domain: domain}, state),
		Database: "db",
		Temporary: func() ids.ObjectAddress {
			t.Error("the temporary namespace is read only by index deparse")
			return ids.ObjectAddress{}
		},
		ResolveRelation: func(name parser.ObjectName) (ids.ObjectRef, error) {
			f.resolved = append(f.resolved, name)
			if name.Parts[len(name.Parts)-1].Text != "t" {
				return ids.ObjectRef{}, sqlError("42P01", "missing")
			}
			return domain.Ref(3, table.Address()), nil
		},
	}
	return f
}

func (f *deparseFixture) serial(table, column string) (sqltype.Value, error) {
	c := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	return Deparse(c, f.in, functions.DeparseSerialSequence, [3]sqltype.Value{sqltype.String(f.text, table), sqltype.String(f.text, column)}, f.text)
}

func TestDeparseSerialSequence(t *testing.T) {
	f := newDeparseFixture(t)
	got, err := f.serial("db.public.t", "id")
	if err != nil {
		t.Fatal(err)
	}
	if text, _ := got.Text(); text != "public.t_id_seq" {
		t.Fatalf("sequence %q", text)
	}
	if len(f.resolved) != 1 || len(f.resolved[0].Parts) != 2 || f.resolved[0].Parts[0].Pos != (parser.Position{}) {
		t.Fatalf("resolved %+v, want public.t without positions", f.resolved)
	}
	if got, err := f.serial("t", "v"); err != nil || !got.IsNull() {
		t.Fatalf("unowned column: %v %v", got, err)
	}
	if got, err := f.serial("t", "ctid"); err != nil || !got.IsNull() {
		t.Fatalf("system column: %v %v", got, err)
	}
	for _, tc := range []struct{ table, column, code string }{
		{"t", "x", "42703"},
		{`"t`, "id", "42602"},
		{"u", "id", "42P01"},
	} {
		_, err := f.serial(tc.table, tc.column)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
			t.Errorf("%s.%s: error %v, want %s", tc.table, tc.column, err, tc.code)
		}
	}
}

func TestDeparseMissingObjects(t *testing.T) {
	f := newDeparseFixture(t)
	c := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	oid := builtinType(t, sqltype.Oid)
	missing, err := sqltype.OIDValue(oid, 999)
	if err != nil {
		t.Fatal(err)
	}
	for _, signature := range []functions.DeparseSignature{functions.DeparseConstraint, functions.DeparseIndex, functions.DeparseTrigger, functions.DeparseRule} {
		got, err := Deparse(c, f.in, signature, [3]sqltype.Value{missing}, f.text)
		if err != nil || !got.IsNull() {
			t.Errorf("signature %v: %v %v, want NULL", signature, got, err)
		}
	}
}
