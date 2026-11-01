package pgcatalog_test

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
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

type pgftFixture struct {
	serversFixture
	table, empty *catalog.Relation
}

func newPGFTFixture(t *testing.T) pgftFixture {
	t.Helper()
	f := newPGFSFixture(t)
	table := *f.f.e.Catalog.Read(f.state).MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 22604})
	table.RelationKind = catalog.ForeignRelation
	typ, _ := f.f.e.Catalog.Read(f.state).LookupType(sqltype.Int4)
	table.Columns = []catalog.Column{{Name: "a", Usage: typ.Usage(), Nullable: true, NullabilityKnown: true}}
	table.Attributes = []int32{1}
	table.Index = map[string]int{"a": 0}
	table.Foreign = &catalog.ForeignTableDefinition{Server: f.server.Address(), Options: []catalog.ForeignOption{{Name: "remote", Value: "private=value"}, {Name: "empty", Value: ""}, {Name: "unicode", Value: "é🙂"}}, Columns: map[int32][]catalog.ForeignOption{1: {{Name: "scope", Value: "column"}}}}
	empty := table
	empty.ObjectAddress.ObjectID++
	empty.ObjectName = "zero_columns"
	empty.Foreign = &catalog.ForeignTableDefinition{Server: f.second.Address()}
	empty.Columns, empty.Attributes, empty.Index = nil, nil, nil
	ordinary := table
	ordinary.ObjectAddress.ObjectID += 2
	ordinary.ObjectName = "ordinary_decoy"
	ordinary.RelationKind, ordinary.Foreign = catalog.TableRelation, nil
	view := ordinary
	view.ObjectAddress.ObjectID++
	view.ObjectName, view.RelationKind = "view_decoy", catalog.ViewRelation
	for _, d := range []*catalog.Relation{&table, &empty, &ordinary, &view} {
		f.state.SetDescriptor(d.Address(), d)
		f.state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	return pgftFixture{serversFixture: f, table: &table, empty: &empty}
}


func pgftInput(t *testing.T, f pgftFixture, state *catalog.State) pgcatalog.Input {
	t.Helper()
	in := f.input(t, state, f.f.reader)
	in.Source = in.Catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.ForeignTableClassID})
	in.Ref = in.Owner.Domain.Ref(in.Catalog.Epoch(), in.Source.Address())
	return in
}

func pgftRows(t *testing.T, in pgcatalog.Input) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(ids.ForeignTableClassID)
	if !ok { t.Fatal("missing foreign-table producer") }
	source, err := catalogrt.CaptureRows(in, produce)
	if err != nil { t.Fatal(err) }
	defer source.Close()
	if source.Ref != in.Ref || source.View != nil || source.Anonymous == nil { t.Fatal("unowned rows", source) }
	for _, row := range source.Anonymous {
		if len(row) != 3 { t.Fatal("width", row) }
		for i, value := range row {
			if value.Type() != in.Source.Columns[i].Usage.Type || !value.ValidResult(in.Source.Columns[i].Usage) { t.Fatal("carrier", i, value) }
		}
	}
	return source.Anonymous
}

func pgftRow(t *testing.T, rows [][]sqltype.Value, oid ids.OID) []sqltype.Value {
	t.Helper()
	for _, row := range rows { if got, _ := row[0].OID(); got == uint32(oid) { return row } }
	t.Fatal("missing foreign table", oid)
	return nil
}

func pgftReject(t *testing.T, in pgcatalog.Input) {
	t.Helper()
	produce, _ := pgcatalog.ProducerFor(ids.ForeignTableClassID)
	rows, err := produce(in)
	var diagnostic interface{ SQLState() string }
	if rows != nil || err == nil || errors.As(err, &diagnostic) { t.Fatal("invalid input accepted or SQLSTATE fabricated", rows, err) }
}

