package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

var attrUnitNames = []string{"udt_catalog", "udt_schema", "udt_name", "attribute_name", "ordinal_position", "attribute_default", "is_nullable", "data_type", "character_maximum_length", "character_octet_length", "character_set_catalog", "character_set_schema", "character_set_name", "collation_catalog", "collation_schema", "collation_name", "numeric_precision", "numeric_precision_radix", "numeric_scale", "datetime_precision", "interval_type", "interval_precision", "attribute_udt_catalog", "attribute_udt_schema", "attribute_udt_name", "scope_catalog", "scope_schema", "scope_name", "maximum_cardinality", "dtd_identifier", "is_derived_reference_attribute"}
var attrUnitDomains = []sqltype.ID{13248, 13248, 13248, 13248, 13243, 13246, 13256, 13246, 13243, 13243, 13248, 13248, 13248, 13248, 13248, 13248, 13243, 13243, 13243, 13243, 13246, 13243, 13248, 13248, 13248, 13248, 13248, 13248, 13243, 13248, 13256}

func attrUnitInput(t *testing.T, f defaultUnitFixture, view catalog.Read, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	auth := dtpUnitAuthority(t, f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoAttributesOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: auth, Database: "captured db"})}
}

func attrUnitState(t *testing.T, f defaultUnitFixture) *catalog.State {
	t.Helper()
	view := f.e.CatalogRead()
	state := catalog.CloneState(view.State())
	integer, _ := view.LookupType(sqltype.Int4)
	varchar, _ := view.LookupType(sqltype.VarChar)
	label, _, err := varchar.ResolveModifiers(4)
	if err != nil {
		t.Fatal(err)
	}
	label.Collation.ID = 950
	domain, err := sqltype.Domain(49200, "required", "public.required", integer.Usage(), false, true)
	if err != nil {
		t.Fatal(err)
	}
	literalDefault := func(n int64) catalog.ColumnDefault {
		value, err := sqltype.Integer(integer, n)
		if err != nil {
			t.Fatal(err)
		}
		definition := catalog.ColumnDefault{Expression: plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: value}}
		if _, err := (stored.ScalarRenderer{Dependencies: &definition.Dependencies, CommentSnapshot: &definition.CatalogSnapshot, CatalogAliasReferences: &definition.CatalogAliasReferences}).SQL(t.Context(), view, definition.Expression); err != nil {
			t.Fatal(err)
		}
		return definition
	}
	nine := literalDefault(9)
	d := &catalog.Type{SQLType: domain, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}, ObjectRevision: 1, Owner: f.owner, ACL: authorization.ACLValue{Explicit: true}, DomainNotNull: &catalog.DomainNotNullConstraint{OID: 49201, Name: "required_not_null"}, DomainDefault: &nine, DomainDefaultOwn: true, DomainDefaultText: "9"}
	state.SetDescriptor(d.Address(), d)
	state.BindName(catalog.DescriptorNameKey(d), d.Address())
	for i, name := range []string{"pair", "empty", "table_negative"} {
		var members []sqltype.CompositeMember
		if i != 1 {
			members = []sqltype.CompositeMember{{Attribute: 1, Name: "id", Usage: integer.Usage()}, {Attribute: 3, Name: "label", Usage: label}, {Attribute: 4, Name: "required", Usage: domain.Usage()}}
		}
		typ, err := sqltype.Composite(sqltype.ID(49000+i), name, "public."+name, 0, members)
		if err != nil {
			t.Fatal(err)
		}
		relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(49100 + i)}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}, ObjectName: name, ObjectRevision: 1, RelationKind: catalog.CompositeRelation, Owner: f.owner, RowType: typ.ID()}
		if i == 2 {
			relation.RelationKind = catalog.TableRelation
		}
		for _, member := range members {
			relation.Columns = append(relation.Columns, catalog.Column{Name: member.Name, Usage: member.Usage, Nullable: true, NullabilityKnown: true})
			relation.Attributes = append(relation.Attributes, member.Attribute)
		}
		if len(members) > 0 {
			seven := literalDefault(7)
			seven.OID = 49300
			relation.ColumnMeta = catalog.ColumnMetadata{HighWater: 4, Defaults: map[int32]catalog.ColumnDefault{1: seven}}
			relation.Columns[0].Nullable = false
		}
		descriptor := &catalog.Type{SQLType: typ, ObjectNamespace: relation.Namespace(), ObjectRevision: 1, Owner: f.owner, OwningRelation: relation.Address(), ACL: authorization.ACLValue{Explicit: true}}
		state.SetDescriptor(relation.Address(), relation)
		state.BindName(catalog.DescriptorNameKey(relation), relation.Address())
		state.SetDescriptor(descriptor.Address(), descriptor)
		state.BindName(catalog.DescriptorNameKey(descriptor), descriptor.Address())
	}
	return state
}

