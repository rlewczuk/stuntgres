package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func defaultACLUnitSource(t *testing.T, e *Engine, c catalog.Read) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 826}
	d, ok := c.Relation(a)
	if !ok {
		t.Fatal("missing default catalog")
	}
	return d, e.Catalog.Domain.Ref(c.Epoch(), a)
}
func defaultACLUnitCapture(t *testing.T, f defaultUnitFixture, c catalog.Read) *exec.Source {
	t.Helper()
	d, ref := defaultACLUnitSource(t, f.e, c)
	f.e.Mu.Lock()
	rows, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: c, Roles: f.e.RoleStore.PublishedRead(&f.e.Catalog.Owner, c), Ref: ref, Source: d, Owner: &f.e.Catalog.Owner, Runtime: EngineCatalogRuntime{engine: f.e}}, capturePGDefaultACL)
	f.e.Mu.Unlock()
	if err != nil || rows == nil {
		t.Fatal("capture", rows, err)
	}
	return rows
}

func TestPGDefaultACLIdentity(t *testing.T) {
	f := newDefaultUnitFixture(t)
	key := catalog.DefaultACLKey{Creator: f.owner, Class: authorization.DefaultTables}
	start := f.e.Catalog.NextOID()
	noop := defaultUnitStatement("tables", "all", []string{"default_owner"}, nil, []string{"default_owner"})
	if err := f.apply(noop); err != nil {
		t.Fatal(err)
	}
	if f.e.Catalog.NextOID() != start || f.e.Catalog.State.DefaultACLCount() != 0 {
		t.Fatal("baseline no-op reserved identity")
	}
	grant := f.grant(t, "tables", "select", nil)
	original := f.e.Catalog.State.DefaultACL(key)
	if original == nil || original.OID == 0 {
		t.Fatal("missing real identity")
	}
	frontier := f.e.Catalog.NextOID()
	if err := f.apply(grant); err != nil {
		t.Fatal(err)
	}
	if f.e.Catalog.State.DefaultACL(key) != original || f.e.Catalog.NextOID() != frontier {
		t.Fatal("no-op replaced or allocated")
	}
	f.grant(t, "tables", "update", nil)
	if f.e.Catalog.State.DefaultACL(key).OID != original.OID {
		t.Fatal("edit lost identity")
	}
	duplicates := defaultUnitStatement("sequences", "usage", []string{"default_owner", "default_owner"}, []string{"default_schema", "default_schema"}, []string{"default_reader"})
	if err := f.apply(duplicates); err != nil {
		t.Fatal(err)
	}
	if f.e.Catalog.NextOID() != frontier+1 || f.e.Catalog.State.DefaultACLCount() != 2 {
		t.Fatal("duplicate operands allocated intermediate identity")
	}
	state, roles, frontier := f.e.Catalog.State, f.e.RoleStore.Published, f.e.Catalog.NextOID()
	late := defaultUnitStatement("types", "usage", []string{"default_owner", "missing"}, nil, []string{"default_reader"})
	if err := f.apply(late); err == nil {
		t.Fatal("missing creator accepted")
	}
	if f.e.Catalog.State != state || f.e.RoleStore.Published != roles || f.e.Catalog.NextOID() != frontier {
		t.Fatal("late failure published or reserved")
	}
	revoke := defaultUnitStatement("tables", "all", []string{"default_owner"}, nil, []string{"default_reader"})
	revoke.Revoke = true
	if err := f.apply(revoke); err != nil {
		t.Fatal(err)
	}
	if f.e.Catalog.State.DefaultACL(key) != nil {
		t.Fatal("baseline not deleted")
	}
	if err := f.apply(grant); err != nil {
		t.Fatal(err)
	}
	if f.e.Catalog.State.DefaultACL(key).OID == original.OID {
		t.Fatal("recreated identity reused")
	}
	// Rewind the allocator into an actual policy identity; the sole owner skips it.
	used := f.e.Catalog.State.DefaultACL(key).OID
	f.e.Catalog.InstallOIDFrontierLocked(used)
	f.e.Mu.Lock()
	allocated, err := f.e.Catalog.AllocateOIDLocked()
	f.e.Mu.Unlock()
	if err != nil || allocated == used {
		t.Fatal("default OID collision", allocated, err)
	}
	f.e.Catalog.InstallOIDFrontierLocked(0)
	before := f.e.Catalog.State
	fail := defaultUnitStatement("schemas", "usage", []string{"default_owner"}, nil, []string{"default_reader"})
	if err := f.apply(fail); err == nil || f.e.Catalog.State != before {
		t.Fatal("exhaustion published candidate", err)
	}
	// Same-catalog principal remap is not a default-bearing Restore facility.
	copy := catalog.CloneState(before)
	f.e.Mu.Lock()
	remapped, _, err := f.e.remapPreparedCatalog(copy, f.e.RoleStore.PublishedRead(&f.e.Catalog.Owner, f.e.Catalog.Read(before)), f.e.RoleStore.PublishedRead(&f.e.Catalog.Owner, f.e.Catalog.Read(before)), nil)
	f.e.Mu.Unlock()
	if err != nil || remapped.DefaultACL(key).OID != used {
		t.Fatal("principal remap lost identity", err)
	}
}

