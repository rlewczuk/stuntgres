package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func newPGFSFixture(t *testing.T) serversFixture {
	t.Helper()
	f := newServersFixture(t)
	// SetDescriptor installs descriptor storage, not its independent name index.
	// Wrapper validation requires the same identity binding as real staged DDL.
	for _, d := range []*catalog.ForeignObject{f.wrapper, f.server, f.second} {
		f.state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	return f
}

func pgfsInput(t *testing.T, f serversFixture, state *catalog.State) pgcatalog.Input {
	t.Helper()
	in := f.input(t, state, f.f.reader)
	in.Source = in.Catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.ForeignServerClassID})
	in.Ref = in.Owner.Domain.Ref(in.Catalog.Epoch(), in.Source.Address())
	return in
}

func pgfsRows(t *testing.T, in pgcatalog.Input) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(ids.ForeignServerClassID)
	if !ok {
		t.Fatal("missing server producer")
	}
	source, err := core.CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if source.Ref != in.Ref || source.View != nil || source.Anonymous == nil {
		t.Fatal("unowned source", source)
	}
	for _, row := range source.Anonymous {
		if len(row) != 8 {
			t.Fatal("width", row)
		}
		for i, value := range row {
			if value.Type() != in.Source.Columns[i].Usage.Type || !value.ValidResult(in.Source.Columns[i].Usage) {
				t.Fatal("source carrier", i, value)
			}
		}
	}
	return source.Anonymous
}

func pgfsOptions(t *testing.T, value sqltype.Value, want []string) {
	t.Helper()
	if value.IsNull() || value.ArrayLen() != len(want) || !slices.Equal(value.ArrayDimensions(), []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(want))}}) {
		t.Fatal("owned ordered array", value, want)
	}
	for i, expected := range want {
		text, err := value.ArrayElement(i).Text()
		if err != nil || text != expected || value.ArrayElement(i).Type() != value.Type().Element() {
			t.Fatal("option", i, text, err)
		}
	}
}

func TestPGForeignServerProducer(t *testing.T) {
	f := newPGFSFixture(t)
	in := pgfsInput(t, f, f.state)
	names := []string{"oid", "srvname", "srvowner", "srvfdw", "srvtype", "srvversion", "srvacl", "srvoptions"}
	types := []sqltype.ID{26, 19, 26, 26, 25, 25, 1034, 1009}
	for i, column := range in.Source.Columns {
		typ, _ := in.Catalog.LookupType(types[i])
		dimensions, collation := int16(0), uint32(0)
		if i >= 6 {
			dimensions = 1
		}
		if i == 1 || i == 4 || i == 5 || i == 7 {
			collation = 950
		}
		fact := in.Source.ColumnMeta.CatalogAttributes[i]
		if column.Name != names[i] || column.Usage.Type != typ || column.Usage.Typmod != -1 || column.Usage.Collation.ID != collation || column.Nullable != (i >= 4) || !column.NullabilityKnown || column.DeclaredDimensions != dimensions || column.OriginTable != 1417 || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) || fact.Dimensions != dimensions || fact.NotNull != (i < 4) || fact.Collation != collation {
			t.Fatal("schema authority", i, column, fact)
		}
	}
	if !pgcatalog.CatalogHasIndexes(1417) {
		t.Fatal("server unique index metadata")
	}
	rowType := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(in.Source.RowType)})
	if rowType.OwningRelation != in.Source.Address() {
		t.Fatal("source row type ownership")
	}
	rows := pgfsRows(t, in)
	if len(rows) != 2 {
		t.Fatal("decoy descriptor exclusion", rows)
	}
	row := rows[0]
	if id, _ := row[0].OID(); id != 22601 { row = rows[1] }
	for i, want := range map[int]uint32{0: 22601, 2: uint32(f.f.owner.ID()), 3: 22600} {
		got, err := row[i].OID()
		if err != nil || got != want {
			t.Fatal("identity", i, got, err)
		}
	}
	if name, _ := row[1].Text(); name != "view_server" {
		t.Fatal("name", name)
	}
	if kind, _ := row[4].Text(); kind != "é=🙂" {
		t.Fatal("server type", kind)
	}
	if version, _ := row[5].Text(); row[5].IsNull() || version != "" {
		t.Fatal("empty version is not NULL", version)
	}
	for _, candidate := range rows {
		if id, _ := candidate[0].OID(); id == 22602 {
			if !candidate[4].IsNull() || !candidate[5].IsNull() || !candidate[7].IsNull() {
				t.Fatal("nullable server fields", candidate)
			}
		}
	}
	pgfsOptions(t, row[7], []string{"password=server-secret", "empty="})
	if row[6].IsNull() || row[6].ArrayLen() != 1 || !slices.Equal(row[6].ArrayDimensions(), []sqltype.ArrayDimension{{Lower: 1, Length: 1}}) {
		t.Fatal("raw ACL", row[6])
	}
	for _, explicit := range []bool{false, true} {
		state := catalog.CloneState(f.state)
		server := *f.server
		server.Options = nil
		server.ACL = authorization.ACLValue{Explicit: explicit}
		state.SetDescriptor(server.Address(), &server)
		rows := pgfsRows(t, pgfsInput(t, f, state))
		row := rows[0]
		if id, _ := row[0].OID(); id != 22601 { row = rows[1] }
		if !row[7].IsNull() || row[6].IsNull() == explicit || explicit && !slices.Equal(row[6].ArrayDimensions(), []sqltype.ArrayDimension{{Lower: 1, Length: 0}}) {
			t.Fatal("NULL versus explicit empty ACL/options", row)
		}
	}
	if rows := pgfsRows(t, pgfsInput(t, f, f.f.e.Catalog.State)); len(rows) != 0 {
		t.Fatal("empty inventory", rows)
	}
}

