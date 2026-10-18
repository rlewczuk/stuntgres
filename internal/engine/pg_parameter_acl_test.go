package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func pgParameterACLUnitFixture(t *testing.T) (*Engine, *Operation, authorization.PrincipalRef) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	reader := roleAttributeCreate(t, e, op, "grant_reader", true, parser.RoleOptions{}).Ref
	return e, op, reader
}

func pgParameterACLUnitSource(t *testing.T, e *Engine, catalog catalog.Read) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.ParameterClassID}
	d, ok := catalog.Relation(address)
	if !ok {
		t.Fatal("missing pg_parameter_acl")
	}
	return d, e.catalog.Domain.Ref(catalog.Epoch(), address)
}

func pgParameterACLUnitCapture(t *testing.T, e *Engine, catalog catalog.Read, roles authorization.Read) *querySource {
	t.Helper()
	d, ref := pgParameterACLUnitSource(t, e, catalog)
	rows, err := e.captureCatalogSourceLocked(context.Background(), catalog, roles, ref, d)
	if err != nil || rows == nil || rows.ref != ref || rows.anonymous == nil {
		t.Fatal("capture", rows, err)
	}
	return rows
}

// Expected ACLITEM layouts are independent of the production mask converter.
func pgParameterACLUnitEntries(t *testing.T, e *Engine, value sqltype.Value, want map[uint32][3]uint32) {
	t.Helper()
	if value.IsNull() || value.Type().ID() != 1034 || value.ArrayLen() != len(want) {
		t.Fatal("ACL shape", value)
	}
	if len(want) == 0 {
		if value.ArrayRank() != 0 {
			t.Fatal("empty ACL must be zero dimensional")
		}
	} else if value.ArrayRank() != 1 || value.ArrayDimension(0) != (sqltype.ArrayDimension{Lower: 1, Length: int32(len(want))}) {
		t.Fatal("ACL dimensions")
	}
	seen := make(map[uint32]bool)
	for i := range value.ArrayLen() {
		authority, grantee, grantor, privileges, options, err := value.ArrayElement(i).ACLItem()
		expected, exists := want[grantee]
		if err != nil || authority != &e.aclAuthority || !exists || seen[grantee] || expected != ([3]uint32{grantor, privileges, options}) {
			t.Fatal("ACL provenance/masks", grantee, grantor, privileges, options, err)
		}
		seen[grantee] = true
	}
}

func TestPGParameterACLShapeAndOwnership(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	d, _ := pgParameterACLUnitSource(t, e, view)
	owner := e.authorizationDomain.Principal(authorization.BootstrapPrincipalID)
	if d.Name() != "pg_parameter_acl" || d.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}) || d.Owner != owner || d.Owner.ID() != 10 || d.RelationKind != catalog.CatalogRelation || d.VirtualView || len(d.Columns) != 3 || d.AttributeHighWater != 3 || !d.HasIndexes {
		t.Fatal("relation identity", d)
	}
	for i, name := range []string{"oid", "parname", "paracl"} {
		id := []sqltype.ID{26, 25, 1034}[i]
		collation := []uint32{0, 950, 0}[i]
		typ, _ := view.LookupType(id)
		c := d.Columns[i]
		if c.Name != name || c.Usage.Type != typ || c.Usage.Typmod != -1 || uint32(c.Usage.Collation.ID) != collation || c.Nullable != (i == 2) || !c.NullabilityKnown || c.OriginTable != 6243 || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[name] != i {
			t.Fatal("column", i, c)
		}
		fact := relationAttributeMetadata(d, i)
		dimensions := int16(0)
		if i == 2 {
			dimensions = 1
		}
		if fact.Name != name || fact.TypeID != uint32(id) || fact.Number != int32(i+1) || fact.Typmod != -1 || fact.NotNull != (i < 2) || fact.Dimensions != dimensions || fact.Collation != collation {
			t.Fatal("attribute", i, fact)
		}
	}
	if err := (relationProvider{}).Validate(view, d); err != nil {
		t.Fatal(err)
	}
	if !d.ACL.Explicit || len(d.ACL.Entries) != 1 || !d.ACL.Entries[0].Grantee.Public || d.ACL.Entries[0].Grantor != owner || d.ACL.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", d.ACL)
	}
	row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10097})
	array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10096})
	ownedArray, ok := view.LookupArray(row.SQLType)
	if !ok || d.RowType != 10097 || row.Array != 10096 || ownedArray != array.SQLType || array.SQLType.Element() != row.SQLType || row.OwningRelation != d.Address() || array.OwningRelation != d.Address() || row.Owner != owner || array.Owner != owner {
		t.Fatal("row/array ownership", row, array)
	}
	if !representedSummaryTable(6243) || !representedCatalogHasIndexes(6243) || pgClassKind(view, d) != "r" || pgClassPersistence(view, d) != "p" {
		t.Fatal("discovery")
	}
	class := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259})
	facts, err := e.pgClassRow(context.Background(), view, e.Roles(), class, d)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := facts[15].Boolean()
	if err != nil || !shared {
		t.Fatal("shared", shared, err)
	}
	space, err := facts[8].OID()
	if err != nil || space != 1664 {
		t.Fatal("pg_global", space, err)
	}
	for _, oid := range []ids.OID{6244, 6245, 6246, 6247} {
		if _, exists := view.Find(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}); exists {
			t.Fatal("fabricated backing relation", oid)
		}
	}
	if rows := pgParameterACLUnitCapture(t, e, view, e.Roles()); rows.count() != 0 {
		t.Fatal("pristine rows")
	}
}

