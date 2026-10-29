package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func publicationInquiryUnitShape(t *testing.T, view catalog.Read) *sqltype.Descriptor {
	t.Helper()
	members := make([]sqltype.CompositeMember, 4)
	for i, field := range [...]struct {
		name string
		id   sqltype.ID
	}{{"pubid", sqltype.Oid}, {"relid", sqltype.Oid}, {"attrs", 22}, {"qual", sqltype.PgNodeTree}} {
		typ, ok := view.LookupType(field.id)
		if !ok {
			t.Fatal("missing declared type", field.id)
		}
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: field.name, Usage: typ.Usage()}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		t.Fatal(err)
	}
	return shape
}

func TestPublicationInquiryCaptureAuthority(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	shape := publicationInquiryUnitShape(t, view)
	boolean, _ := view.LookupType(sqltype.Bool)
	array, _ := view.LookupType(1009)
	scope := tablespaceInfoUnitScope(t, e)
	host := core.HostOf(scope)
	value, err := host.RelationIsPublishable(4294967295, boolean)
	if err != nil || !value.IsNull() {
		t.Fatal("missing relation", value, err)
	}
	value, err = host.RelationIsPublishable(1259, boolean)
	if err != nil {
		t.Fatal(err)
	}
	publishable, err := value.Boolean()
	if err != nil || publishable {
		t.Fatal("system relation", value, err)
	}
	empty, err := array.Input(context.Background(), "{}", -1)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := host.PublicationTables(empty, shape)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	if _, ok, err := cursor.Next(context.Background()); err != nil || ok {
		t.Fatal("empty source", ok, err)
	}
	for _, bad := range []catalog.Read{{}, engine.New().CatalogRead()} {
		scope.Inquiry.CommentCatalog = bad
		if _, err := host.RelationIsPublishable(1259, boolean); err == nil {
			t.Fatal("accepted foreign scalar capture")
		}
		if _, err := host.PublicationTables(empty, shape); err == nil {
			t.Fatal("accepted foreign SRF capture")
		}
	}
}

func TestPublicationInquiryNameDiagnostics(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	shape := publicationInquiryUnitShape(t, view)
	array, _ := view.LookupType(1009)
	host := core.HostOf(tablespaceInfoUnitScope(t, e))
	for _, tc := range []struct{ input, code, message string }{
		{"{missing_publication}", "42704", "publication \"missing_publication\" does not exist"},
		{"{missing_publication,NULL}", "22004", "null array element not allowed in this context"},
	} {
		names, err := array.Input(context.Background(), tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		_, err = host.PublicationTables(names, shape)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message || diagnostic.Severity != "ERROR" || diagnostic.SeverityUnlocalized != "ERROR" {
			t.Fatal(tc.input, err)
		}
	}
}

func TestPublicationInquiryUsesCapturedRelationFacts(t *testing.T) {
	e := engine.New()
	base := e.CatalogRead()
	state := catalog.CloneState(base.State())
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 49001}
	r := &catalog.Relation{ObjectAddress: address, ObjectName: "publication_inquiry_private", ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}, RelationKind: catalog.TableRelation}
	state.SetDescriptor(address, r)
	view := base.WithState(state)
	boolean, _ := view.LookupType(sqltype.Bool)
	scope := tablespaceInfoUnitScope(t, e)
	scope.Inquiry.CommentCatalog = view
	host := core.HostOf(scope)
	value, err := host.RelationIsPublishable(49001, boolean)
	if err != nil {
		t.Fatal(err)
	}
	publishable, err := value.Boolean()
	if err != nil || !publishable {
		t.Fatal("captured ordinary relation", value, err)
	}
	scope.Inquiry.CommentCatalog = base
	missing, err := host.RelationIsPublishable(49001, boolean)
	if err != nil || !missing.IsNull() {
		t.Fatal("fresh capture", missing, err)
	}
	publishable, err = value.Boolean()
	if err != nil || !publishable {
		t.Fatal("retained scalar changed", value, err)
	}
}