func TestPGForeignServerAuthority(t *testing.T) {
	f := newPGFSFixture(t)
	produce, _ := pgcatalog.ProducerFor(ids.ForeignServerClassID)
	reject := func(in pgcatalog.Input) {
		t.Helper()
		rows, err := produce(in)
		var diagnostic interface{ SQLState() string }
		if rows != nil || err == nil || errors.As(err, &diagnostic) {
			t.Fatal("invalid authority accepted or fabricated SQLSTATE", rows, err)
		}
	}
	foreign := engine.New()
	foreignFixture := newPGFSFixture(t)
	foreignRuntime := pgfsInput(t, foreignFixture, foreignFixture.state).Runtime
	for _, state := range []*catalog.State{f.f.e.Catalog.State, f.state} {
		in := pgfsInput(t, f, state)
		for _, mutate := range []func(*pgcatalog.Input){
			func(in *pgcatalog.Input) { in.Runtime = nil },
			func(in *pgcatalog.Input) { in.Runtime = foreignRuntime },
			func(in *pgcatalog.Input) { r := in.Runtime.(core.EngineCatalogRuntime); r.Auth.Catalog = foreign.CatalogRead(); in.Runtime = r },
			func(in *pgcatalog.Input) { r := in.Runtime.(core.EngineCatalogRuntime); r.Auth.Roles = foreign.Roles(); in.Runtime = r },
			func(in *pgcatalog.Input) { r := in.Runtime.(core.EngineCatalogRuntime); r.Auth.Owner = &foreign.Catalog.Owner; in.Runtime = r },
			func(in *pgcatalog.Input) { in.Owner = nil },
			func(in *pgcatalog.Input) { in.Owner = &foreign.Catalog.Owner },
			func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() },
			func(in *pgcatalog.Input) { in.Roles = foreign.Roles() },
			func(in *pgcatalog.Input) { in.Source = nil },
			func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
			func(in *pgcatalog.Input) { in.Ref.Epoch++ },
			func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
			func(in *pgcatalog.Input) { in.Ref.Address.ObjectID++ },
			func(in *pgcatalog.Input) { in.Ref = foreign.Catalog.Domain.Ref(in.Ref.Epoch, in.Ref.Address) },
			func(in *pgcatalog.Input) { r := in.Runtime.(core.EngineCatalogRuntime); r.Auth.RoleRecord = nil; in.Runtime = r },
			func(in *pgcatalog.Input) { r := in.Runtime.(core.EngineCatalogRuntime); r.Auth.State.CurrentUser = foreign.RoleStore.Domain.Principal(10); in.Runtime = r },
		} {
			bad := in
			mutate(&bad)
			reject(bad)
		}
		for _, kind := range []string{"width", "attributes", "attribute", "name", "namespace", "virtual", "catalog-view", "kind", "column", "generation", "typmod", "collation", "nullable", "known", "dimensions", "origin", "origin-table", "facts-missing", "facts-truncated", "facts-extra", "facts-not-null", "facts-nullable", "facts-dimensions", "facts-collation"} {
			t.Run(kind, func(t *testing.T) {
				next := catalog.CloneState(state)
				source := *in.Source
				source.Columns = slices.Clone(source.Columns)
				source.Attributes = slices.Clone(source.Attributes)
				originalFacts := slices.Clone(in.Source.ColumnMeta.CatalogAttributes)
				source.ColumnMeta.CatalogAttributes = slices.Clone(source.ColumnMeta.CatalogAttributes)
				switch kind {
				case "width": source.Columns = source.Columns[:6]
				case "attributes": source.Attributes = source.Attributes[:6]
				case "attribute": source.Attributes[0] = 2
				case "name": source.ObjectName = "wrong"
				case "namespace": source.ObjectNamespace.ObjectID++
				case "virtual": source.VirtualView = true
				case "catalog-view": source.CatalogView = true
				case "kind": source.RelationKind = catalog.TableRelation
				case "column": source.Columns[0].Name = "wrong"
				case "generation": source.Columns[0].Usage.Type, _ = foreign.CatalogRead().LookupType(sqltype.Oid)
				case "typmod": source.Columns[0].Usage.Typmod = 0
				case "collation": source.Columns[1].Usage.Collation = sqltype.CollationRef{}
				case "nullable": source.Columns[0].Nullable = true
				case "known": source.Columns[0].NullabilityKnown = false
				case "dimensions": source.Columns[6].DeclaredDimensions = 0
				case "origin": source.Columns[0].OriginAttribute++
				case "origin-table": source.Columns[0].OriginTable++
				case "facts-missing": source.ColumnMeta.CatalogAttributes = nil
				case "facts-truncated": source.ColumnMeta.CatalogAttributes = source.ColumnMeta.CatalogAttributes[:6]
				case "facts-extra": source.ColumnMeta.CatalogAttributes = append(source.ColumnMeta.CatalogAttributes, source.ColumnMeta.CatalogAttributes[0])
				case "facts-not-null": source.ColumnMeta.CatalogAttributes[4].NotNull = true
				case "facts-nullable": source.ColumnMeta.CatalogAttributes[0].NotNull = false
				case "facts-dimensions": source.ColumnMeta.CatalogAttributes[6].Dimensions = 0
				case "facts-collation": source.ColumnMeta.CatalogAttributes[1].Collation = 0
				}
				next.SetDescriptor(source.Address(), &source)
				reject(pgfsInput(t, f, next))
				if !reflect.DeepEqual(in.Source.ColumnMeta.CatalogAttributes, originalFacts) {
					t.Fatal("malformed source fixture mutated captured fact backing")
				}
			})
		}
	}
	for _, kind := range []string{"owner", "grantor", "grantee", "privilege", "link", "wrong-class", "revision", "missing-name", "wrong-name"} {
		t.Run(kind, func(t *testing.T) {
			state := catalog.CloneState(f.state)
			server := *f.server
			server.ACL.Entries = slices.Clone(server.ACL.Entries)
			switch kind {
			case "owner": server.Owner = f.f.e.RoleStore.Domain.Principal(99999)
			case "grantor": server.ACL.Entries[0].Grantor = foreign.RoleStore.Domain.Principal(10)
			case "grantee": server.ACL.Entries[0].Grantee.Role = foreign.RoleStore.Domain.Principal(10)
			case "privilege": server.ACL.Entries[0].Privileges = authorization.SelectPrivilege
			case "link": server.Wrapper.ObjectID = 99999
			case "wrong-class": server.Wrapper.ClassID = ids.ForeignServerClassID
			case "revision": server.ObjectRevision = 0
			case "missing-name": state.UnbindName(catalog.DescriptorNameKey(&server))
			case "wrong-name": state.BindName(catalog.DescriptorNameKey(&server), ids.ObjectAddress{ClassID: ids.ForeignServerClassID, ObjectID: 99999})
			}
			state.SetDescriptor(server.Address(), &server)
			reject(pgfsInput(t, f, state))
		})
	}
	// Fixture reads are legitimate private roots paired through the real runtime.
	pgfsRows(t, pgfsInput(t, f, f.state))
}

