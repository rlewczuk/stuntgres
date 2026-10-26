package core_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

var amopUnitNames = []string{"oid", "amopfamily", "amoplefttype", "amoprighttype", "amopstrategy", "amoppurpose", "amopopr", "amopmethod", "amopsortfamily"}

func amopUnitSource(t *testing.T, e *core.Engine, oid ids.OID) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}
	d, ok := e.Catalog.State.Relation(a)
	if !ok { t.Fatal("missing source", oid) }
	return d, e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), a)
}

func amopUnitCapture(view catalog.Read, d *catalog.Relation, ref ids.ObjectRef, ctx context.Context) (*exec.Source, error) {
	return core.CaptureCatalogSourceRows(pgcatalog.Input{Context: ctx, Catalog: view, Ref: ref, Source: d, Owner: view.Owner(), Runtime: core.EngineCatalogRuntime{}}, registeredCatalogProducer(2602))
}

func amopUnitBag(t *testing.T, rows *exec.Source, d *catalog.Relation) map[uint32][]any {
	t.Helper()
	bag := map[uint32][]any{}
	for _, row := range rows.Anonymous {
		if len(row) != 9 || cap(row) != 9 { t.Fatal("detached width", len(row), cap(row)) }
		values := make([]any, 9)
		for i, v := range row {
			if v.IsNull() || v.Type() != d.Columns[i].Usage.Type { t.Fatal("foreign/null cell", i) }
			var err error
			switch i {
			case 4: values[i], err = v.Integer()
			case 5: values[i], err = v.Text()
			default: values[i], err = v.OID()
			}
			if err != nil { t.Fatal(err) }
		}
		oid := values[0].(uint32)
		if oid == 0 || bag[oid] != nil { t.Fatal("duplicate identity", oid) }
		bag[oid] = values
	}
	return bag
}

func TestPGAmopCaptureOwnership(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	d, ref := amopUnitSource(t, e, 2602)
	if d.Name() != "pg_amop" || d.Namespace().ObjectID != 11 || d.RowType != 10017 || d.RelationKind != catalog.CatalogRelation || !d.History.HasIndexes || d.ColumnMeta.HighWater != 9 || len(d.Columns) != 9 { t.Fatal("descriptor", d) }
	for i, name := range amopUnitNames {
		id := sqltype.Oid
		if i == 4 { id = sqltype.Int2 }; if i == 5 { id = sqltype.Char }
		typ, ok := view.LookupType(id)
		c := d.Columns[i]
		if !ok || c.Name != name || c.Usage.Type != typ || c.Usage.Typmod != -1 || c.Usage.Collation.ID != 0 || c.Nullable || !c.NullabilityKnown || c.OriginTable != 2602 || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[name] != i { t.Fatal("column", i, c) }
	}
	if len(d.Security.ACL.Entries) != 1 || !d.Security.ACL.Entries[0].Grantee.Public || d.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege { t.Fatal("PUBLIC SELECT") }
	if err := core.RelationProvider.Validate(view, d); err != nil { t.Fatal(err) }
	descriptors, names := maps.Collect(view.Descriptors()), maps.Collect(view.Names())
	rows, err := amopUnitCapture(view, d, ref, context.Background())
	if err != nil { t.Fatal(err) }
	bag := amopUnitBag(t, rows, d)
	if rows.Ref != ref || len(bag) != 638 { t.Fatal("capture identity/count", rows.Ref, len(bag)) }
	owners := 0
	for _, descriptor := range view.Descriptors() {
		m, ok := descriptor.(*catalog.OperatorFamilyOperator); if !ok { continue }; owners++
		want := []any{uint32(m.Address().ObjectID), uint32(m.Family), uint32(m.Left), uint32(m.Right), int64(m.Number), string([]byte{m.Purpose}), uint32(m.Operator), uint32(m.Method), uint32(m.SortFamily)}
		if !reflect.DeepEqual(bag[uint32(m.Address().ObjectID)], want) { t.Fatal("owner projection", m) }
	}
	if owners != len(bag) { t.Fatal("invented membership") }
	for _, want := range [][]any{
		{uint32(10010),uint32(1976),uint32(21),uint32(20),int64(1),"s",uint32(1864),uint32(403),uint32(0)},
		{uint32(10015),uint32(1976),uint32(23),uint32(23),int64(1),"s",uint32(97),uint32(403),uint32(0)},
		{uint32(10017),uint32(1976),uint32(23),uint32(23),int64(3),"s",uint32(96),uint32(403),uint32(0)},
		{uint32(10264),uint32(1977),uint32(23),uint32(23),int64(1),"s",uint32(96),uint32(405),uint32(0)},
		{uint32(10319),uint32(1029),uint32(600),uint32(600),int64(15),"o",uint32(517),uint32(783),uint32(1970)},
	} { if !reflect.DeepEqual(bag[want[0].(uint32)], want) { t.Fatal("released witness", want) } }
	again, err := amopUnitCapture(view, d, ref, context.Background())
	if err != nil || &again.Anonymous[0][0] == &rows.Anonymous[0][0] { t.Fatal("capture storage reused", err) }
	ctx, cancel := context.WithCancel(context.Background()); cancel()
	during, stop := context.WithCancel(context.Background()); defer stop()
	for _, ctx := range []context.Context{ctx, &cancelAtErrCheck{Context:during, cancel:stop, at:5}} {
		if partial, err := amopUnitCapture(view,d,ref,ctx); partial != nil || !errors.Is(err,context.Canceled) { t.Fatal("canceled partial",partial,err) }
	}
	if !maps.Equal(descriptors,maps.Collect(view.Descriptors())) || !maps.Equal(names,maps.Collect(view.Names())) { t.Fatal("capture mutated catalog") }
	backing := &rows.Anonymous[0][0]
	if err := engine.Reset(context.Background(), e); err != nil { t.Fatal(err) }
	fresh, freshRef := amopUnitSource(t,e,2602)
	freshRows, err := amopUnitCapture(e.CatalogRead(),fresh,freshRef,context.Background())
	if err != nil { t.Fatal(err) }
	if fresh.Columns[0].Usage.Type == d.Columns[0].Usage.Type || &rows.Anonymous[0][0] != backing || !reflect.DeepEqual(amopUnitBag(t,rows,d),bag) || !reflect.DeepEqual(amopUnitBag(t,freshRows,fresh),bag) { t.Fatal("generation/storage changed facts") }
	if invalid, err := amopUnitCapture(e.CatalogRead(),d,freshRef,context.Background()); invalid != nil || err == nil { t.Fatal("old source accepted",invalid,err) }
}

