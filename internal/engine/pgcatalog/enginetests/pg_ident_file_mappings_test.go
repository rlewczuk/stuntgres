package enginetests_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/identfile"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func identInput(e *core.Engine) pgcatalog.Input {
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.IdentFileMappingsOID}
	return pgcatalog.Input{Context: context.Background(), Catalog: view, Source: view.MustRelation(address), Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Owner: view.Owner(), Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{}), IdentFile: e.IdentFile}
}
func TestPGIdentFileMappingsCapture(t *testing.T) {
	e := engine.New()
	path := filepath.Join(t.TempDir(), "ident")
	if err := os.WriteFile(path, []byte("m s p\nmissing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.IdentFile = identfile.New(path)
	input := identInput(e)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.IdentFileMappingsOID)
	if !ok {
		t.Fatal("producer missing")
	}
	rows, err := produce(input)
	if err != nil || len(rows) != 2 || len(rows[0]) != 7 {
		t.Fatal(rows, err)
	}
	if n, err := rows[0][0].Integer(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	for _, i := range []int{0, 3, 4, 5} {
		if !rows[1][i].IsNull() {
			t.Fatal("invalid-row field", i, rows[1][i])
		}
	}
	if text, err := rows[1][6].Text(); err != nil || text != "missing entry at end of line" {
		t.Fatal(text, err)
	}
	captured, err := core.CaptureCatalogSourceRows(input, produce)
	if err != nil || captured.Ref != input.Ref || !reflect.DeepEqual(captured.Anonymous, rows) {
		t.Fatal(captured, err)
	}
	rows[0][1] = sqltype.Null(input.Source.Columns[1].Usage.Type)
	again, err := produce(input)
	if err != nil || again[0][1].IsNull() {
		t.Fatal("borrowed previous rows", again, err)
	}
	reject := func(in pgcatalog.Input) {
		t.Helper()
		if rows, err := produce(in); rows != nil || err == nil {
			t.Fatal("forged source admitted", rows, err)
		}
		if source, err := core.CaptureCatalogSourceRows(in, produce); source != nil || err == nil {
			t.Fatal("forged source published", source, err)
		}
	}
	foreign := engine.New()
	for _, change := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Source = nil }, func(in *pgcatalog.Input) { in.Owner = nil }, func(in *pgcatalog.Input) { in.Owner = foreign.CatalogRead().Owner() }, func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() }, func(in *pgcatalog.Input) { in.Runtime = nil }, func(in *pgcatalog.Input) { in.Ref = ids.ObjectRef{} }, func(in *pgcatalog.Input) { in.Ref.Epoch++ }, func(in *pgcatalog.Input) { source := *in.Source; in.Source = &source },
	} {
		in := input
		change(&in)
		reject(in)
	}
	for _, change := range []func(*catalog.Relation){
		func(r *catalog.Relation) { r.Columns = r.Columns[:6] }, func(r *catalog.Relation) { r.Columns = append(r.Columns, r.Columns[0]) }, func(r *catalog.Relation) { r.Columns[0].Name = "forged" }, func(r *catalog.Relation) { r.Columns[0].Usage.Type, _ = input.Catalog.LookupType(sqltype.Int8) }, func(r *catalog.Relation) { r.Columns[1].Usage.Type, _ = foreign.CatalogRead().LookupType(sqltype.Text) }, func(r *catalog.Relation) { r.Columns[0].Usage.Typmod = 0 }, func(r *catalog.Relation) { r.Columns[1].Usage.Collation.ID = 950 }, func(r *catalog.Relation) { r.Columns[0].Nullable = false }, func(r *catalog.Relation) { r.Columns[0].NullabilityKnown = false }, func(r *catalog.Relation) { r.Columns[0].DeclaredDimensions = 1 },
	} {
		state := *input.Catalog.State()
		source := *input.Source
		source.Columns = append([]catalog.Column(nil), source.Columns...)
		change(&source)
		state.SetDescriptor(source.Address(), &source)
		in := input
		in.Catalog, in.Source = e.Catalog.Read(&state), &source
		reject(in)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	input.Context, input.Source = ctx, nil
	if rows, err := produce(input); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(rows, err)
	}
}
func TestPGIdentFileMappingsOwnership(t *testing.T) {
	e := engine.New()
	in := identInput(e)
	r := in.Source
	if r.RowType != 12120 || !r.VirtualView || !r.CatalogView || len(r.Columns) != 7 || r.Owner.ID() != authorization.BootstrapPrincipalID {
		t.Fatal(r)
	}
	names := []string{"map_number", "file_name", "line_number", "map_name", "sys_name", "pg_username", "error"}
	types := []sqltype.ID{sqltype.Int4, sqltype.Text, sqltype.Int4, sqltype.Text, sqltype.Text, sqltype.Text, sqltype.Text}
	proc, ok := in.Catalog.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 6250})
	if !ok || proc.Builtin != routine.BuiltinRoutine(6250) || proc.Builtin.Providers != 0 || proc.Builtin.Parallel != 's' || !proc.Strict || !proc.ReturnsSet() || proc.Result != 2249 || proc.Owner != r.Owner {
		t.Fatal(proc)
	}
	if err := (stored.ProcedureProvider{}).Validate(in.Catalog, proc); err != nil {
		t.Fatal(err)
	}
	outputs := proc.OutputDeclaration()
	if outputs == nil || outputs.Len() != 7 || outputs.Inputs() != 0 {
		t.Fatal(outputs)
	}
	for i, c := range r.Columns {
		owned, ok := in.Catalog.LookupType(types[i])
		collation := uint32(100)
		if types[i] == sqltype.Int4 {
			collation = 0
		}
		if !ok || c.Usage.Type != owned || c.Name != names[i] || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collation || !c.Nullable || !c.NullabilityKnown || c.OriginTable != 12118 || c.OriginAttribute != int16(i+1) || outputs.Name(i) != names[i] || outputs.Type(i) != ids.OID(types[i]) {
			t.Fatal(i, c)
		}
	}
	for _, item := range []struct {
		address   ids.ObjectAddress
		acl       authorization.ACLValue
		privilege authorization.Privileges
	}{{r.Address(), r.Security.ACL, authorization.TableGrantPrivileges}, {proc.Address(), proc.ACL, authorization.ExecutePrivilege}} {
		if !item.acl.Explicit || len(item.acl.Entries) != 1 || item.acl.Entries[0].Grantee.Role != r.Owner || item.acl.Entries[0].Privileges != item.privilege {
			t.Fatal(item)
		}
		initial := in.Catalog.State().InitialPrivilege(item.address)
		if initial == nil || !reflect.DeepEqual(initial.ACL, item.acl) {
			t.Fatal(initial, item.acl)
		}
	}
	row := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12120})
	array := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12119})
	if row.Array != 12119 || row.OwningRelation != r.Address() || array.OwningRelation != r.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal(row, array)
	}
}
