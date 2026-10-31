package stored_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func infoTriggersUnitText(t *testing.T, value sqltype.Value) string {
	t.Helper()
	base, err := value.BaseValue()
	if err != nil || (base.Type().ID() != sqltype.Name && base.Type().ID() != sqltype.VarChar) {
		t.Fatal("textual domain payload lost", base, err)
	}
	text, err := base.Text()
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func infoTriggersInput(t *testing.T, e *core.Engine, scope *core.StatementScope) pgcatalog.Input {
	t.Helper()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(scope.Op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	read := auth.Catalog
	source := read.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoTriggersOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: read, Roles: auth.Roles, Owner: read.Owner(), Source: source, Ref: read.Owner().Domain.Ref(read.Epoch(), source.Address()), Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: auth, Database: "captured_database"})}
}

func TestInfoTriggersProducer(t *testing.T) {
	e, scope, read, relation, function := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoTriggersOID)
	if !ok {
		t.Fatal("missing producer")
	}
	empty, err := produce(infoTriggersInput(t, e, scope))
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatal("empty", empty, err)
	}
	a := triggerUnitDefinition(t, read, relation, function, "a", stored.TriggerInsert)
	z := triggerUnitDefinition(t, read, relation, function, "z", stored.TriggerInsert|stored.TriggerUpdate)
	p := z.Parts()
	p.OID++
	z, err = stored.NewTrigger(p)
	if err != nil {
		t.Fatal(err)
	}
	triggerUnitInstall(t, e, relation, a, z)
	in := infoTriggersInput(t, e, scope)
	if in.Source.Name() != "triggers" || in.Source.Namespace().ObjectID != 13229 || !in.Source.VirtualView || in.Source.RowType != 13463 {
		t.Fatal("source identity", in.Source)
	}
	rowType := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13463})
	arrayType := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13462})
	if rowType.Array != 13462 || rowType.OwningRelation != in.Source.Address() || arrayType.OwningRelation != in.Source.Address() || arrayType.SQLType.Element() != rowType.SQLType {
		t.Fatal("row/array reciprocity")
	}
	rows, err := produce(in)
	if err != nil || len(rows) != 3 {
		t.Fatal(rows, err)
	}
	want := [][3]string{{"a", "INSERT", "1"}, {"z", "INSERT", "2"}, {"z", "UPDATE", "1"}}
	for n, row := range rows {
		if len(row) != 17 {
			t.Fatal("width", row)
		}
		for i, value := range row {
			column := in.Source.Columns[i]
			if value.Type() != column.Usage.Type || !value.Type().IsDomain() || !value.ValidResult(column.Usage) || !column.Nullable || !column.NullabilityKnown || column.OriginTable != 16237 || column.OriginAttribute != int16(i+1) {
				t.Fatal("domain/shape", i, value, column)
			}
			collation := uint32(950)
			if i == 7 || i == 16 {
				collation = 0
			}
			if column.Usage.Typmod != -1 || column.Usage.Collation.ID != collation || column.DeclaredDimensions != 0 {
				t.Fatal("logical metadata", i, column)
			}
		}
		for _, i := range []int{0, 4} {
			if infoUserMappingsUnitText(t, row[i]) != "captured_database" {
				t.Fatal("database", row)
			}
		}
		for _, i := range []int{8, 12, 13, 14, 15, 16} {
			if !row[i].IsNull() {
				t.Fatal("NULL", i, row[i])
			}
		}
		base, err := row[7].BaseValue()
		if err != nil {
			t.Fatal(err)
		}
		rank, err := base.Integer()
		if err != nil {
			t.Fatal(err)
		}
		got := [3]string{infoUserMappingsUnitText(t, row[2]), infoTriggersUnitText(t, row[3]), strconv.FormatInt(rank, 10)}
		if got != want[n] {
			t.Fatal(got, want[n])
		}
		if infoTriggersUnitText(t, row[9]) != "EXECUTE FUNCTION public.trigger_unit_fn()" || infoTriggersUnitText(t, row[10]) != "ROW" || infoTriggersUnitText(t, row[11]) != "BEFORE" {
			t.Fatal("presentation", row)
		}
	}
	runtime := core.CatalogRuntimeFieldsOf(in.Runtime)
	runtime.Database = ""
	in.Runtime = core.NewCatalogRuntime(runtime)
	emptyDatabase, err := produce(in)
	if err != nil || len(emptyDatabase) != 3 {
		t.Fatal(emptyDatabase, err)
	}
	for _, row := range emptyDatabase {
		if row[0].IsNull() || infoUserMappingsUnitText(t, row[0]) != "" || infoUserMappingsUnitText(t, row[4]) != "" {
			t.Fatal("empty captured database", row)
		}
	}
}