func TestPGForeignTableProducer(t *testing.T) {
	f := newPGFTFixture(t)
	in := pgftInput(t, f, f.state)
	for i, name := range []string{"ftrelid", "ftserver", "ftoptions"} {
		column, fact := in.Source.Columns[i], in.Source.ColumnMeta.CatalogAttributes[i]
		typeID, dimensions, collation := sqltype.ID(26), int16(0), uint32(0)
		if i == 2 { typeID, dimensions, collation = 1009, 1, 950 }
		typ, _ := in.Catalog.LookupType(typeID)
		if column.Name != name || column.Usage.Type != typ || column.Usage.Typmod != -1 || column.Usage.Collation.ID != collation || column.Nullable != (i == 2) || !column.NullabilityKnown || column.DeclaredDimensions != dimensions || column.OriginTable != 3118 || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) || fact.NotNull != (i != 2) || fact.Dimensions != dimensions || fact.Collation != collation { t.Fatal("exact source shape", i, column, fact) }
	}
	rowType := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(in.Source.RowType)})
	if rowType.OwningRelation != in.Source.Address() || !pgcatalog.CatalogHasIndexes(3118) || !in.Source.History.HasIndexes { t.Fatal("row type/index ownership") }
	rows := pgftRows(t, in)
	if len(rows) != 2 { t.Fatal("defined foreign tables only", rows) }
	for _, relation := range []*catalog.Relation{f.table, f.empty} {
		row := pgftRow(t, rows, relation.Address().ObjectID)
		if got, err := row[1].OID(); err != nil || got != uint32(relation.Foreign.Server.ObjectID) { t.Fatal("actual server link", got, err) }
		if relation == f.empty {
			if !row[2].IsNull() || row[2].Type().ID() != 1009 { t.Fatal("typed NULL options", row) }
		} else { pgfsOptions(t, row[2], []string{"remote=private=value", "empty=", "unicode=é🙂"}) }
	}
	if rows := pgftRows(t, pgftInput(t, f, f.f.e.Catalog.State)); len(rows) != 0 { t.Fatal("empty inventory", rows) }
}

func TestPGForeignTableAuthority(t *testing.T) {
	f, other := newPGFTFixture(t), newPGFTFixture(t)
	foreign := engine.New()
	for _, state := range []*catalog.State{f.f.e.Catalog.State, f.state} {
		in := pgftInput(t, f, state)
		for _, mutate := range []func(*pgcatalog.Input){
			func(in *pgcatalog.Input) { in.Runtime = nil },
			func(in *pgcatalog.Input) { in.Runtime = pgftInput(t, other, other.state).Runtime },
			func(in *pgcatalog.Input) { r := core.CatalogRuntimeFieldsOf(in.Runtime); r.Auth.Catalog = foreign.CatalogRead(); in.Runtime = core.NewCatalogRuntime(r) },
			func(in *pgcatalog.Input) { r := core.CatalogRuntimeFieldsOf(in.Runtime); r.Auth.Roles = foreign.Roles(); in.Runtime = core.NewCatalogRuntime(r) },
			func(in *pgcatalog.Input) { r := core.CatalogRuntimeFieldsOf(in.Runtime); r.Auth.Owner = &foreign.Catalog.Owner; in.Runtime = core.NewCatalogRuntime(r) },
			func(in *pgcatalog.Input) { r := core.CatalogRuntimeFieldsOf(in.Runtime); r.Auth.RoleRecord = nil; in.Runtime = core.NewCatalogRuntime(r) },
			func(in *pgcatalog.Input) { r := core.CatalogRuntimeFieldsOf(in.Runtime); r.Auth.RoleRecord = foreign.RoleStore.Published; in.Runtime = core.NewCatalogRuntime(r) },
			func(in *pgcatalog.Input) { r := core.CatalogRuntimeFieldsOf(in.Runtime); r.Auth.State.CurrentUser = foreign.RoleStore.Domain.Principal(10); in.Runtime = core.NewCatalogRuntime(r) },
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
		} { bad := in; mutate(&bad); pgftReject(t, bad) }
		for _, kind := range []string{"width", "attributes", "attnum", "name", "namespace", "kind", "virtual", "view", "column", "generation", "typmod", "collation", "nullable", "known", "dimensions", "origin", "origin-table", "facts-missing", "facts-short", "facts-extra", "facts-notnull", "facts-nullable", "facts-dimensions", "facts-collation"} {
			t.Run(kind, func(t *testing.T) {
				next := catalog.CloneState(state)
				source := *in.Source
				source.Columns, source.Attributes = slices.Clone(source.Columns), slices.Clone(source.Attributes)
				source.ColumnMeta.CatalogAttributes = slices.Clone(source.ColumnMeta.CatalogAttributes)
				original := slices.Clone(in.Source.ColumnMeta.CatalogAttributes)
				switch kind {
				case "width": source.Columns = source.Columns[:2]
				case "attributes": source.Attributes = source.Attributes[:2]
				case "attnum": source.Attributes[0]++
				case "name": source.ObjectName = "wrong"
				case "namespace": source.ObjectNamespace.ObjectID++
				case "kind": source.RelationKind = catalog.TableRelation
				case "virtual": source.VirtualView = true
				case "view": source.CatalogView = true
				case "column": source.Columns[0].Name = "wrong"
				case "generation": source.Columns[0].Usage.Type, _ = foreign.CatalogRead().LookupType(26)
				case "typmod": source.Columns[0].Usage.Typmod = 0
				case "collation": source.Columns[2].Usage.Collation = sqltype.CollationRef{}
				case "nullable": source.Columns[0].Nullable = true
				case "known": source.Columns[0].NullabilityKnown = false
				case "dimensions": source.Columns[2].DeclaredDimensions = 0
				case "origin": source.Columns[0].OriginAttribute++
				case "origin-table": source.Columns[0].OriginTable++
				case "facts-missing": source.ColumnMeta.CatalogAttributes = nil
				case "facts-short": source.ColumnMeta.CatalogAttributes = source.ColumnMeta.CatalogAttributes[:2]
				case "facts-extra": source.ColumnMeta.CatalogAttributes = append(source.ColumnMeta.CatalogAttributes, source.ColumnMeta.CatalogAttributes[0])
				case "facts-notnull": source.ColumnMeta.CatalogAttributes[2].NotNull = true
				case "facts-nullable": source.ColumnMeta.CatalogAttributes[0].NotNull = false
				case "facts-dimensions": source.ColumnMeta.CatalogAttributes[2].Dimensions = 0
				case "facts-collation": source.ColumnMeta.CatalogAttributes[2].Collation = 0
				}
				next.SetDescriptor(source.Address(), &source)
				pgftReject(t, pgftInput(t, f, next))
				if !reflect.DeepEqual(original, in.Source.ColumnMeta.CatalogAttributes) { t.Fatal("mutated captured backing") }
			})
		}
	}
}