func attrUnitRows(t *testing.T, in pgcatalog.Input, count int) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoAttributesOID)
	if !ok {
		t.Fatal("missing attributes producer")
	}
	capture, err := catalogrt.CaptureRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Close()
	if capture.Ref != in.Ref || capture.View != nil || len(capture.Anonymous) != count {
		t.Fatal("capture ownership/rows", capture)
	}
	rows := capture.Anonymous
	for _, row := range rows {
		if len(row) != 31 {
			t.Fatal("width", len(row))
		}
		for i, value := range row {
			if value.Type() != in.Source.Columns[i].Usage.Type || !value.ValidResult(in.Source.Columns[i].Usage) {
				t.Fatal("domain cell", i, value)
			}
			base, err := value.BaseValue()
			if err != nil || base.Type() != in.Source.Columns[i].Usage.Type.BaseType() || base.IsNull() != value.IsNull() {
				t.Fatal("base carrier", i, base, err)
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return infoUserMappingsUnitText(t, rows[i][3]) < infoUserMappingsUnitText(t, rows[j][3]) })
	return rows
}

func TestInfoAttributesShape(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.CatalogRead()
	in := attrUnitInput(t, f, view, f.owner)
	s := in.Source
	if pgcatalog.InfoAttributesOID != 16258 || s.Name() != "attributes" || s.RowType != 13269 || s.Namespace().ObjectID != 13229 || !s.VirtualView || s.RelationKind != catalog.CatalogRelation {
		t.Fatal("source", s)
	}
	if !s.Security.ACL.Explicit || len(s.Security.ACL.Entries) != 1 || !s.Security.ACL.Entries[0].Grantee.Public || s.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", s.Security.ACL)
	}
	row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13269})
	array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13268})
	if row.OwningRelation != s.Address() || row.Array != 13268 || array.OwningRelation != s.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal("reciprocal identities")
	}
	if len(s.Columns) != 31 || len(s.Attributes) != 31 || len(s.ColumnMeta.CatalogAttributes) != 31 {
		t.Fatal("width")
	}
	for i, c := range s.Columns {
		typ, _ := view.LookupType(attrUnitDomains[i])
		collation, base, mod := uint32(950), sqltype.Name, int32(-1)
		switch attrUnitDomains[i] {
		case 13243:
			collation, base = 0, sqltype.Int4
		case 13246:
			base = sqltype.VarChar
		case 13256:
			base, mod = sqltype.VarChar, 7
		}
		if c.Name != attrUnitNames[i] || c.Usage != typ.Usage() || !c.Nullable || !c.NullabilityKnown || c.OriginTable != 16258 || c.OriginAttribute != int16(i+1) || s.Attributes[i] != int32(i+1) || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collation || c.DeclaredDimensions != 0 || typ.BaseType().ID() != base || typ.DomainBase().Typmod != mod || typ.OwnDomainNotNull() {
			t.Fatal("column", i, c)
		}
		a := s.ColumnMeta.CatalogAttributes[i]
		if a.NotNull || a.Collation != collation || a.Dimensions != 0 {
			t.Fatal("attribute", i, a)
		}
	}
	if pgcatalog.BuiltinViewEvents(16258, nil) != 0 {
		t.Fatal("writable builtin")
	}
	attrUnitRows(t, in, 0)
}