func TestPGDefaultACLCaptureOwnership(t *testing.T) {
	f := newDefaultUnitFixture(t)
	for _, class := range []string{"tables", "sequences", "functions", "types", "schemas", "large objects"} {
		f.grant(t, class, "all", nil)
	}
	scoped := defaultUnitStatement("tables", "select", []string{"default_owner"}, []string{"default_schema"}, []string{"default_reader"})
	scoped.GrantOption = true
	if err := f.apply(scoped); err != nil {
		t.Fatal(err)
	}
	// PUBLIC is inserted after named entries in authority, but sorts first in output.
	public := defaultUnitStatement("tables", "select", []string{"default_owner"}, nil, []string{"PUBLIC"})
	if err := f.apply(public); err != nil {
		t.Fatal(err)
	}
	c := f.e.CatalogRead()
	d, _ := defaultACLUnitSource(t, f.e, c)
	state, roles, frontier := f.e.Catalog.State, f.e.RoleStore.Published, f.e.Catalog.NextOID()
	key := catalog.DefaultACLKey{Creator: f.owner, Class: authorization.DefaultTables}
	entries := slices.Clone(state.DefaultACL(key).ACL.Entries)
	captured := defaultACLUnitCapture(t, f, c)
	if captured.Count() != 7 {
		t.Fatal("actual policy count", captured.Count())
	}
	masks := map[string]uint32{"r": 1 | 2 | 4 | 8 | 16 | 32 | 64 | 16384, "S": 262, "f": 128, "T": 256, "n": 768, "L": 6}
	// Released table ACL bits are INSERT1/SELECT2/UPDATE4/DELETE8/
	// TRUNCATE16/REFERENCES32/TRIGGER64/MAINTAIN16384.
	seen := make(map[ids.OID]bool)
	for _, row := range captured.Anonymous {
		if len(row) != 5 {
			t.Fatal("width")
		}
		for i, id := range []sqltype.ID{26, 26, 26, 18, 1034} {
			if row[i].Type() != d.Columns[i].Usage.Type || row[i].Type().ID() != id || row[i].IsNull() {
				t.Fatal("typed nonnull", i, row[i])
			}
		}
		oid, _ := row[0].OID()
		creator, _ := row[1].OID()
		schema, _ := row[2].OID()
		code, _ := row[3].Text()
		if oid == 0 || seen[ids.OID(oid)] || creator != uint32(f.owner.ID()) {
			t.Fatal("identity", oid, creator)
		}
		seen[ids.OID(oid)] = true
		class := authorization.DefaultACLClass(slices.Index([]string{"r", "S", "f", "T", "n", "L"}, code) + 1)
		k := catalog.DefaultACLKey{Creator: f.owner, Class: class}
		if schema != 0 {
			k.Schema = f.schema
		}
		if c.State().DefaultACL(k).OID != ids.OID(oid) {
			t.Fatal("not producer identity")
		}
		want := map[uint32][3]uint32{uint32(f.owner.ID()): {uint32(f.owner.ID()), masks[code], 0}, uint32(f.reader.ID()): {uint32(f.owner.ID()), masks[code], 0}}
		if code == "f" {
			want[0] = [3]uint32{uint32(f.owner.ID()), 128, 0}
		}
		if code == "T" {
			want[0] = [3]uint32{uint32(f.owner.ID()), 256, 0}
		}
		if code == "r" && schema == 0 {
			want[0] = [3]uint32{uint32(f.owner.ID()), 2, 0}
		}
		if schema != 0 {
			want = map[uint32][3]uint32{uint32(f.reader.ID()): {uint32(f.owner.ID()), 2, 2}}
		}
		pgParameterACLUnitEntries(t, f.e, row[4], want)
		previous := uint32(0)
		for i := range row[4].ArrayLen() {
			_, grantee, _, _, _, _ := row[4].ArrayElement(i).ACLItem()
			if i > 0 && grantee <= previous {
				t.Fatal("non-numeric ACL order")
			}
			previous = grantee
		}
	}
	if !slices.Equal(entries, state.DefaultACL(key).ACL.Entries) || f.e.Catalog.State != state || f.e.RoleStore.Published != roles || f.e.Catalog.NextOID() != frontier {
		t.Fatal("read mutated authority")
	}
	f.grant(t, "tables", "update", []string{"default_schema"})
	if captured.Count() != 7 {
		t.Fatal("saved capture changed")
	}
	// A real empty routine policy remains nonnull and has no array dimensions.
	empty := defaultUnitStatement("functions", "all", []string{"default_owner"}, nil, []string{"default_owner", "default_reader", "PUBLIC"})
	empty.Revoke = true
	if err := f.apply(empty); err != nil {
		t.Fatal(err)
	}
	for _, row := range defaultACLUnitCapture(t, f, f.e.CatalogRead()).Anonymous {
		code, _ := row[3].Text()
		if code == "f" {
			pgParameterACLUnitEntries(t, f.e, row[4], map[uint32][3]uint32{})
		}
	}
	if err := f.e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if defaultACLUnitCapture(t, f, f.e.CatalogRead()).Count() != 0 || captured.Count() != 7 {
		t.Fatal("Reset changed detached capture")
	}
	for _, row := range captured.Anonymous {
		schema, _ := row[2].OID()
		if schema != 0 {
			pgParameterACLUnitEntries(t, f.e, row[4], map[uint32][3]uint32{uint32(f.reader.ID()): {uint32(f.owner.ID()), 2, 2}})
		}
	}
}