func TestPGParameterACLCapture(t *testing.T) {
	e, op, reader := pgParameterACLUnitFixture(t)
	request := parameterSpaceRequest("parameter", "lc_messages", "PACL_UNIT.Custom")
	if err := applyObjectGrantUnit(e, op, request); err != nil {
		t.Fatal(err)
	}
	public := parameterSpaceRequest("parameter", "lc_messages")
	public.GrantOption = false
	public.Privileges = []parser.GrantPrivilege{{Name: "set"}}
	public.Grantees = []parser.GrantGrantee{{Public: true}}
	if err := applyObjectGrantUnit(e, op, public); err != nil {
		t.Fatal(err)
	}
	view, roles := e.CatalogRead(), e.Roles()
	state, nextOID, revision := e.catalog.state, e.catalog.nextOID, e.catalog.nextRevision
	descriptors, names := maps.Collect(state.Descriptors()), maps.Collect(state.Names())
	rows := pgParameterACLUnitCapture(t, e, view, roles)
	if rows.count() != 2 {
		t.Fatal("only actual descriptors", rows.count())
	}
	seen := make(map[string]bool)
	for _, row := range rows.anonymous {
		name, err := row[1].Text()
		if err != nil || seen[name] || (name != "lc_messages" && name != "pacl_unit.custom") {
			t.Fatal("canonical membership", name, err)
		}
		seen[name] = true
		address := view.NameAddress(ids.NameKey{ClassID: ids.ParameterClassID, Name: name})
		id, err := row[0].OID()
		if err != nil || id == 0 || ids.OID(id) != address.ObjectID || address.ClassID == ids.RelationClassID {
			t.Fatal("actual entry identity", address, id, err)
		}
		want := map[uint32][3]uint32{10: {10, 12288, 0}, uint32(reader.ID()): {10, 12288, 12288}}
		if name == "lc_messages" {
			want[0] = [3]uint32{10, 4096, 0}
		}
		pgParameterACLUnitEntries(t, e, row[2], want)
	}
	if e.catalog.state != state || e.catalog.nextOID != nextOID || e.catalog.nextRevision != revision || !maps.Equal(descriptors, maps.Collect(state.Descriptors())) || !maps.Equal(names, maps.Collect(state.Names())) {
		t.Fatal("capture mutated owner roots/frontiers")
	}
	d, ref := pgParameterACLUnitSource(t, e, view)
	if rows, err := e.capturePGParameterACL(context.Background(), view, New().Roles(), ref, d); err == nil || rows != nil {
		t.Fatal("foreign roles accepted", err)
	}
	address := view.NameAddress(ids.NameKey{ClassID: ids.ParameterClassID, Name: "lc_messages"})
	corrupt := *view.MustParameter(address)
	corrupt.Owner = authorization.PrincipalRef{}
	broken := catalog.CloneState(view.State())
	broken.SetDescriptor(address, &corrupt)
	if rows, err := e.capturePGParameterACL(context.Background(), e.catalog.read(broken), roles, ref, d); err == nil || rows != nil {
		t.Fatal("invalid owner accepted", err)
	}
	corrupt.Owner = e.authorizationDomain.Principal(10)
	corrupt.ACL.Entries = append([]authorization.ACLEntry(nil), corrupt.ACL.Entries...)
	corrupt.ACL.Entries[0].Grantor = New().authorizationDomain.Principal(10)
	if rows, err := e.capturePGParameterACL(context.Background(), e.catalog.read(broken), roles, ref, d); err == nil || rows != nil {
		t.Fatal("foreign grantor accepted", err)
	}
	// Real owner operations, not synthetic parameter rows, produce explicit {}.
	seed := parameterSpaceRequest("parameter", "pacl_unit.empty")
	seed.GrantOption = false
	seed.Privileges = []parser.GrantPrivilege{{Name: "set"}}
	seed.Grantees = []parser.GrantGrantee{{Public: true}}
	if err := applyObjectGrantUnit(e, op, seed); err != nil {
		t.Fatal(err)
	}
	emptyAddress := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ParameterClassID, Name: "pacl_unit.empty"})
	seed.Revoke = true
	seed.Privileges = []parser.GrantPrivilege{{All: true}}
	seed.Grantees = []parser.GrantGrantee{{Role: parser.RoleSpec{Name: "postgres"}}}
	if err := applyObjectGrantUnit(e, op, seed); err != nil {
		t.Fatal(err)
	}
	seed.Grantees = []parser.GrantGrantee{{Public: true}}
	if err := applyObjectGrantUnit(e, op, seed); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range pgParameterACLUnitCapture(t, e, e.CatalogRead(), e.Roles()).anonymous {
		name, _ := row[1].Text()
		if name == "pacl_unit.empty" {
			found = true
			id, _ := row[0].OID()
			if ids.OID(id) != emptyAddress.ObjectID {
				t.Fatal("empty identity changed")
			}
			pgParameterACLUnitEntries(t, e, row[2], map[uint32][3]uint32{})
		}
	}
	if !found {
		t.Fatal("explicit empty elided")
	}
	seed.Revoke = false
	seed.Grantees = []parser.GrantGrantee{{Role: parser.RoleSpec{Name: "postgres"}}}
	if err := applyObjectGrantUnit(e, op, seed); err != nil {
		t.Fatal(err)
	}
	if _, exists := e.catalog.state.Find(emptyAddress); exists {
		t.Fatal("baseline not removed")
	}
	implicit, err := e.pgClassACL(roles, d.Columns[2].Usage.Type, authorization.ACLValue{})
	if err != nil || !implicit.IsNull() {
		t.Fatal("implicit carrier", implicit, err)
	}
}