func TestPGForeignServerCancellation(t *testing.T) {
	f := newPGFSFixture(t)
	produce, _ := pgcatalog.ProducerFor(ids.ForeignServerClassID)
	for _, state := range []*catalog.State{f.f.e.Catalog.State, f.state} {
		in := pgfsInput(t, f, state)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		in.Context = ctx
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("precanceled inventory", rows, err)
		}
		// Count an admitted traversal, then interrupt initial admission, skipped
		// inventory entries, mid-traversal and the final return. This is linear
		// in catalog size, with no timing assumptions or production hooks.
		const allowance = 1 << 30
		counter := &dtpCancelContext{Context: context.Background(), remaining: allowance}
		in.Context = counter
		if _, err := produce(in); err != nil {
			t.Fatal(err)
		}
		boundaries := allowance - counter.remaining
		if boundaries < 4 {
			t.Fatal("missing traversal/final cancellation boundaries", boundaries)
		}
		for _, remaining := range []int{1, 2, boundaries / 2, boundaries} {
			in.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
			if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("canceled prefix", remaining, rows, err)
			}
		}
	}
}

func TestPGForeignServerOwnership(t *testing.T) {
	f := newPGFSFixture(t)
	old := pgfsInput(t, f, f.state)
	rows := pgfsRows(t, old)
	retained := rows[0]
	if id, _ := retained[0].OID(); id != 22601 { retained = rows[1] }
	saved := slices.Clone(retained)
	rowBacking := &retained[0]
	optionsBacking := reflect.ValueOf(retained[7]).FieldByName("array").Pointer()
	aclBacking := reflect.ValueOf(retained[6]).FieldByName("array").Pointer()
	oldRecord := old.Runtime.(core.EngineCatalogRuntime).Auth.RoleRecord
	oldType := retained[7].Type()
	state := catalog.CloneState(f.state)
	server := *f.server
	server.ObjectName = "new_server"
	version := "replacement-version"
	server.Version = &version
	wrapper := *f.wrapper
	wrapper.ObjectAddress.ObjectID = 22610
	wrapper.ObjectName = "replacement_wrapper"
	wrapper.ObjectRevision++
	state.SetDescriptor(wrapper.Address(), &wrapper)
	state.BindName(catalog.DescriptorNameKey(&wrapper), wrapper.Address())
	server.Wrapper = wrapper.Address()
	server.Options = []catalog.ForeignOption{{Name: "replacement", Value: "new"}}
	server.ACL = authorization.ACLValue{Explicit: true}
	server.ObjectRevision++
	state.SetDescriptor(server.Address(), &server)
	state.UnbindName(catalog.DescriptorNameKey(f.server))
	state.BindName(catalog.DescriptorNameKey(&server), server.Address())
	fresh := pgfsInput(t, f, state)
	rows = pgfsRows(t, fresh)
	current := rows[0]
	if id, _ := current[0].OID(); id != 22601 { current = rows[1] }
	pgfsOptions(t, current[7], []string{"replacement=new"})
	if got, _ := current[5].Text(); got != version {
		t.Fatal("replacement version", got)
	}
	if got, _ := current[3].OID(); got != uint32(wrapper.Address().ObjectID) {
		t.Fatal("replacement wrapper relationship", got)
	}
	if got, _ := retained[5].Text(); got != "" || f.server.Version == server.Version {
		t.Fatal("retained version ownership", got)
	}
	if got, _ := retained[3].OID(); got != uint32(f.wrapper.Address().ObjectID) {
		t.Fatal("retained wrapper relationship", got)
	}
	if current[6].IsNull() || current[6].ArrayLen() != 0 || reflect.ValueOf(current[7]).FieldByName("array").Pointer() == optionsBacking {
		t.Fatal("new definition did not own new array backing")
	}
	// A fresh engine is an actual replacement generation, not equal OID authority.
	other := engine.New()
	otherType, _ := other.CatalogRead().LookupType(1009)
	if oldType == otherType || old.Catalog.Get(f.server.Address()) != f.server || old.Runtime.(core.EngineCatalogRuntime).Auth.RoleRecord != oldRecord || fresh.Catalog.Get(server.Address()) != &server {
		t.Fatal("captured owner/generation changed")
	}
	pgfsOptions(t, retained[7], []string{"password=server-secret", "empty="})
	if !reflect.DeepEqual(retained, saved) || &retained[0] != rowBacking || retained[7].Type() != oldType || reflect.ValueOf(retained[7]).FieldByName("array").Pointer() != optionsBacking || reflect.ValueOf(retained[6]).FieldByName("array").Pointer() != aclBacking || f.server.Options[0].Value != "server-secret" {
		t.Fatal("retained immutable array/type/backing changed")
	}
}