func TestInfoTriggersCaptureOwnership(t *testing.T) {
	infoTriggersCaptureFailures(t)
	e, scope, read, relation, function := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	definition := triggerUnitDefinition(t, read, relation, function, "before", stored.TriggerInsert)
	relation = triggerUnitInstall(t, e, relation, definition)
	in := infoTriggersInput(t, e, scope)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoTriggersOID)
	retained, err := core.CaptureRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	backing := &retained.Anonymous[0][0]
	oldRoot, oldSource := in.Catalog.State(), in.Source
	parts := definition.Parts()
	parts.Name = "after"
	renamed, err := stored.NewTrigger(parts)
	if err != nil {
		t.Fatal(err)
	}
	triggerUnitInstall(t, e, relation, renamed)
	fresh := infoTriggersInput(t, e, scope)
	rows, err := produce(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if oldRoot == fresh.Catalog.State() || in.Owner != fresh.Owner || oldSource != fresh.Source || retained.Ref != in.Ref || backing != &retained.Anonymous[0][0] || infoUserMappingsUnitText(t, retained.Anonymous[0][2]) != "before" || infoUserMappingsUnitText(t, rows[0][2]) != "after" || definition.Name() != "before" {
		t.Fatal("capture backing/roots/definitions")
	}
	for _, invalid := range []func(*pgcatalog.Input){
		func(x *pgcatalog.Input) { x.Ref = ids.ObjectRef{} },
		func(x *pgcatalog.Input) { copy := *x.Source; x.Source = &copy },
		func(x *pgcatalog.Input) { x.Owner = nil },
	} {
		bad := fresh
		invalid(&bad)
		if rows, err := produce(bad); err == nil || rows != nil {
			t.Fatal("invalid authority accepted", rows, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	fresh.Context = ctx
	if rows, err := produce(fresh); !errors.Is(err, context.Canceled) || rows != nil {
		t.Fatal("cancellation", rows, err)
	}
}

func TestInfoTriggersPresentation(t *testing.T) {
	e, scope, read, relation, function := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	definition := triggerUnitDefinition(t, read, relation, function, "args", stored.TriggerUpdate)
	parts := definition.Parts()
	parts.Arguments = []string{"O'Reilly", "", `a\b`, "7"}
	parts.UpdateColumns = []int32{2}
	definition, err := stored.NewTrigger(parts)
	if err != nil {
		t.Fatal(err)
	}
	definition = infoTriggersWhen(t, e, scope, read, relation, definition)
	relation = triggerUnitInstall(t, e, relation, definition)
	before := definition.Parts()
	in := infoTriggersInput(t, e, scope)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoTriggersOID)
	rows, err := produce(in)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	if got := infoTriggersUnitText(t, rows[0][9]); got != `EXECUTE FUNCTION public.trigger_unit_fn('O''Reilly', '', 'a\b', '7')` {
		t.Fatal(got)
	}
	if got := infoTriggersUnitText(t, rows[0][8]); got != "(new.v > 0)" {
		t.Fatal("WHEN", got)
	}
	after := stored.TriggersOf(relation)[0].Parts()
	if !reflect.DeepEqual(before, after) || &before.Arguments[0] != &after.Arguments[0] || &before.UpdateColumns[0] != &after.UpdateColumns[0] || &before.References[0] != &after.References[0] {
		t.Fatal("renderer changed stored backing")
	}
	for _, quote := range []bool{false, true} {
		presentation := stored.Presentation{Path: []ids.ObjectAddress{relation.Namespace()}, Output: sqltype.OutputEnvironment{QuoteAllIdentifiers: quote}}
		count := 0
		err := stored.VisitTriggerDefinitions(t.Context(), in.Catalog, relation, presentation, true, func(f stored.TriggerPresentation) error {
			count++
			statement, condition := `EXECUTE FUNCTION trigger_unit_fn('O''Reilly', '', 'a\b', '7')`, "(new.v > 0)"
			if quote {
				statement, condition = `EXECUTE FUNCTION "trigger_unit_fn"('O''Reilly', '', 'a\b', '7')`, `("new"."v" > 0)`
			}
			if f.Statement != statement || f.Condition != condition {
				t.Fatal("captured path/output", f)
			}
			return nil
		})
		if err != nil || count != 1 {
			t.Fatal(count, err)
		}
	}
	for _, nonstandard := range []bool{false, true} {
		presentation := stored.Presentation{Path: []ids.ObjectAddress{relation.Namespace()}, NonstandardStrings: nonstandard}
		count := 0
		err := stored.VisitTriggerDefinitions(t.Context(), in.Catalog, relation, presentation, true, func(f stored.TriggerPresentation) error {
			count++
			want := `EXECUTE FUNCTION trigger_unit_fn('O''Reilly', '', 'a\b', '7')`
			if nonstandard {
				want = `EXECUTE FUNCTION trigger_unit_fn('O''Reilly', '', 'a\\b', '7')`
			}
			if f.Statement != want {
				t.Fatal("captured string setting", f.Statement, want)
			}
			return nil
		})
		if err != nil || count != 1 {
			t.Fatal(count, err)
		}
	}
	if got := infoTriggersUnitText(t, rows[0][9]); got != `EXECUTE FUNCTION public.trigger_unit_fn('O''Reilly', '', 'a\b', '7')` {
		t.Fatal("retained presentation changed", got)
	}
}

func TestInfoTriggersVisibility(t *testing.T) {
	infoTriggersACLVisibility(t)
	e, scope, read, relation, function := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	definition := triggerUnitDefinition(t, read, relation, function, "visible", stored.TriggerInsert)
	triggerUnitInstall(t, e, relation, definition)
	in := infoTriggersInput(t, e, scope)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoTriggersOID)
	rows, err := produce(in)
	if err != nil || len(rows) != 1 {
		t.Fatal("owner visibility", rows, err)
	}
	// A foreign principal cannot be adopted merely because its numeric ID matches.
	other, otherScope, _, _, _ := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	foreign := infoTriggersInput(t, other, otherScope)
	in.Roles = foreign.Roles
	if rows, err := produce(in); err == nil || rows != nil {
		t.Fatal("foreign roles accepted", rows, err)
	}
}

func infoTriggersCaptureFailures(t *testing.T) {
	t.Helper()
	e, scope, read, relation, function := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	definition := triggerUnitDefinition(t, read, relation, function, "actual", stored.TriggerUpdate)
	definition = infoTriggersWhen(t, e, scope, read, relation, definition)
	relation = triggerUnitInstall(t, e, relation, definition)
	in := infoTriggersInput(t, e, scope)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoTriggersOID)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	interrupted := in
	interrupted.Context = ctx
	interrupted.Runtime = triggerCancelRuntime{Runtime: in.Runtime, cancel: cancel}
	if rows, err := produce(interrupted); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("candidate cancellation exposed prefix", rows, err)
	}
	for _, remaining := range []int{1, 2, 3} {
		rendererContext := &dtpCancelContext{Context: context.Background(), remaining: remaining}
		var facts []stored.TriggerPresentation
		err := stored.VisitTriggerDefinitions(rendererContext, in.Catalog, relation, stored.Presentation{}, true, func(f stored.TriggerPresentation) error { facts = append(facts, f); return nil })
		if !errors.Is(err, context.Canceled) || facts != nil {
			t.Fatal("render cancellation", facts, err)
		}
	}
	for _, epoch := range []uint64{0, uint64(in.Ref.Epoch) + 1} {
		bad := in
		bad.Ref = bad.Ref.Domain().Ref(in.Ref.Epoch, bad.Ref.Address)
		// ObjectRef epoch type is retained by assignment from the captured reference.
		if epoch == 0 {
			bad.Ref.Epoch = 0
		} else {
			bad.Ref.Epoch++
		}
		if rows, err := produce(bad); rows != nil || err == nil {
			t.Fatal("invalid epoch", rows, err)
		}
	}
	state := catalog.CloneState(in.Catalog.State())
	broken := *function
	broken.SQL = nil
	state.SetDescriptor(broken.Address(), &broken)
	bad := in
	bad.Catalog = e.Catalog.Read(state)
	runtime := core.CatalogRuntimeFieldsOf(in.Runtime)
	runtime.Auth.Recapture(bad.Catalog)
	bad.Roles = runtime.Auth.Roles
	bad.Runtime = core.NewCatalogRuntime(runtime)
	if rows, err := produce(bad); rows != nil || err == nil {
		t.Fatal("malformed current routine", rows, err)
	}
	p := definition.Parts()
	p.UpdateColumns = []int32{99}
	invalid, err := stored.NewTrigger(p)
	if err != nil {
		t.Fatal(err)
	}
	copy := *relation
	copy.Triggers = stored.TriggersPayload([]stored.Trigger{invalid})
	state = catalog.CloneState(in.Catalog.State())
	state.SetDescriptor(copy.Address(), &copy)
	bad = in
	bad.Catalog = e.Catalog.Read(state)
	runtime = core.CatalogRuntimeFieldsOf(in.Runtime)
	runtime.Auth.Recapture(bad.Catalog)
	bad.Roles = runtime.Auth.Roles
	bad.Runtime = core.NewCatalogRuntime(runtime)
	if rows, err := produce(bad); rows != nil || err == nil {
		t.Fatal("malformed current attribute", rows, err)
	}
}

