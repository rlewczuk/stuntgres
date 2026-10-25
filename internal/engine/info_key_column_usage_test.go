package engine

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

type kcuUnitFixture struct {
	f                    defaultUnitFixture
	state                *catalog.State
	parent, child, index *catalog.Relation
}

func newKCUUnitFixture(t *testing.T) kcuUnitFixture {
	t.Helper()
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	typ, _ := f.e.Catalog.Read(state).LookupType(sqltype.Int4)
	relation := func(oid ids.OID, name string) *catalog.Relation {
		return &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: f.schema, ObjectName: name, ObjectRevision: 1, Owner: f.owner, RelationKind: catalog.TableRelation, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true}}, Attributes: []int32{1, 3}, Columns: []catalog.Column{{Name: "a", Usage: typ.Usage()}, {Name: "b", Usage: typ.Usage()}}}
	}
	parent := relation(20400, "kcu_unit_parent")
	parent.Attributes = append(parent.Attributes, 4)
	parent.Columns = append(parent.Columns, catalog.Column{Name: "payload", Usage: typ.Usage()})
	index := relation(20401, "kcu_unit_provider")
	index.SQLIndex = &catalog.IndexDefinition{Parent: parent.Address(), Unique: true, Keys: []catalog.IndexKey{{Attribute: 3}, {Attribute: 1}}, Include: []int32{4}}
	// The other equivalent provider deliberately has the opposite order. Capture
	// must follow fk.Index rather than picking an arbitrary matching index.
	other := relation(20402, "kcu_unit_other")
	other.SQLIndex = &catalog.IndexDefinition{Parent: parent.Address(), Unique: true, Keys: []catalog.IndexKey{{Attribute: 1}, {Attribute: 3}}}
	index.RelationKind = catalog.IndexRelation
	other.RelationKind = catalog.IndexRelation
	parent.Constraints.Unique = []catalog.UniqueConstraint{{Name: "same", Attributes: []int32{1, 3}, Index: other.Address(), Primary: true}}
	child := relation(20403, "kcu_unit_child")
	child.Constraints.ForeignKeys = []catalog.ForeignKeyConstraint{{Name: "same", Attributes: []int32{3, 1}, Target: parent.Address(), Referenced: []int32{1, 3}, Index: index.Address()}}
	child.Security.ColumnACL = map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.SelectPrivilege, 0)}}}
	for _, r := range []*catalog.Relation{parent, index, other, child} {
		state.SetDescriptor(r.Address(), r)
	}
	return kcuUnitFixture{f, state, parent, child, index}
}
func (f kcuUnitFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f.f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.KeyColumnUsageOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: EngineCatalogRuntime{Auth: auth, Database: "unit_database"}}
}
func kcuUnitRows(t *testing.T, in pgcatalog.Input) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.KeyColumnUsageOID)
	if !ok {
		t.Fatal("missing producer")
	}
	captured, err := CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.Anonymous == nil || captured.View != nil || captured.Ref != in.Ref {
		t.Fatal("anonymous source identity", captured)
	}
	return captured.Anonymous
}
func kcuUnitBag(t *testing.T, rows [][]sqltype.Value) map[string]int {
	t.Helper()
	bag := map[string]int{}
	for _, row := range rows {
		if infoUserMappingsUnitText(t, row[0]) != "unit_database" || infoUserMappingsUnitText(t, row[3]) != "unit_database" {
			t.Fatal("database identity", row)
		}
		table := infoUserMappingsUnitText(t, row[5])
		column := infoUserMappingsUnitText(t, row[6])
		name := infoUserMappingsUnitText(t, row[2])
		ordinal, err := row[7].BaseValue()
		if err != nil {
			t.Fatal(err)
		}
		n, err := ordinal.Integer()
		if err != nil {
			t.Fatal(err)
		}
		position := "NULL"
		if !row[8].IsNull() {
			base, err := row[8].BaseValue()
			if err != nil {
				t.Fatal(err)
			}
			p, err := base.Integer()
			if err != nil {
				t.Fatal(err)
			}
			position = strconv.FormatInt(p, 10)
		}
		key := table + "/" + name + "/" + column + "/" + strconv.FormatInt(n, 10) + "/" + position
		bag[key]++
	}
	return bag
}
func TestInfoKeyColumnUsageProducer(t *testing.T) {
	f := newKCUUnitFixture(t)
	in := f.input(t, f.state, f.f.owner)
	rows := kcuUnitRows(t, in)
	want := map[string]int{"kcu_unit_parent/same/a/1/NULL": 1, "kcu_unit_parent/same/b/2/NULL": 1, "kcu_unit_child/same/b/1/2": 1, "kcu_unit_child/same/a/2/1": 1}
	if got := kcuUnitBag(t, rows); !reflect.DeepEqual(got, want) {
		t.Fatal("key bag", got, want)
	}
	for _, row := range rows {
		if len(row) != 9 {
			t.Fatal("projection", row)
		}
		for i, value := range row {
			col := in.Source.Columns[i]
			domain, base := sqltype.ID(13248), sqltype.Name
			if i >= 7 {
				domain, base = 13243, sqltype.Int4
			}
			payload, err := value.BaseValue()
			if err != nil || value.Type() != col.Usage.Type || value.Type().ID() != domain || payload.Type().ID() != base || !value.ValidResult(col.Usage) || !col.Nullable || !col.NullabilityKnown || col.OriginTable != 16204 || col.OriginAttribute != int16(i+1) || col.DeclaredDimensions != 0 || col.Usage.Typmod != -1 {
				t.Fatal("carrier/origin", i, value, col, err)
			}
		}
	}
	if in.Source.RowType != 13352 {
		t.Fatal("native row identity", in.Source.RowType)
	}
	rowType, ok := in.Catalog.LookupType(13352)
	if !ok {
		t.Fatal("row type absent")
	}
	arrayType, ok := in.Catalog.LookupType(13351)
	if !ok || arrayType.Element() != rowType {
		t.Fatal("canonical array identity")
	}
	descriptor, ok := in.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13352})
	if !ok || descriptor.OwningRelation != in.Source.Address() {
		t.Fatal("row relation backlink")
	}
	for _, kind := range []catalog.RelationKind{catalog.ViewRelation, catalog.ForeignRelation, catalog.PartitionedRelation} {
		state := catalog.CloneState(f.state)
		child := *f.child
		child.RelationKind = kind
		state.SetDescriptor(child.Address(), &child)
		wantCount := 2
		if kind == catalog.PartitionedRelation {
			wantCount = 4
		}
		if rows := kcuUnitRows(t, f.input(t, state, f.f.owner)); len(rows) != wantCount {
			t.Fatal("relation kind population", kind, len(rows))
		}
	}
	empty := catalog.CloneState(f.f.e.Catalog.State)
	if rows := kcuUnitRows(t, f.input(t, empty, f.f.owner)); len(rows) != 0 {
		t.Fatal("invented keys", rows)
	}
}
func TestInfoKeyColumnUsageCapture(t *testing.T) {
	f := newKCUUnitFixture(t)
	want := map[string]int{"kcu_unit_child/same/a/2/1": 1}
	in := f.input(t, f.state, f.f.reader)
	held := kcuUnitRows(t, in)
	if got := kcuUnitBag(t, held); !reflect.DeepEqual(got, want) {
		t.Fatal("partial visibility", got)
	}
	changed := catalog.CloneState(f.state)
	child := *f.child
	child.ObjectName = "kcu_unit_renamed"
	child.Security.ColumnACL = nil
	changed.SetDescriptor(child.Address(), &child)
	if rows := kcuUnitRows(t, f.input(t, changed, f.f.reader)); len(rows) != 0 {
		t.Fatal("revoked visibility", rows)
	}
	if got := kcuUnitBag(t, held); !reflect.DeepEqual(got, want) {
		t.Fatal("retained capture changed", got)
	}
	for _, actor := range []authorization.PrincipalRef{f.f.owner, f.f.member} {
		if rows := kcuUnitRows(t, f.input(t, f.state, actor)); len(rows) != 4 {
			t.Fatal("owner inheritance", len(rows))
		}
	}
	temporary := catalog.CloneState(f.state)
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20410}, ObjectName: "pg_temp_kcu_unit", ObjectRevision: 1, Owner: f.f.owner, Temporary: in.Owner.Domain.Session(100)}
	temporary.SetDescriptor(ns.Address(), ns)
	tempChild := *f.child
	tempChild.ObjectNamespace = ns.Address()
	temporary.SetDescriptor(tempChild.Address(), &tempChild)
	tempInput := f.input(t, temporary, f.f.owner)
	if rows := kcuUnitRows(t, tempInput); len(rows) != 2 {
		t.Fatal("peer TEMP disclosed", len(rows))
	}
	runtime := tempInput.Runtime.(EngineCatalogRuntime)
	runtime.temporary = ns.Temporary
	tempInput.Runtime = runtime
	if rows := kcuUnitRows(t, tempInput); len(rows) != 4 {
		t.Fatal("own TEMP omitted", len(rows))
	}
	ownerRows := kcuUnitRows(t, f.input(t, changed, f.f.owner))
	renamed := map[string]int{"kcu_unit_parent/same/a/1/NULL": 1, "kcu_unit_parent/same/b/2/NULL": 1, "kcu_unit_renamed/same/b/1/2": 1, "kcu_unit_renamed/same/a/2/1": 1}
	if got := kcuUnitBag(t, ownerRows); !reflect.DeepEqual(got, renamed) {
		t.Fatal("fresh rename", got)
	}
	dropped := catalog.CloneState(changed)
	without := child
	without.Constraints.ForeignKeys = nil
	dropped.SetDescriptor(without.Address(), &without)
	if rows := kcuUnitRows(t, f.input(t, dropped, f.f.owner)); len(rows) != 2 {
		t.Fatal("fresh dropped constraint", len(rows))
	}
	if got := kcuUnitBag(t, ownerRows); !reflect.DeepEqual(got, renamed) {
		t.Fatal("drop changed retained rows", got)
	}
	produce, _ := pgcatalog.ProducerFor(pgcatalog.KeyColumnUsageOID)
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &New().Catalog.Owner },
		func(in *pgcatalog.Input) { in.Roles = New().Roles() },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) {
			r := in.Runtime.(EngineCatalogRuntime)
			r.Auth.RoleRecord = nil
			in.Runtime = r
		},
	} {
		bad := in
		mutate(&bad)
		if rows, err := produce(bad); rows != nil || err == nil {
			t.Fatal("invalid capture accepted", rows, err)
		}
	}
	for _, kind := range []string{"width", "domain", "index", "target"} {
		state := catalog.CloneState(f.state)
		switch kind {
		case "width", "domain":
			source := *in.Source
			source.Columns = append([]catalog.Column(nil), source.Columns...)
			if kind == "width" {
				source.Columns = source.Columns[:8]
			} else {
				source.Columns[0].Usage.Type, _ = in.Catalog.LookupType(sqltype.Name)
			}
			state.SetDescriptor(source.Address(), &source)
		case "index":
			index := *f.index
			definition := *index.SQLIndex
			definition.Parent = f.child.Address()
			index.SQLIndex = &definition
			state.SetDescriptor(index.Address(), &index)
		case "target":
			child := *f.child
			child.Constraints.ForeignKeys = append([]catalog.ForeignKeyConstraint(nil), child.Constraints.ForeignKeys...)
			child.Constraints.ForeignKeys[0].Referenced = []int32{2, 3}
			state.SetDescriptor(child.Address(), &child)
		}
		if rows, err := produce(f.input(t, state, f.f.owner)); rows != nil || err == nil {
			t.Fatal("malformed metadata accepted", kind, rows, err)
		}
	}
}