func TestPublicationInquiryRoutineAssociation(t *testing.T) {
	e := engine.New()
	for _, tc := range []struct {
		name     string
		oid      uint32
		formal   sqltype.ID
		set      bool
		variadic ids.OID
	}{
		{"pg_relation_is_publishable", 6121, sqltype.Regclass, false, 0},
		{"pg_get_publication_tables", 6119, 1009, true, 25},
	} {
		node := catalogRelationsUnitBind(t, e, tc.name, parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}})
		ref := expr.InfoOf(node).RoutineReference
		if ref == nil || ref.RoutineOID() != tc.oid {
			t.Fatal("missing executable routine identity", tc.name, ref)
		}
		child := node.(expr.Composite).Children()[0]
		if child.ResultUsage().Type.ID() != tc.formal {
			t.Fatal("declared operand", tc.name, child.ResultUsage())
		}
		found := false
		for _, declaration := range routine.BuiltinRoutineDeclarations {
			if uint32(declaration.Identity.ID) != tc.oid {
				continue
			}
			found = true
			if !declaration.Strict || declaration.Volatility != expr.Stable || declaration.Parallel != 's' || declaration.ReturnsSet != tc.set || declaration.Variadic != tc.variadic || declaration.Providers != functions.CatalogRoutineProvider {
				t.Fatal("released executable association", tc.name, declaration)
			}
		}
		if !found {
			t.Fatal("missing successful provider association", tc.name)
		}
		if tc.set {
			shape := node.ResultUsage().Type
			for i, field := range [...]struct {
				name string
				id   sqltype.ID
			}{{"pubid", sqltype.Oid}, {"relid", sqltype.Oid}, {"attrs", 22}, {"qual", sqltype.PgNodeTree}} {
				member := shape.CompositeMember(i)
				if member.Name != field.name || member.Usage.Type.ID() != field.id {
					t.Fatal("declared SRF field", member)
				}
			}
		}
	}
}

func TestPublicationEmptyProjectionCapture(t *testing.T) {
	for _, generated := range []bool{false, true} {
		name := "zero_attributes"
		if generated {
			name = "stored_only"
		}
		t.Run(name, func(t *testing.T) {
			e, relation, p, _ := publicationOwnerFixture(t)
			base := e.CatalogRead()
			state := catalog.CloneState(base.State())
			empty := *relation
			empty.ObjectRevision++
			if generated {
				empty.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{
					1: {Kind: catalog.GenerationStored},
					2: {Kind: catalog.GenerationStored},
				}
			} else {
				empty.Columns, empty.Attributes, empty.Index = nil, nil, nil
			}
			state.SetDescriptor(empty.Address(), &empty)
			e.Mu.Lock()
			e.Catalog.State = state
			e.Mu.Unlock()
			view := e.CatalogRead()
			shape := publicationInquiryUnitShape(t, view)
			array, _ := view.LookupType(1009)
			names, err := array.Input(context.Background(), "{"+p.Name()+"}", -1)
			if err != nil {
				t.Fatal(err)
			}
			host := core.HostOf(tablespaceInfoUnitScope(t, e))
			old, err := host.PublicationTables(names, shape)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			produce, ok := pgcatalog.ProducerFor(pgcatalog.PublicationTablesOID)
			if !ok {
				t.Fatal("missing publication-table producer")
			}
			var retained [][]sqltype.Value
			var retainedType *sqltype.Descriptor
			publicationInput(t, e, func(in pgcatalog.Input, auth core.AuthorizationView) {
				in.Source = in.Catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.PublicationTablesOID})
				in.Ref = auth.Ref(in.Source.Address())
				retainedType = in.Source.Columns[3].Usage.Type
				retained, err = produce(in)
				if err != nil || len(retained) != 1 || !retained[0][3].IsNull() || retained[0][3].Type() != retainedType {
					t.Fatal("empty projection did not retain typed NULL", retained, err)
				}
				next := catalog.CloneState(in.Catalog.State())
				if generated {
					stored := *p
					stored.ObjectRevision++
					stored.GeneratedColumns = 's'
					next.SetDescriptor(stored.Address(), &stored)
				} else {
					restored := *relation
					restored.ObjectRevision = empty.ObjectRevision + 1
					next.SetDescriptor(restored.Address(), &restored)
				}
				e.Catalog.State = next
			})
			publicationInput(t, e, func(in pgcatalog.Input, auth core.AuthorizationView) {
				in.Source = in.Catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.PublicationTablesOID})
				in.Ref = auth.Ref(in.Source.Address())
				rows, err := produce(in)
				if err != nil || len(rows) != 1 || rows[0][3].IsNull() || rows[0][3].ArrayLen() != 2 {
					t.Fatal("fresh projection did not expand", rows, err)
				}
			})
			value, ok, err := old.Next(context.Background())
			if err != nil || !ok || !value.CompositeField(2).IsNull() || value.CompositeField(2).Type() != shape.CompositeMember(2).Usage.Type {
				t.Fatal("captured inquiry changed after replacement", value, ok, err)
			}
			if _, ok, err := old.Next(context.Background()); err != nil || ok {
				t.Fatal("unexpected captured inquiry row", ok, err)
			}
			freshHost := core.HostOf(tablespaceInfoUnitScope(t, e))
			fresh, err := freshHost.PublicationTables(names, shape)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			value, ok, err = fresh.Next(context.Background())
			if err != nil || !ok || value.CompositeField(2).IsNull() || value.CompositeField(2).ArrayLen() != 2 {
				t.Fatal("fresh inquiry did not expand", value, ok, err)
			}
			if !retained[0][3].IsNull() || retained[0][3].Type() != retainedType || empty.Attributes != nil && &empty.Attributes[0] != &relation.Attributes[0] {
				t.Fatal("retained typed projection or attribute backing changed")
			}
		})
	}
}