func TestPGForeignTableDefinitions(t *testing.T) {
	f := newPGFTFixture(t)
	foreign := engine.New()
	for _, kind := range []string{"nonforeign", "class", "oid", "subid", "revision", "missing-name", "wrong-name", "namespace", "namespace-class", "owner", "foreign-owner", "grantor", "grantee", "privilege", "missing-server", "server-class", "server-subid", "server-zero", "server-owner", "server-acl", "server-revision", "server-name", "server-wrapper"} {
		t.Run(kind, func(t *testing.T) {
			state := catalog.CloneState(f.state)
			relation, definition, server := *f.table, *f.table.Foreign, *f.server
			relation.Foreign = &definition
			relation.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, 0)}}
			server.ACL.Entries = slices.Clone(server.ACL.Entries)
			switch kind {
			case "nonforeign": relation.RelationKind = catalog.TableRelation
			case "class": relation.ObjectAddress.ClassID = ids.ForeignTableClassID
			case "oid": relation.ObjectAddress.ObjectID = 0
			case "subid": relation.ObjectAddress.SubID = 1
			case "revision": relation.ObjectRevision = 0
			case "missing-name": state.UnbindName(catalog.DescriptorNameKey(&relation))
			case "wrong-name": state.BindName(catalog.DescriptorNameKey(&relation), f.empty.Address())
			case "namespace": relation.ObjectNamespace.ObjectID = 99999
			case "namespace-class": relation.ObjectNamespace.ClassID = ids.RelationClassID
			case "owner": relation.Owner = f.f.e.RoleStore.Domain.Principal(99999)
			case "foreign-owner": relation.Owner = foreign.RoleStore.Domain.Principal(10)
			case "grantor": relation.Security.ACL.Entries[0].Grantor = foreign.RoleStore.Domain.Principal(10)
			case "grantee": relation.Security.ACL.Entries[0].Grantee.Role = foreign.RoleStore.Domain.Principal(10)
			case "privilege": relation.Security.ACL.Entries[0].Privileges = authorization.UsagePrivilege
			case "missing-server": definition.Server.ObjectID = 99999
			case "server-class": definition.Server.ClassID = ids.ForeignWrapperClassID
			case "server-subid": definition.Server.SubID = 1
			case "server-zero": definition.Server.ObjectID = 0
			case "server-owner": server.Owner = foreign.RoleStore.Domain.Principal(10)
			case "server-acl": server.ACL.Entries[0].Grantor = foreign.RoleStore.Domain.Principal(10)
			case "server-revision": server.ObjectRevision = 0
			case "server-name": state.UnbindName(catalog.DescriptorNameKey(&server))
			case "server-wrapper": server.Wrapper.ObjectID = 99999
			}
			state.SetDescriptor(f.table.Address(), &relation)
			state.SetDescriptor(server.Address(), &server)
			pgftReject(t, pgftInput(t, f, state))
			switch kind {
			case "class", "oid", "subid", "revision", "missing-name", "wrong-name", "namespace", "namespace-class", "owner", "foreign-owner", "grantor", "grantee", "privilege":
				// A construction shell must pass the same relation authority,
				// namespace and ACL checks before its absent metadata is skipped.
				relation.Foreign = nil
				state.SetDescriptor(f.table.Address(), &relation)
				pgftReject(t, pgftInput(t, f, state))
			}
		})
	}
	// The table validator sees a private foreign-relation shell before the
	// definition is installed. It contributes no metadata row, not an error.
	state := catalog.CloneState(f.state)
	shell := *f.table
	shell.Foreign = nil
	state.SetDescriptor(shell.Address(), &shell)
	rows := pgftRows(t, pgftInput(t, f, state))
	if len(rows) != 1 { t.Fatal("construction shell emitted foreign metadata", rows) }
	pgftRow(t, rows, f.empty.Address().ObjectID)
	pgftRows(t, pgftInput(t, f, f.state))
}

