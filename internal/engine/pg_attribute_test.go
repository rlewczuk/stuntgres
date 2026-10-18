package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgAttributeUnitSource(t *testing.T, e *Engine, catalog CatalogRead) (*relationDescriptor, ids.ObjectRef) {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1249}
	d, ok := catalog.Relation(address)
	if !ok {
		t.Fatal("missing pg_attribute")
	}
	return d, e.catalog.Domain.Ref(catalog.Epoch(), address)
}

func pgAttributeUnitCapture(t *testing.T, e *Engine, catalog CatalogRead) *querySource {
	t.Helper()
	d, ref := pgAttributeUnitSource(t, e, catalog)
	e.mu.Lock()
	rows, err := e.capturePGAttribute(context.Background(), catalog, e.roleRead(catalog, e.roles), ref, d)
	e.mu.Unlock()
	if err != nil || rows == nil || rows.ref != ref {
		t.Fatal(rows, err)
	}
	return rows
}

func pgAttributeUnitRows(t *testing.T, rows *querySource, relation uint32) map[int64][]sqltype.Value {
	t.Helper()
	out := make(map[int64][]sqltype.Value)
	for _, row := range rows.anonymous {
		id, err := row[0].OID()
		if err != nil {
			t.Fatal(err)
		}
		if id != relation {
			continue
		}
		number, err := row[4].Integer()
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := out[number]; exists {
			t.Fatal("duplicate attribute", relation, number)
		}
		out[number] = row
	}
	return out
}

func TestPGAttributeShape(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	d, _ := pgAttributeUnitSource(t, e, view)
	names := []string{"attrelid", "attname", "atttypid", "attlen", "attnum", "atttypmod", "attndims", "attbyval", "attalign", "attstorage", "attcompression", "attnotnull", "atthasdef", "atthasmissing", "attidentity", "attgenerated", "attisdropped", "attislocal", "attinhcount", "attcollation", "attstattarget", "attacl", "attoptions", "attfdwoptions", "attmissingval"}
	typeIDs := []sqltype.ID{26, 19, 26, 21, 21, 23, 21, 16, 18, 18, 18, 16, 16, 16, 18, 18, 16, 16, 21, 26, 21, 1034, 1009, 1009, 2277}
	if d.RowType != 75 || d.AttributeHighWater != 25 || len(d.Columns) != 25 || !d.HasIndexes || d.VirtualView {
		t.Fatal("released identity", d)
	}
	for i, c := range d.Columns {
		if c.Name != names[i] || c.Usage.Type.ID() != typeIDs[i] || c.Nullable != (i >= 20) || !c.NullabilityKnown || c.OriginTable != 1249 || c.OriginAttribute != int16(i+1) {
			t.Fatal("column", i, c)
		}
	}
	if err := (relationProvider{}).Validate(view, d); err != nil {
		t.Fatal(err)
	}
	array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 270})
	if array.OwningRelation != d.Address() || array.SQLType.Element().ID() != 75 {
		t.Fatal("array ownership", array)
	}
	bad := *d
	bad.Columns = append([]ColumnDescriptor(nil), d.Columns...)
	bad.Columns[23].Usage = d.Columns[24].Usage
	if err := (relationProvider{}).Validate(view, &bad); err == nil {
		t.Fatal("pseudo carrier escaped attribute25")
	}
}