func TestPGDefaultACLAuthorityAndCancellation(t *testing.T) {
	f := newDefaultUnitFixture(t)
	f.grant(t, "tables", "select", nil)
	f.grant(t, "sequences", "usage", nil)
	c, roles := f.e.CatalogRead(), f.e.Roles()
	d, ref := defaultACLUnitSource(t, f.e, c)
	foreign := New()
	reject := func(view catalog.Read, r authorization.Read, reference ids.ObjectRef, source *catalog.Relation) {
		t.Helper()
		f.e.Mu.Lock()
		rows, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: view, Roles: r, Ref: reference, Source: source, Owner: &f.e.Catalog.Owner, Runtime: EngineCatalogRuntime{engine: f.e}}, capturePGDefaultACL)
		f.e.Mu.Unlock()
		var diagnostic *sqlstate.Error
		if err == nil || rows != nil || errors.As(err, &diagnostic) {
			t.Fatal("invariant authority accepted or fabricated SQLSTATE", rows, err)
		}
	}
	reject(foreign.CatalogRead(), roles, ref, d)
	reject(c, foreign.Roles(), ref, d)
	reject(c, roles, ref, nil)
	forged := ref
	forged.Epoch++
	reject(c, roles, forged, d)
	forged = foreign.Catalog.Domain.Ref(ref.Epoch, ref.Address)
	reject(c, roles, forged, d)
	detached := *d
	reject(c, roles, ref, &detached)
	forged = ref
	forged.Address.SubID = 1
	reject(c, roles, forged, d)
	for _, width := range []int{0, 4, 6} {
		next := catalog.CloneState(c.State())
		invalid := *d
		invalid.Columns = make([]catalog.Column, width)
		next.SetDescriptor(d.Address(), &invalid)
		reject(f.e.Catalog.Read(next), roles, ref, &invalid)
	}
	for _, missing := range []bool{false, true} {
		next := catalog.CloneState(c.State())
		if missing {
			next.SetDescriptor(catalog.CurrentDatabaseAddress, (*catalog.Database)(nil))
		} else {
			database := *c.MustDatabase(catalog.CurrentDatabaseAddress)
			database.Owner = f.reader
			next.SetDescriptor(catalog.CurrentDatabaseAddress, &database)
		}
		reject(f.e.Catalog.Read(next), roles, ref, d)
	}
	for i := range d.Columns {
		next := catalog.CloneState(c.State())
		invalid := *d
		invalid.Columns = slices.Clone(d.Columns)
		invalid.Columns[i].Usage.Type, _ = foreign.CatalogRead().LookupType(d.Columns[i].Usage.Type.ID())
		next.SetDescriptor(d.Address(), &invalid)
		reject(f.e.Catalog.Read(next), roles, ref, &invalid)
	}
	key := catalog.DefaultACLKey{Creator: f.owner, Class: authorization.DefaultTables}
	record := c.State().DefaultACL(key)
	for _, kind := range []string{"nil", "zero", "duplicate", "class", "creator", "schema", "grantor", "grantee", "implicit"} {
		next := catalog.CloneState(c.State())
		// CloneState shares immutable default records and their map until an
		// overlay replaces it. Corruption fixtures must detach that map first.
		next.SetDefaultACLs(maps.Collect(c.State().DefaultACLs()))
		broken := *record
		k := key
		switch kind {
		case "nil":
			next.SetDefaultACL(key, nil)
		case "zero":
			broken.OID = 0
			next.SetDefaultACL(key, &broken)
		case "duplicate":
			k.Class = authorization.DefaultSequences
			broken.ACL = authorization.ACLValue{Explicit: true}
			next.SetDefaultACL(k, &broken)
		case "class":
			k.Class = authorization.DefaultParameters
			next.SetDefaultACL(k, &broken)
		case "creator":
			k.Creator = foreign.RoleStore.Domain.Principal(f.owner.ID())
			next.SetDefaultACL(k, &broken)
		case "schema":
			k.Schema = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 999999}
			next.SetDefaultACL(k, &broken)
		case "grantor", "grantee":
			broken.ACL.Entries = slices.Clone(record.ACL.Entries)
			if kind == "grantor" {
				broken.ACL.Entries[0].Grantor = foreign.RoleStore.Domain.Principal(f.owner.ID())
			} else {
				broken.ACL.Entries[0].Grantee.Role = foreign.RoleStore.Domain.Principal(f.owner.ID())
			}
			next.SetDefaultACL(key, &broken)
		case "implicit":
			broken.ACL.Explicit = false
			next.SetDefaultACL(key, &broken)
		}
		reject(f.e.Catalog.Read(next), roles, ref, d)
		if c.State().DefaultACL(key) != record || c.State().DefaultACLCount() != 2 {
			t.Fatal("corruption fixture mutated the captured authority")
		}
	}
	state, roleRoot, frontier := f.e.Catalog.State, f.e.RoleStore.Published, f.e.Catalog.NextOID()
	for _, at := range []int{1, 2, 3} {
		ctx, cancel := context.WithCancel(context.Background())
		controlled := &namespaceCancelContext{Context: ctx, cancel: cancel, at: at}
		f.e.Mu.Lock()
		rows, err := CaptureCatalogSourceRows(pgcatalog.Input{Context: controlled, Catalog: c, Roles: roles, Ref: ref, Source: d, Owner: &f.e.Catalog.Owner, Runtime: EngineCatalogRuntime{engine: f.e}}, capturePGDefaultACL)
		f.e.Mu.Unlock()
		cancel()
		if rows != nil || !errors.Is(err, context.Canceled) || f.e.Catalog.State != state || f.e.RoleStore.Published != roleRoot || f.e.Catalog.NextOID() != frontier {
			t.Fatal("canceled partial result or mutated authority", at, rows, err)
		}
	}
	if defaultACLUnitCapture(t, f, c).Count() != 2 {
		t.Fatal("capture did not recover")
	}
}

