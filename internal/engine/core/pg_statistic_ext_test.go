package core_test

import (
	"context"
	"errors"
	"maps"
	"testing"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func statisticExtUnitSource(e *core.Engine) (*catalog.Relation,ids.ObjectRef) {
	a := ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.StatisticExtOID}
	return e.CatalogRead().MustRelation(a),e.Catalog.Domain.Ref(e.Catalog.State.Epoch(),a)
}

func TestPGStatisticExtRegistrationFacts(t *testing.T) {
	e := engine.New(); view := e.CatalogRead(); d,_ := statisticExtUnitSource(e)
	if d.Name() != "pg_statistic_ext" || d.Namespace().ObjectID != 11 || d.RowType != 10031 || !d.History.HasIndexes || d.VirtualView || d.CatalogView || d.RelationKind != catalog.CatalogRelation || d.ColumnMeta.HighWater != 9 || len(d.Columns) != 9 || !pgcatalog.RegisteredSource(view,d) { t.Fatal("registration",d) }
	if _,ok := pgcatalog.ProducerFor(pgcatalog.StatisticExtOID); !ok { t.Fatal("missing producer") }
	registry := pgcatalog.Registrations()
	for i,r := range registry { if i > 0 && registry[i-1].OID >= r.OID { t.Fatal("unordered producer registry") } }
	names := [...]string{"oid","stxrelid","stxname","stxnamespace","stxowner","stxkeys","stxstattarget","stxkind","stxexprs"}
	types := [...]sqltype.ID{26,26,19,26,26,22,21,1002,194}
	for i,c := range d.Columns {
		owned,ok := view.LookupType(types[i]); nullable := i == 6 || i == 8
		collation := uint32(0); if i == 2 || i == 8 { collation = 950 }
		dims := int16(0); if i == 5 || i == 7 { dims = 1 }
		if !ok || c.Name != names[i] || c.Usage.Type != owned || c.Usage.Typmod != -1 || uint32(c.Usage.Collation.ID) != collation || c.Nullable != nullable || !c.NullabilityKnown || c.OriginTable != 3381 || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[c.Name] != i { t.Fatal("column",i,c) }
		a := d.ColumnMeta.CatalogAttributes[i]
		if a.NotNull == nullable || a.Dimensions != dims || uint32(a.Collation) != collation { t.Fatal("attribute",i,a) }
	}
	if len(d.Security.ACL.Entries) != 1 || !d.Security.ACL.Entries[0].Grantee.Public || d.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege { t.Fatal("PUBLIC SELECT",d.Security.ACL) }
	row := view.MustType(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:10031})
	array := view.MustType(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:10030})
	if row.Array != 10030 || array.SQLType.Element() != row.SQLType || row.OwningRelation != d.Address() || array.OwningRelation != d.Address() || row.Owner != d.Owner || array.Owner != d.Owner { t.Fatal("row/array",row,array) }
	if err := core.RelationProvider.Validate(view,d); err != nil { t.Fatal(err) }
}