func TestInfoAttributesCapture(t *testing.T) {
	t.Run("paired_statement_frontier", attrUnitPairedCapture)
	t.Run("own_domain_nullability", attrUnitOwnDomainNullability)
	t.Run("dropped_metadata", attrUnitDroppedMetadata)
	f := newDefaultUnitFixture(t)
	state := attrUnitState(t, f)
	view := f.e.Catalog.Read(state)
	in := attrUnitInput(t, f, view, f.owner)
	first, second := attrUnitRows(t, in, 3), attrUnitRows(t, in, 3)
	want := [][]any{
		{"captured db", "public", "pair", "id", int64(1), "7", "NO", "integer", nil, nil, nil, nil, nil, nil, nil, nil, int64(32), int64(2), int64(0), nil, nil, nil, "captured db", "pg_catalog", "int4", nil, nil, nil, nil, "1", "NO"},
		{"captured db", "public", "pair", "label", int64(3), nil, "YES", "character varying", int64(4), int64(16), nil, nil, nil, "captured db", "pg_catalog", "C", nil, nil, nil, nil, nil, nil, "captured db", "pg_catalog", "varchar", nil, nil, nil, nil, "3", "NO"},
		{"captured db", "public", "pair", "required", int64(4), nil, "NO", "USER-DEFINED", nil, nil, nil, nil, nil, nil, nil, nil, int64(32), int64(2), int64(0), nil, nil, nil, "captured db", "public", "required", nil, nil, nil, nil, "4", "NO"},
	}
	for r, row := range first {
		for i, value := range row {
			var got any
			if !value.IsNull() {
				base, err := value.BaseValue()
				if err != nil {
					t.Fatal(err)
				}
				if attrUnitDomains[i] == 13243 {
					got, err = base.Integer()
				} else {
					got, err = base.Text()
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(got, want[r][i]) {
				t.Fatalf("row %d column %d=%v want=%v", r, i, got, want[r][i])
			}
		}
	}
	first[0][0] = sqltype.Null(first[0][0].Type())
	if second[0][0].IsNull() {
		t.Fatal("shared mutable row backing")
	}
	attrUnitRows(t, attrUnitInput(t, f, view, f.reader), 0)
	attrUnitRows(t, attrUnitInput(t, f, view, f.member), 3)
	oldMember := attrUnitInput(t, f, view, f.member)
	roles := cloneRoleFixture(f.e.RoleStore.Published)
	setFixtureEdges(roles, nil)
	f.e.RoleStore.PublishLocked(roles)
	attrUnitRows(t, attrUnitInput(t, f, view, f.member), 0)
	attrUnitRows(t, oldMember, 3)
	attrUnitRows(t, in, 3)
}

func attrUnitPairedCapture(t *testing.T) {
	f := newDefaultUnitFixture(t)
	state := attrUnitState(t, f)
	f.e.Catalog.PublishLocked(state)
	f.op.Session.DatabaseName = "captured db"
	f.actor(f.member)
	scope, err := f.e.StartStatement(f.op)
	if err != nil {
		t.Fatal(err)
	}
	finished := false
	defer func() {
		if !finished {
			if err := scope.Finish(nil); err != nil {
				t.Error(err)
			}
		}
	}()
	f.e.Mu.Lock()
	err = f.e.CaptureCommentSnapshotLocked(scope)
	f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	view := scope.Inquiry.CommentCatalog
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoAttributesOID})
	next := catalog.CloneState(state)
	for _, address := range []ids.ObjectAddress{{ClassID: ids.RelationClassID, ObjectID: 49100}, {ClassID: ids.TypeClassID, ObjectID: 49000}} {
		descriptor := view.Get(address)
		next.UnbindName(catalog.DescriptorNameKey(descriptor))
		next.RemoveDescriptor(address)
	}
	// Replace even the live source pointer: opening must recapture the admitted one.
	newSource := *source
	newSource.ObjectRevision = source.Revision() + 1
	next.SetDescriptor(source.Address(), &newSource)
	f.e.Catalog.PublishLocked(next)
	roles := cloneRoleFixture(f.e.RoleStore.Published)
	setFixtureEdges(roles, nil)
	f.e.RoleStore.PublishLocked(roles)
	f.actor(f.reader)
	f.op.Session.DatabaseName = "new db"
	f.e.Mu.Lock()
	captured, err := core.EngineOpenRelationSourceLocked(f.e, scope, view.Owner().Domain.Ref(view.Epoch(), source.Address()), source.Owner, plan.RowMark{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if len(captured.Anonymous) != 3 {
		t.Fatal("resampled live inventory", captured.Anonymous)
	}
	for _, row := range captured.Anonymous {
		if infoUserMappingsUnitText(t, row[0]) != "captured db" || infoUserMappingsUnitText(t, row[2]) != "pair" {
			t.Fatal("resampled admitted database", row)
		}
		for i, value := range row {
			if value.Type() != source.Columns[i].Usage.Type || !value.ValidResult(source.Columns[i].Usage) {
				t.Fatal("paired domain authority", i, value)
			}
		}
	}
	attrUnitRows(t, attrUnitInput(t, f, f.e.Catalog.Read(next), f.owner), 0)
	if newSource.Revision() <= source.Revision() {
		t.Fatal("source fixture did not advance effective generation")
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	finished = true
	fresh, err := f.e.StartStatement(f.op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := fresh.Finish(nil); err != nil {
			t.Error(err)
		}
	}()
	f.e.Mu.Lock()
	err = f.e.CaptureCommentSnapshotLocked(fresh)
	f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	f.e.Mu.Lock()
	current, err := core.EngineOpenRelationSourceLocked(f.e, fresh, view.Owner().Domain.Ref(view.Epoch(), newSource.Address()), newSource.Owner, plan.RowMark{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	if len(current.Anonymous) != 0 {
		t.Fatal("fresh statement retained retired attributes", current.Anonymous)
	}
}

func TestInfoAttributesAuthority(t *testing.T) {
	f := newDefaultUnitFixture(t)
	state := attrUnitState(t, f)
	view := f.e.Catalog.Read(state)
	in := attrUnitInput(t, f, view, f.owner)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoAttributesOID)
	foreign := engine.New()
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &foreign.Catalog.Owner },
		func(in *pgcatalog.Input) { in.Roles = foreign.Roles() },
		func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) {
			r := core.CatalogRuntimeFieldsOf(in.Runtime)
			r.Auth.RoleRecord = nil
			in.Runtime = core.NewCatalogRuntime(r)
		},
	} {
		bad := in
		mutate(&bad)
		rows, err := produce(bad)
		schemataUnitFailure(t, rows, err)
	}
	for column := range 31 {
		for _, kind := range []string{"primitive", "foreign_domain", "typmod", "collation", "name", "origin", "nullable", "ordinal", "attribute_notnull", "attribute_dimensions", "attribute_collation"} {
			bad := catalog.CloneState(state)
			source := *in.Source
			source.Columns = append([]catalog.Column(nil), source.Columns...)
			source.Attributes = append([]int32(nil), source.Attributes...)
			source.ColumnMeta.CatalogAttributes = append([]catalog.CatalogAttribute(nil), source.ColumnMeta.CatalogAttributes...)
			switch kind {
			case "primitive":
				source.Columns[column].Usage.Type = source.Columns[column].Usage.Type.BaseType()
			case "foreign_domain":
				source.Columns[column].Usage.Type, _ = foreign.CatalogRead().LookupType(attrUnitDomains[column])
			case "typmod":
				source.Columns[column].Usage.Typmod = 7
			case "collation":
				source.Columns[column].Usage.Collation.ID++
			case "name":
				source.Columns[column].Name = "invented"
			case "origin":
				source.Columns[column].OriginAttribute++
			case "nullable":
				source.Columns[column].Nullable = false
			case "ordinal":
				source.Attributes[column]++
			case "attribute_notnull":
				source.ColumnMeta.CatalogAttributes[column].NotNull = true
			case "attribute_dimensions":
				source.ColumnMeta.CatalogAttributes[column].Dimensions++
			case "attribute_collation":
				source.ColumnMeta.CatalogAttributes[column].Collation++
			}
			bad.SetDescriptor(source.Address(), &source)
			rows, err := produce(attrUnitInput(t, f, f.e.Catalog.Read(bad), f.owner))
			schemataUnitFailure(t, rows, err)
		}
	}
	for _, kind := range []string{"missing_type", "reciprocal", "namespace", "namespace_binding", "owner", "member", "layout", "late_collation"} {
		bad := catalog.CloneState(state)
		rel := *view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 49100})
		typ := *view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 49000})
		switch kind {
		case "missing_type":
			bad.RemoveDescriptor(typ.Address())
		case "reciprocal":
			typ.OwningRelation = ids.ObjectAddress{}
			bad.SetDescriptor(typ.Address(), &typ)
		case "namespace":
			rel.ObjectNamespace.ObjectID = 99999
		case "namespace_binding":
			ns := *view.MustNamespace(rel.Namespace())
			ns.ObjectName = "unbound"
			bad.SetDescriptor(ns.Address(), &ns)
		case "owner":
			rel.Owner = f.reader
		case "member":
			rel.Columns = append([]catalog.Column(nil), rel.Columns...)
			rel.Columns[2].Usage.Type, _ = foreign.CatalogRead().LookupType(sqltype.Int4)
		case "layout":
			rel.Attributes = rel.Attributes[:1]
		case "late_collation":
			// The first legitimate member is constructed before the missing C
			// authority on the second member can fail. No prefix may escape.
			bad.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: 950})
		}
		bad.SetDescriptor(rel.Address(), &rel)
		rows, err := produce(attrUnitInput(t, f, f.e.Catalog.Read(bad), f.owner))
		schemataUnitFailure(t, rows, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	bad := in
	bad.Context = ctx
	if rows, err := produce(bad); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("precancel", rows, err)
	}
	for _, remaining := range []int{1, 4, 100} {
		bad.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if rows, err := produce(bad); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled capture", rows, err)
		}
	}
	late, stop := context.WithCancel(t.Context())
	defer stop()
	bad = in
	bad.Context = late
	bad.Runtime = attrCancelRuntime{Runtime: in.Runtime, cancel: stop}
	if rows, err := produce(bad); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("late member cancellation", rows, err)
	}
}

type attrCancelRuntime struct {
	pgcatalog.Runtime
	cancel context.CancelFunc
}

func (r attrCancelRuntime) ColumnExpression(ctx context.Context, view catalog.Read, relation *catalog.Relation, expression expr.Expr) (string, error) {
	text, err := r.Runtime.ColumnExpression(ctx, view, relation, expression)
	r.cancel()
	return text, err
}