type pgftCancelRuntime struct {
	pgcatalog.Runtime
	cancel context.CancelFunc
	remaining int
}

func (r *pgftCancelRuntime) ValidateACL(roles authorization.Read, d catalog.Descriptor) error {
	if err := r.Runtime.ValidateACL(roles, d); err != nil { return err }
	r.remaining--
	if r.remaining == 0 { r.cancel() }
	return nil
}

func TestPGForeignTableCancellationAndRetention(t *testing.T) {
	f := newPGFTFixture(t)
	produce, _ := pgcatalog.ProducerFor(ids.ForeignTableClassID)
	for _, state := range []*catalog.State{f.f.e.Catalog.State, f.state} {
		in := pgftInput(t, f, state)
		ctx, cancel := context.WithCancel(t.Context()); cancel(); in.Context = ctx
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) { t.Fatal("precanceled inventory", rows, err) }
		const allowance = 1 << 30
		counter := &dtpCancelContext{Context: context.Background(), remaining: allowance}
		in.Context = counter
		if _, err := produce(in); err != nil { t.Fatal(err) }
		boundaries := allowance-counter.remaining
		for _, boundary := range []int{0, 1, boundaries/2, boundaries-1} {
			in.Context = &dtpCancelContext{Context: context.Background(), remaining: boundary}
			if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) { t.Fatal("partial publication", boundary, rows, err) }
		}
	}
	// Cancel during admitted definition validation, including the final selected
	// server: neither an earlier row nor the final row may be published.
	for _, boundary := range []int{2, 4} {
		in := pgftInput(t, f, f.state)
		ctx, cancel := context.WithCancel(t.Context())
		in.Context = ctx
		in.Runtime = &pgftCancelRuntime{Runtime: in.Runtime, cancel: cancel, remaining: boundary}
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) { t.Fatal("ACL traversal cancellation", rows, err) }
		cancel()
	}
	old := pgftInput(t, f, f.state)
	retained := pgftRow(t, pgftRows(t, old), f.table.Address().ObjectID)
	saved, backing, typ := slices.Clone(retained), &retained[0], retained[2].Type()
	arrayBacking := reflect.ValueOf(retained[2]).FieldByName("array").Pointer()
	metadata := slices.Clone(old.Source.Columns)
	state := catalog.CloneState(f.state)
	table, definition := *f.table, *f.table.Foreign
	definition.Options = []catalog.ForeignOption{{Name: "replacement", Value: "new"}}
	table.Foreign, table.ObjectRevision = &definition, table.ObjectRevision+1
	state.SetDescriptor(table.Address(), &table)
	fresh := pgftInput(t, f, state)
	pgfsOptions(t, pgftRow(t, pgftRows(t, fresh), table.Address().ObjectID)[2], []string{"replacement=new"})
	replacement := newPGFTFixture(t)
	nextTable, nextDefinition := *replacement.table, *replacement.table.Foreign
	nextDefinition.Options = []catalog.ForeignOption{{Name: "replacement", Value: "new"}}
	nextTable.Foreign, nextTable.ObjectRevision = &nextDefinition, nextTable.ObjectRevision+1
	replacement.state.SetDescriptor(nextTable.Address(), &nextTable)
	replacementInput := pgftInput(t, replacement, replacement.state)
	current := pgftRow(t, pgftRows(t, replacementInput), replacement.table.Address().ObjectID)
	if current[2].Type() == typ || current[2].Type() != replacementInput.Source.Columns[2].Usage.Type { t.Fatal("replacement type generation") }
	pgfsOptions(t, current[2], []string{"replacement=new"})
	pgfsOptions(t, retained[2], []string{"remote=private=value", "empty=", "unicode=é🙂"})
	if !reflect.DeepEqual(retained, saved) || &retained[0] != backing || retained[2].Type() != typ || reflect.ValueOf(retained[2]).FieldByName("array").Pointer() != arrayBacking || !reflect.DeepEqual(metadata, old.Source.Columns) || old.Catalog.Get(f.table.Address()) != f.table || f.table.Foreign.Options[0].Value != "private=value" { t.Fatal("retained actual rows/metadata changed") }
}