func infoTriggersACLVisibility(t *testing.T) {
	t.Helper()
	e, scope, read, relation, function := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	definition := triggerUnitDefinition(t, read, relation, function, "grant_unit", stored.TriggerUpdate)
	definition = infoTriggersWhen(t, e, scope, read, relation, definition)
	relation = triggerUnitInstall(t, e, relation, definition)
	roles := enginetest.CloneRoleFixture(e.RoleStore.Published)
	reader := e.RoleStore.Domain.Principal(20001)
	member := e.RoleStore.Domain.Principal(20002)
	for _, actor := range []authorization.PrincipalRef{reader, member} {
		name := "trigger_reader"
		if actor == member {
			name = "trigger_member"
		}
		enginetest.PutFixtureRole(roles, authorization.RoleDescriptor{Ref: actor, Name: name, Login: true, Inherit: true, ConnectionLimit: -1})
	}
	enginetest.SetFixtureEdges(roles, append(enginetest.FixtureEdges(roles), authorization.Membership{Role: relation.Owner, Member: member, Grantor: relation.Owner, Inherit: true, Set: true}))
	e.RoleStore.PublishLocked(roles)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoTriggersOID)
	check := func(table *catalog.Relation, actor authorization.PrincipalRef, want int, condition bool) {
		t.Helper()
		state := catalog.CloneState(e.Catalog.State)
		state.SetDescriptor(table.Address(), table)
		in := infoTriggersInput(t, e, scope)
		view := e.Catalog.Read(state)
		runtime := core.CatalogRuntimeFieldsOf(in.Runtime)
		runtime.Auth.State.CurrentUser = actor
		runtime.Auth.Recapture(view)
		in.Catalog, in.Roles, in.Runtime = view, runtime.Auth.Roles, core.NewCatalogRuntime(runtime)
		rows, err := produce(in)
		if err != nil || len(rows) != want {
			t.Fatal("ACL visibility", actor, table.Security, rows, err)
		}
		if want != 0 && rows[0][8].IsNull() == condition {
			t.Fatal("owner-only condition", rows)
		}
	}
	for _, explicit := range []bool{false, true} {
		copy := *relation
		copy.Security.ACL = authorization.ACLValue{Explicit: explicit}
		check(&copy, reader, 0, false)
		check(&copy, relation.Owner, 1, true)
		check(&copy, member, 1, true)
	}
	for _, privilege := range []authorization.Privileges{authorization.SelectPrivilege, authorization.MaintainPrivilege, authorization.InsertPrivilege, authorization.UpdatePrivilege, authorization.DeletePrivilege, authorization.TruncatePrivilege, authorization.ReferencesPrivilege, authorization.TriggerPrivilege} {
		copy := *relation
		copy.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: relation.Owner, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: privilege}}}
		want := 1
		if privilege == authorization.SelectPrivilege || privilege == authorization.MaintainPrivilege {
			want = 0
		}
		check(&copy, reader, want, false)
	}
	for _, privilege := range []authorization.Privileges{authorization.SelectPrivilege, authorization.InsertPrivilege, authorization.UpdatePrivilege, authorization.ReferencesPrivilege} {
		copy := *relation
		copy.Security.ACL = authorization.ACLValue{Explicit: true}
		copy.Security.ColumnACL = map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{{Grantor: relation.Owner, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: privilege}}}}
		want := 1
		if privilege == authorization.SelectPrivilege {
			want = 0
		}
		check(&copy, reader, want, false)
		copy.ColumnMeta.Dropped = map[int32]catalog.AttributeMetadata{1: {}}
		check(&copy, reader, 0, false)
	}
	old := infoTriggersInput(t, e, scope)
	oldRuntime := core.CatalogRuntimeFieldsOf(old.Runtime)
	oldRuntime.Auth.State.CurrentUser = member
	old.Runtime = core.NewCatalogRuntime(oldRuntime)
	retained, err := produce(old)
	if err != nil || len(retained) != 1 {
		t.Fatal("old role capture", retained, err)
	}
	oldRoles := oldRuntime.Auth.RoleRecord
	successor := enginetest.CloneRoleFixture(e.RoleStore.Published)
	enginetest.SetFixtureEdges(successor, nil)
	e.RoleStore.PublishLocked(successor)
	if oldRoles == e.RoleStore.Published {
		t.Fatal("role root did not advance")
	}
	check(relation, member, 0, false)
	again, err := produce(old)
	if err != nil || len(again) != 1 || retained[0][8].IsNull() {
		t.Fatal("retained paired authority changed", again, err)
	}
	mixed := old
	mixed.Roles = infoTriggersInput(t, e, scope).Roles
	if rows, err := produce(mixed); rows != nil || err == nil {
		t.Fatal("mixed role frontier accepted", rows, err)
	}
}

// Only the deterministic cancellation boundary is controlled; all definitions,
// authority validation, rendering and produced rows use the genuine runtime.
type triggerCancelRuntime struct {
	pgcatalog.Runtime
	cancel context.CancelFunc
}

func (r triggerCancelRuntime) VisitTriggerDefinitions(ctx context.Context, read catalog.Read, relation *catalog.Relation, disclose bool, visit func(pgcatalog.TriggerFact) error) error {
	return r.Runtime.VisitTriggerDefinitions(ctx, read, relation, disclose, func(f pgcatalog.TriggerFact) error {
		if err := visit(f); err != nil {
			return err
		}
		r.cancel()
		return nil
	})
}