func TestPGParameterACLSnapshot(t *testing.T) {
	e, op, reader := pgParameterACLUnitFixture(t)
	request := parameterSpaceRequest("parameter", "pacl_unit.snapshot")
	request.GrantOption = false
	request.Privileges = []parser.GrantPrivilege{{Name: "set"}}
	if err := applyObjectGrantUnit(e, op, request); err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if scope != nil {
			if err := scope.Finish(nil); err != nil {
				t.Error(err)
			}
		}
	}()
	e.mu.Lock()
	err = e.captureCommentSnapshotLocked(scope)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	before, roles := scope.commentCatalog, scope.commentRoles
	_, ref := pgParameterACLUnitSource(t, e, before)
	address := before.NameAddress(ids.NameKey{ClassID: ids.ParameterClassID, Name: "pacl_unit.snapshot"})
	old := before.MustParameter(address)
	request.Privileges = []parser.GrantPrivilege{{All: true}}
	request.GrantOption = true
	if err := applyObjectGrantUnit(e, op, request); err != nil {
		t.Fatal(err)
	}
	if e.catalog.state == before.State() || e.roles == scope.deparseAuth.roleRecord {
		t.Fatal("distinguishing live publication missing")
	}
	e.mu.Lock()
	captured, err := e.openRelationSourceLocked(scope, ref, op.RoleState().CurrentUser, queryRowMark{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if captured.count() != 1 {
		t.Fatal("old capture membership")
	}
	pgParameterACLUnitEntries(t, e, captured.anonymous[0][2], map[uint32][3]uint32{10: {10, 12288, 0}, uint32(reader.ID()): {10, 4096, 0}})
	current := pgParameterACLUnitCapture(t, e, e.CatalogRead(), e.Roles())
	pgParameterACLUnitEntries(t, e, current.anonymous[0][2], map[uint32][3]uint32{10: {10, 12288, 0}, uint32(reader.ID()): {10, 12288, 12288}})
	oldID, _ := captured.anonymous[0][0].OID()
	newID, _ := current.anonymous[0][0].OID()
	if oldID != newID || before.Get(address) != old || old.Name() != "pacl_unit.snapshot" || len(old.ACL.Entries) != 2 {
		t.Fatal("immutable identity/root")
	}
	retained := &finalRows{}
	if err := captureACLItemOutputValue(retained, capturedACLItemOutput{owner: e, roles: roles}, captured.anonymous[0][2]); err != nil {
		t.Fatal(err)
	}
	request.Revoke, request.GrantOption = true, false
	if err := applyObjectGrantUnit(e, op, request); err != nil {
		t.Fatal(err)
	}
	if rows := pgParameterACLUnitCapture(t, e, e.CatalogRead(), e.Roles()); rows.count() != 0 {
		t.Fatal("fresh removal")
	}
	if rows := pgParameterACLUnitCapture(t, e, before, roles); rows.count() != 1 {
		t.Fatal("old root removed")
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	scope = nil
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if name, err := retained.FormatACLRole(&e.aclAuthority, uint32(reader.ID())); err != nil || name != "grant_reader" {
		t.Fatal("retained display authority", name, err)
	}
	pgParameterACLUnitEntries(t, e, captured.anonymous[0][2], map[uint32][3]uint32{10: {10, 12288, 0}, uint32(reader.ID()): {10, 4096, 0}})
}

func TestPGParameterACLCancellation(t *testing.T) {
	e, op, _ := pgParameterACLUnitFixture(t)
	if err := applyObjectGrantUnit(e, op, parameterSpaceRequest("parameter", "pacl_unit.cancel")); err != nil {
		t.Fatal(err)
	}
	catalog, roles := e.CatalogRead(), e.Roles()
	d, ref := pgParameterACLUnitSource(t, e, catalog)
	state, oid, revision := e.catalog.state, e.catalog.nextOID, e.catalog.nextRevision
	for _, after := range []int{0, 3, catalog.DescriptorCount()} {
		ctx, cancel := context.WithCancel(context.Background())
		if after == 0 {
			cancel()
		}
		during := &aclValuesScanContext{Context: ctx, cancel: cancel, after: after}
		rows, err := e.capturePGParameterACL(during, catalog, roles, ref, d)
		cancel()
		if rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("partial/canceled capture", after, rows, err)
		}
		if e.catalog.state != state || e.catalog.nextOID != oid || e.catalog.nextRevision != revision {
			t.Fatal("cancellation mutated roots")
		}
	}
	if rows := pgParameterACLUnitCapture(t, e, catalog, roles); rows.count() != 1 {
		t.Fatal("capture did not recover")
	}
}

func pgParameterACLUnitQuery(columns ...string) *parser.Select {
	q := &parser.Select{From: &parser.TableReference{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog"}, {Text: "pg_parameter_acl"}}}}}
	for _, name := range columns {
		if name == "*" {
			q.Targets = append(q.Targets, parser.StarExpr{})
		} else {
			q.Targets = append(q.Targets, parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}})
		}
	}
	return q
}

func TestPGParameterACLAuthorization(t *testing.T) {
	e, ownerOp, _ := pgParameterACLUnitFixture(t)
	observer := roleAttributeCreate(t, e, ownerOp, "pacl_observer", true, parser.RoleOptions{}).Ref
	if err := applyObjectGrantUnit(e, ownerOp, parameterSpaceRequest("parameter", "pacl_unit.visible")); err != nil {
		t.Fatal(err)
	}
	op, done := roleOperation(t, e, "pacl_observer")
	defer done()
	if op.RoleState().CurrentUser != observer {
		t.Fatal("observer authority")
	}
	star, oid := pgParameterACLUnitQuery("*"), pgParameterACLUnitQuery("oid")
	held, err := e.Describe(op, star)
	if err != nil {
		t.Fatal(err)
	}
	oidHeld, err := e.Describe(op, oid)
	if err != nil {
		t.Fatal(err)
	}
	publish := func(table, column bool) {
		base := e.CatalogRead()
		d, _ := pgParameterACLUnitSource(t, e, base)
		next := *d
		next.ObjectRevision++
		next.ACL = authorization.ACLValue{Explicit: true}
		next.ColumnACL = nil
		entry := authorization.ACLEntry{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}
		if table {
			next.ACL.Entries = []authorization.ACLEntry{entry}
		}
		if column {
			next.ColumnACL = map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{entry}}}
		}
		stage := e.BeginCatalogStage(base)
		defer e.DiscardCatalogStage(stage)
		if err := stage.Replace(&next); err != nil {
			t.Fatal(err)
		}
		publication, err := e.ValidateCatalogStage(context.Background(), stage)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.PublishCatalog(publication, PublicationToken{Epoch: base.Epoch()}); err != nil {
			t.Fatal(err)
		}
	}
	execute := func(q *parser.Select, description *StatementDescription, denied bool, count int) {
		t.Helper()
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		scope.Expected = description
		result, executeErr := scope.Execute(q, 0, nil)
		err = scope.Finish(executeErr)
		if denied {
			var diagnostic *sqlstate.Error
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table pg_parameter_acl"}
			if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
				t.Fatal("current execution denial", err)
			}
			if result.Rows != nil {
				result.Rows.Close()
				t.Fatal("denied rows published")
			}
			return
		}
		if err != nil || result.Rows == nil {
			t.Fatal("ordinary observer", err)
		}
		defer result.Rows.Close()
		seen := 0
		for {
			next, err := result.Rows.Next(context.Background(), make([]sqltype.Value, len(result.Rows.Schema())))
			if err != nil {
				t.Fatal(err)
			}
			if !next {
				break
			}
			seen++
		}
		if seen != count {
			t.Fatal("unfiltered observer membership", seen, count)
		}
	}
	execute(star, &held, false, 1)
	publish(false, false)
	defer publish(true, false)
	execute(star, &held, true, 0)
	execute(oid, &oidHeld, true, 0)
	execute(pgParameterACLUnitQuery("parname"), nil, true, 0)
	execute(pgParameterACLUnitQuery("paracl"), nil, true, 0)
	noRows := pgParameterACLUnitQuery("*")
	noRows.Where = parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: false}}
	execute(noRows, nil, true, 0)
	limit := pgParameterACLUnitQuery("*")
	limit.Bounds = &parser.QueryBounds{Count: parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 0}}}
	execute(limit, nil, true, 0)
	publish(false, true)
	execute(oid, &oidHeld, false, 1)
	execute(star, &held, true, 0)
	execute(pgParameterACLUnitQuery("parname"), nil, true, 0)
	publish(false, false)
	execute(oid, &oidHeld, true, 0)
	publish(false, true)
	execute(oid, &oidHeld, false, 1)
	publish(true, false)
	execute(star, &held, false, 1)
	request := parameterSpaceRequest("parameter", "pacl_unit.visible")
	request.Revoke, request.GrantOption = true, false
	if err := applyObjectGrantUnit(e, ownerOp, request); err != nil {
		t.Fatal(err)
	}
	publish(false, false)
	execute(star, &held, true, 0)
	execute(noRows, nil, true, 0)
	execute(limit, nil, true, 0)
	publish(true, false)
	execute(star, &held, false, 0)
}