func TestPGStatisticExtCaptureOwnership(t *testing.T) {
	e := engine.New(); view := e.CatalogRead(); d,ref := statisticExtUnitSource(e)
	produce,ok := pgcatalog.ProducerFor(pgcatalog.StatisticExtOID); if !ok { t.Fatal("missing producer") }
	roles := e.Roles()
	capture := func(ctx context.Context,v catalog.Read,s *catalog.Relation,r ids.ObjectRef,roles authorization.Read) {
		t.Helper()
		got,err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context:ctx,Catalog:v,Source:s,Ref:r,Owner:v.Owner(),Roles:roles,Runtime:core.EngineCatalogRuntime{}},produce)
		if err != nil || got == nil || got.Anonymous == nil || len(got.Anonymous) != 0 || got.Ref != r { t.Fatal("empty capture",got,err) }
	}
	descriptors,names := maps.Collect(view.State().Descriptors()),maps.Collect(view.State().Names())
	capture(context.Background(),view,d,ref,roles)
	if !maps.Equal(descriptors,maps.Collect(view.State().Descriptors())) || !maps.Equal(names,maps.Collect(view.State().Names())) { t.Fatal("capture mutated catalog") }
	foreignEngine := engine.New()
	foreign := foreignEngine.CatalogRead()
	input := pgcatalog.Input{Context:context.Background(),Catalog:view,Source:d,Ref:ref,Owner:view.Owner(),Roles:roles,Runtime:core.EngineCatalogRuntime{}}
	for _,tc := range []struct {
		name string
		mutate func(*pgcatalog.Input)
	}{
		{"nil owner",func(in *pgcatalog.Input) { in.Owner = nil }},
		{"foreign owner",func(in *pgcatalog.Input) { in.Owner = foreign.Owner() }},
		{"foreign domain",func(in *pgcatalog.Input) { in.Ref = foreign.Owner().Domain.Ref(ref.Epoch,ref.Address) }},
		{"stale epoch",func(in *pgcatalog.Input) { in.Ref.Epoch-- }},
		{"wrong class",func(in *pgcatalog.Input) { in.Ref.Address.ClassID = ids.TypeClassID }},
		{"wrong object",func(in *pgcatalog.Input) { in.Ref.Address.ObjectID++ }},
		{"subobject",func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 }},
		{"missing roles",func(in *pgcatalog.Input) { in.Roles = authorization.Read{} }},
		{"foreign roles",func(in *pgcatalog.Input) { in.Roles = foreignEngine.Roles() }},
	} {
		t.Run(tc.name,func(t *testing.T) {
			bad := input
			tc.mutate(&bad)
			got,err := core.CaptureCatalogSourceRows(bad,produce)
			if got != nil || err == nil || err.Error() != "stuntgres: invalid statistics definition source" { t.Fatal("invalid authority",got,err) }
			var state interface{ SQLState() string }; if errors.As(err,&state) { t.Fatal("invariant SQLSTATE",err) }
		})
	}
	bad := []*catalog.Relation{nil,{Columns:d.Columns[:8]},{Columns:append(append([]catalog.Column(nil),d.Columns...),d.Columns[0])}}
	for i := range d.Columns {
		next := *d; next.Columns = append([]catalog.Column(nil),d.Columns...); next.Columns[i].Usage.Type,_ = foreign.LookupType(d.Columns[i].Usage.Type.ID()); bad = append(bad,&next)
	}
	wrongName := *d; wrongName.ObjectName = "impostor"; bad = append(bad,&wrongName)
	wrongAttribute := *d; wrongAttribute.Attributes = append([]int32(nil),d.Attributes...); wrongAttribute.Attributes[0]++; bad = append(bad,&wrongAttribute)
	for _,s := range bad {
		in := input; in.Source = s
		got,err := core.CaptureCatalogSourceRows(in,produce)
		if got != nil || err == nil || err.Error() != "stuntgres: invalid statistics definition source" { t.Fatal("invalid source",got,err) }
		var state interface{ SQLState() string }; if errors.As(err,&state) { t.Fatal("invariant SQLSTATE",err) }
	}
	ctx,cancel := context.WithCancel(context.Background()); cancel()
	canceled := input; canceled.Context = ctx
	got,err := core.CaptureCatalogSourceRows(canceled,produce)
	if got != nil || !errors.Is(err,context.Canceled) { t.Fatal("canceled capture",got,err) }
	var state interface{ SQLState() string }; if errors.As(err,&state) { t.Fatal("cancellation SQLSTATE",err) }
	if err := engine.Reset(context.Background(),e); err != nil { t.Fatal(err) }
	fresh,freshRef := statisticExtUnitSource(e); freshView := e.CatalogRead()
	if fresh == d || fresh.RowType != d.RowType { t.Fatal("generation identity") }
	for _,id := range []sqltype.ID{26,19,22,21,1002,194,10031,10030} { old,_ := view.LookupType(id); now,_ := freshView.LookupType(id); if old == now { t.Fatal("reused carrier",id) } }
	capture(context.Background(),freshView,fresh,freshRef,e.Roles()); capture(context.Background(),view,d,ref,roles)
	stale := input; stale.Catalog = freshView; stale.Ref = freshRef; stale.Roles = e.Roles()
	if got,err := core.CaptureCatalogSourceRows(stale,produce); got != nil || err == nil { t.Fatal("old source accepted",got,err) }
}