func TestPGDefaultACLPermissionPublication(t *testing.T) {
	f := newDefaultUnitFixture(t)
	op, done := roleOperation(t, f.e, "default_reader")
	defer done()
	query := func() *parser.Select {
		return &parser.Select{From: &parser.TableReference{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog"}, {Text: "pg_default_acl"}}}}, Targets: []parser.Expr{parser.StarExpr{}}, Where: parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: false}}}
	}
	q := query()
	held, err := f.e.Describe(op, q)
	if err != nil {
		t.Fatal(err)
	}
	publish := func(allowed bool) {
		base := f.e.CatalogRead()
		d, _ := defaultACLUnitSource(t, f.e, base)
		next := *d
		next.ObjectRevision++
		next.Security.ACL = authorization.ACLValue{Explicit: true}
		if allowed {
			next.Security.ACL.Entries = []authorization.ACLEntry{{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}}
		}
		stage := f.e.BeginCatalogStage(base)
		defer f.e.DiscardCatalogStage(stage)
		if err := stage.Replace(&next); err != nil {
			t.Fatal(err)
		}
		if err := defaultUnitPublish(t, f.e, stage); err != nil {
			t.Fatal(err)
		}
	}
	execute := func(description *StatementDescription, denied bool) {
		t.Helper()
		scope, err := f.e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		scope.Expected = description
		result, executeErr := scope.Execute(q, 0, nil)
		err = scope.Finish(executeErr)
		if denied {
			var diagnostic *sqlstate.Error
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table pg_default_acl"}
			if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
				t.Fatal("execution permission recheck", err)
			}
			if result.Rows != nil {
				result.Rows.Close()
				t.Fatal("denied Rows")
			}
			return
		}
		if err != nil || result.Rows == nil {
			t.Fatal(err)
		}
		defer result.Rows.Close()
		next, err := result.Rows.Next(context.Background(), make([]sqltype.Value, len(result.Rows.Schema())))
		if err != nil || next {
			t.Fatal("empty selection", next, err)
		}
	}
	execute(nil, false)
	execute(&held, false)
	publish(false)
	execute(nil, true)
	execute(&held, true)
	fresh, err := f.e.Describe(op, q)
	if err != nil {
		t.Fatal(err)
	}
	execute(&fresh, true)
	publish(true)
	execute(nil, false)
	execute(&held, false)
}