func TestPGAttributeCapture(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	d, ref := pgAttributeUnitSource(t, e, view)
	before, descriptors := e.catalog.state, maps.Collect(e.catalog.state.Descriptors())
	rows := pgAttributeUnitCapture(t, e, view)
	for _, row := range rows.anonymous {
		if len(row) != 25 {
			t.Fatal("width", len(row))
		}
		for i, value := range row {
			if !value.ValidResult(d.Columns[i].Usage) || value.IsNull() != (i >= 20) {
				t.Fatal("carrier", i, value)
			}
		}
	}
	self := pgAttributeUnitRows(t, rows, 1249)
	if len(self) != 31 || self[0] != nil {
		t.Fatal("self user/system identities", self)
	}
	for number := int64(-6); number < 0; number++ {
		if len(self[number]) != 25 {
			t.Fatal("system row", number)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := e.capturePGAttribute(ctx, view, e.Roles(), ref, d); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled publication", got, err)
	}
	if got, err := e.capturePGAttribute(context.Background(), view, New().Roles(), ref, d); got != nil || err == nil {
		t.Fatal("foreign roles", got, err)
	}
	wrong := ref
	wrong.Epoch++
	if got, err := e.capturePGAttribute(context.Background(), view, e.Roles(), wrong, d); got != nil || err == nil {
		t.Fatal("stale source", got, err)
	}
	privateColumn := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200000}, Columns: []ColumnDescriptor{{Name: "a", Usage: testUsage(e, sqltype.Int4), Nullable: true}}, Attributes: []int32{1}, ColumnACL: map[int32]authorization.ACLValue{1: {Explicit: true}}}
	explicit, err := e.pgAttributeRow(e.Roles(), d, privateColumn, relationAttributeMetadata(privateColumn, 0))
	if err != nil || explicit[21].IsNull() || explicit[21].ArrayLen() != 0 {
		t.Fatal("private explicit empty column ACL lost", explicit, err)
	}
	modified := *d
	modified.Columns = append([]ColumnDescriptor(nil), d.Columns...)
	modified.Columns[24].Usage.Typmod = 0
	privateState := catalog.CloneState(view.State())
	privateState.SetDescriptor(d.Address(), &modified)
	if got, err := e.capturePGAttribute(context.Background(), e.catalog.read(privateState), e.Roles(), ref, &modified); got != nil || err == nil {
		t.Fatal("modified pseudo usage admitted", got, err)
	}
	if e.catalog.state != before || !maps.Equal(descriptors, maps.Collect(e.catalog.state.Descriptors())) {
		t.Fatal("capture mutated catalog")
	}
	for _, n := range []int{32768, -32769} {
		if _, err := e.pgAttributeRow(e.Roles(), d, d, attributeMetadata{Number: int32(n), Facts: sqltype.PostgreSQLAttributeFacts{Alignment: "i", Storage: "p"}}); err == nil {
			t.Fatal("unchecked int2 narrowing", n)
		}
	}
}

func TestPGAttributePublication(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	u := testUsage(e, sqltype.Int4)
	table := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200000}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "attribute_unit", ObjectRevision: 1, Owner: e.authorizationDomain.Principal(10), Columns: []ColumnDescriptor{{Name: "a", Usage: u, Nullable: true, NullabilityKnown: true}}, Attributes: []int32{1}, AttributeHighWater: 1, Index: map[string]int{"a": 0}}
	if err := stage.Create(table); err != nil {
		t.Fatal(err)
	}
	private := e.stageRead(stage)
	own := pgAttributeUnitRows(t, pgAttributeUnitCapture(t, e, private), 200000)
	if len(own) != 7 || len(pgAttributeUnitRows(t, pgAttributeUnitCapture(t, e, base), 200000)) != 0 {
		t.Fatal("private/committed authority")
	}
	publication, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalog(publication, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	current := e.CatalogRead()
	if got := pgAttributeUnitRows(t, pgAttributeUnitCapture(t, e, current), 200000); !reflect.DeepEqual(got, own) {
		t.Fatal("published metadata", got, own)
	}
	nextStage := e.BeginCatalogStage(current)
	defer e.DiscardCatalogStage(nextStage)
	next := *current.MustRelation(table.Address())
	next.Columns = append([]ColumnDescriptor(nil), next.Columns...)
	next.Columns[0].Nullable = false
	if err := nextStage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	changed := pgAttributeUnitRows(t, pgAttributeUnitCapture(t, e, e.stageRead(nextStage)), 200000)
	if nn, _ := changed[1][11].Boolean(); !nn {
		t.Fatal("private nullability not refreshed")
	}
	if nn, _ := own[1][11].Boolean(); nn {
		t.Fatal("retained eager row changed")
	}
}