func TestPGAmopCaptureValidation(t *testing.T) {
	e, foreign := engine.New(), engine.New()
	for _, oid := range []ids.OID{2602,2603,2753} {
		d, ref := amopUnitSource(t,e,oid)
		view := e.CatalogRead()
		empty := catalog.CloneState(view.State())
		for a := range empty.Descriptors() { if a.ClassID == oid { empty.RemoveDescriptor(a) } }
		for _, view := range []catalog.Read{view,view.WithState(empty)} {
			capture := func(source *catalog.Relation) (*exec.Source,error) { return core.CaptureCatalogSourceRows(pgcatalog.Input{Context:context.Background(),Catalog:view,Ref:ref,Source:source,Owner:view.Owner(),Runtime:core.EngineCatalogRuntime{}},registeredCatalogProducer(oid)) }
			valid, err := capture(d)
			if valid == nil || err != nil { t.Fatal("valid shape",oid,err) }
			for _, row := range valid.Anonymous {
				if len(row) != len(d.Columns) || cap(row) != len(row) { t.Fatal("valid source width",oid) }
				for i,v := range row { if v.IsNull() || v.Type() != d.Columns[i].Usage.Type { t.Fatal("valid source ownership",oid,i) } }
			}
			if view.State() == empty && len(valid.Anonymous) != 0 { t.Fatal("not an empty owner inventory",oid) }
			var invalid []*catalog.Relation
			for _, width := range []int{len(d.Columns)-1,len(d.Columns)+1} {
				bad := *d; bad.Columns = make([]catalog.Column,width); copy(bad.Columns,d.Columns); invalid = append(invalid,&bad)
			}
			for i := range d.Columns {
				for _, typ := range []*sqltype.Descriptor{nil, func() *sqltype.Descriptor { v,_ := view.LookupType(sqltype.Bool); return v }(), func() *sqltype.Descriptor { v,_ := foreign.CatalogRead().LookupType(d.Columns[i].Usage.Type.ID()); return v }()} {
					bad := *d; bad.Columns = append([]catalog.Column(nil),d.Columns...); bad.Columns[i].Usage.Type = typ; invalid = append(invalid,&bad)
				}
			}
			bad := *d; bad.ObjectAddress.ObjectID = 999999; invalid = append(invalid,&bad)
			for _, source := range invalid {
				rows, err := capture(source)
				if rows != nil || err == nil { t.Fatal("invalid source accepted",oid,rows,err) }
				var state interface{ SQLState() string }; if errors.As(err,&state) { t.Fatal("internal error gained SQLSTATE",err) }
			}
			missing := catalog.CloneState(view.State())
			missing.RemoveDescriptor(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(sqltype.Oid)})
			missingView := view.WithState(missing)
			rows, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context:context.Background(),Catalog:missingView,Ref:ref,Source:d,Owner:view.Owner(),Runtime:core.EngineCatalogRuntime{}},registeredCatalogProducer(oid))
			if rows != nil || err == nil { t.Fatal("missing owned type accepted",oid,rows,err) }
		}
	}
}