// This wrapper delegates every metadata/privilege result to the real runtime.
// Only cancellation timing is controlled, at actual column/index walk entries.
type kcuCancelRuntime struct {
	EngineCatalogRuntime
	child                        ids.ObjectAddress
	cancel                       context.CancelFunc
	effectiveCalls, indexCalls   int
	cancelEffective, cancelIndex int
}

func (r *kcuCancelRuntime) Effective(ref ids.ObjectRef, owner bool) (authorization.Privileges, error) {
	if ref.Address.ClassID == r.child.ClassID && ref.Address.ObjectID == r.child.ObjectID {
		r.effectiveCalls++
		if r.effectiveCalls == r.cancelEffective {
			r.cancel()
		}
	}
	return r.EngineCatalogRuntime.Effective(ref, owner)
}

func (r *kcuCancelRuntime) IndexSimpleAttribute(parent ids.ObjectAddress, key catalog.IndexKey) (int32, bool) {
	r.indexCalls++
	if r.indexCalls == r.cancelIndex {
		r.cancel()
	}
	return r.EngineCatalogRuntime.IndexSimpleAttribute(parent, key)
}

func TestInfoKeyColumnUsageCancellation(t *testing.T) {
	f := newKCUUnitFixture(t)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.KeyColumnUsageOID)
	// A constant number of captures keeps this proof linear in catalog size.
	// Check entry and descriptor enumeration without rebuilding authority per
	// descriptor or sweeping every possible cancellation checkpoint.
	base := f.input(t, f.state, f.f.owner)
	for _, remaining := range []int{1, 4} {
		in := base
		in.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("entry/descriptor cancellation exposed rows", remaining, rows, err)
		}
	}
	if rows, err := produce(base); err != nil || len(rows) != 4 {
		t.Fatal("successful populated capture", rows, err)
	}
	// Make the child's first member reference index key 1 and its second
	// member reference key 2. The second member therefore starts only after
	// one complete child row, regardless of catalog traversal order.
	state := catalog.CloneState(f.state)
	child := *f.child
	child.Constraints.ForeignKeys = append([]catalog.ForeignKeyConstraint(nil), child.Constraints.ForeignKeys...)
	child.Constraints.ForeignKeys[0].Referenced = []int32{3, 1}
	child.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, 0)}}
	state.SetDescriptor(child.Address(), &child)
	for _, stage := range []string{"column", "index"} {
		actor := f.f.owner
		if stage == "column" {
			actor = f.f.reader
		}
		in := f.input(t, state, actor)
		ctx, cancel := context.WithCancel(t.Context())
		in.Context = ctx
		runtime := &kcuCancelRuntime{EngineCatalogRuntime: in.Runtime.(EngineCatalogRuntime), child: child.Address(), cancel: cancel}
		if stage == "column" {
			runtime.cancelEffective = 2
		} else {
			runtime.cancelIndex = 2
		}
		in.Runtime = runtime
		rows, err := produce(in)
		cancel()
		if rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("column/index cancellation exposed prefix", stage, rows, err)
		}
		if stage == "column" && (runtime.effectiveCalls != 2 || runtime.indexCalls != 1) {
			t.Fatal("column cancellation did not follow first complete row", runtime.effectiveCalls, runtime.indexCalls)
		}
		if stage == "index" && runtime.indexCalls != 2 {
			t.Fatal("index cancellation did not follow first complete row", runtime.indexCalls)
		}
	}
	empty := f.input(t, catalog.CloneState(f.f.e.Catalog.State), f.f.owner)
	if rows, err := produce(empty); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("successful empty capture", rows, err)
	}
	in := f.input(t, catalog.CloneState(f.f.e.Catalog.State), f.f.owner)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	in.Context = ctx
	if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("pre-canceled empty source", rows, err)
	}
}