func TestPGAttributeDeclaredRank(t *testing.T) {
	e, scope, auth, old := alterAddUnitScope(t)
	action := alterAddUnitAction(t, "ALTER TABLE t ADD b integer[2][3]")
	next, _, err := e.addColumnCandidate(scope, scope.op, auth, old, action, nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.Columns[1].DeclaredDimensions != 2 || next.Columns[1].Usage.Type.ID() != 1007 || old.Columns[0].DeclaredDimensions != 0 {
		t.Fatal("declaration rank", next.Columns, old.Columns)
	}
	for _, tc := range []struct {
		rank int16
		want string
	}{{0, `"pg_catalog"."_int4"`}, {1, "INTEGER[]"}, {2, "INTEGER[][]"}} {
		column := next.Columns[1]
		column.DeclaredDimensions = tc.rank
		text, err := catalogColumnDeclaration(auth.catalog, column)
		if err != nil || text != tc.want {
			t.Fatal("rank rendering", tc, text, err)
		}
	}
	source := &relationDescriptor{Columns: []ColumnDescriptor{{Name: "a", Usage: next.Columns[1].Usage, DeclaredDimensions: 2}}, Attributes: []int32{1}, Index: map[string]int{"a": 0}}
	typed := &relationDescriptor{Columns: []ColumnDescriptor{{Name: "a", Usage: next.Columns[1].Usage}}, Attributes: []int32{1}}
	actions, _ := typedShapeActions(source, source, typed, nil)
	if len(actions) != 0 {
		t.Fatal("unrequested copied rank propagated", actions)
	}
	actions, _ = typedShapeActions(source, source, typed, map[int32]bool{1: true})
	if len(actions) != 1 || actions[0].declaredDimensions != 2 {
		t.Fatal("explicit typed rank lost", actions)
	}
}

func TestPGAttributeDroppedHistory(t *testing.T) {
	e, scope, auth, old := alterAddUnitScope(t)
	old.Columns[0].Usage = testUsage(e, sqltype.ID(1007))
	old.Columns[0].DeclaredDimensions = 2
	initial := e.BeginCatalogStage(auth.catalog)
	defer e.DiscardCatalogStage(initial)
	if err := initial.Create(old); err != nil {
		t.Fatal(err)
	}
	publication, err := e.ValidateCatalogStage(context.Background(), initial)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalog(publication, PublicationToken{Epoch: auth.catalog.Epoch()}); err != nil {
		t.Fatal(err)
	}
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	ref := e.catalog.Domain.Ref(base.Epoch(), old.Address())
	action := parser.AlterTableAction{Action: parser.DropColumn, Column: parser.Identifier{Text: "a"}, Mode: parser.DropRestrict}
	next, err := dropColumnCandidate(context.Background(), stage, old, ref, action, scope, newCatalogChanges(), nil)
	if err != nil {
		t.Fatal(err)
	}
	history := next.DroppedAttributes[1]
	if history.Name != "........pg.dropped.1........" || history.TypeID != 0 || history.Number != 1 || history.Dimensions != 2 || history.Facts.Length != -1 || history.Facts.ByValue || history.Facts.Alignment != "i" || history.Facts.Storage != "x" || !history.Dropped || history.NotNull || history.HasDefault || len(next.Columns) != 0 || next.AttributeHighWater != 3 || len(old.DroppedAttributes) != 0 || len(old.Columns) != 1 {
		t.Fatal("owned tombstone", history, next, old)
	}
	rows := pgAttributeUnitRows(t, pgAttributeUnitCapture(t, e, e.stageRead(stage)), 20000)
	if len(rows) != 7 {
		t.Fatal("tombstone plus systems", rows)
	}
	if id, _ := rows[1][2].OID(); id != 0 {
		t.Fatal("dropped type dependency retained")
	}
	identityColumn := *old
	identityColumn.Identities = map[int32]columnIdentity{1: {Mode: parser.IdentityAlways}}
	metadata := relationAttributeMetadata(&identityColumn, 0)
	if metadata.Identity != "a" {
		t.Fatal("identity before dependency removal", metadata)
	}
}

func TestPGAttributeCarrier(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	d, _ := pgAttributeUnitSource(t, e, view)
	u := d.Columns[24].Usage
	value := sqltype.CatalogResultNull(u)
	if err := validateProducedValue(value, u); err != nil {
		t.Fatal("produced canonical NULL rejected", err)
	}
	result, err := anonymousResult([]ColumnDescriptor{d.Columns[24]}, [][]sqltype.Value{{value}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Rows.Close()
	dst := make([]sqltype.Value, 1)
	if present, err := result.Rows.Next(context.Background(), dst); !present || err != nil || !dst[0].IsNull() || dst[0].Type() != u.Type {
		t.Fatal("catalog result carrier", dst, present, err)
	}
	column := columnExpression{Info: expr.Info{Usage: u}, slot: 0, scope: 1, frame: 1}
	got, err := column.Eval(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1, Row: []sqltype.Value{value}})
	if err != nil || !got.ValidResult(u) {
		t.Fatal("projection carrier", got, err)
	}
	foreign := sqltype.CatalogResultNull(testUsage(New(), sqltype.ID(2277)))
	if err := validateProducedValue(foreign, u); err == nil {
		t.Fatal("foreign produced NULL admitted")
	}
	if _, err := anonymousResult([]ColumnDescriptor{d.Columns[24]}, [][]sqltype.Value{{foreign}}, 1); err == nil {
		t.Fatal("foreign carrier descriptor admitted")
	}
	// Ordinary storage validation must not admit this pseudo type elsewhere.
	ordinary := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200000}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "bad", Owner: e.authorizationDomain.Principal(10), Columns: []ColumnDescriptor{{Name: "a", Usage: u}}, Attributes: []int32{1}, AttributeHighWater: 1, Index: map[string]int{"a": 0}}
	if err := (relationProvider{}).Validate(view, ordinary); err == nil {
		t.Fatal("ANYARRAY stored column admitted")
	}
	_, err = e.pgClassACL(New().Roles(), d.Columns[21].Usage.Type, authorization.ACLValue{Explicit: true})
	if err == nil {
		t.Fatal("foreign empty ACL authority admitted")
	}
}
