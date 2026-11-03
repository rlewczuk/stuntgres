package pgcatalog_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func statioSysUnitInput(t *testing.T, e *core.Engine, auth core.AuthorizationView, oid ids.OID) pgcatalog.Input {
	t.Helper()
	read := auth.Catalog
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}
	source := read.MustRelation(address)
	wantNames := []string{"relid", "schemaname", "relname", "blks_read", "blks_hit"}
	wantTypes := []sqltype.ID{sqltype.Oid, sqltype.Name, sqltype.Name, sqltype.Int8, sqltype.Int8}
	if len(source.Columns) != 5 || source.RelationKind != catalog.CatalogRelation || !source.VirtualView {
		t.Fatal("five-column virtual view", source)
	}
	for i, column := range source.Columns {
		typ, ok := read.LookupType(wantTypes[i])
		if !ok || column.Name != wantNames[i] || column.Usage.Type != typ || column.OriginTable != uint32(oid) || column.OriginAttribute != int16(i+1) || source.Attributes[i] != int32(i+1) || !column.Nullable || !column.NullabilityKnown {
			t.Fatal("source-owned type/origin/nullability", i, column)
		}
	}
	return pgcatalog.Input{Context: context.Background(), Catalog: read, Roles: auth.Roles, Owner: &e.Catalog.Owner, Ref: e.Catalog.Domain.Ref(read.Epoch(), address), Source: source, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth, Settings: auth.Session.Settings})}
}

func statioSysUnitRows(t *testing.T, in pgcatalog.Input) [][]sqltype.Value {
	t.Helper()
	producer, ok := pgcatalog.ProducerFor(in.Source.Address().ObjectID)
	if !ok {
		t.Fatal("missing registered producer")
	}
	rows, err := producer(in)
	if err != nil || rows == nil {
		t.Fatal("capture", rows, err)
	}
	return rows
}

func statioSysUnitRow(t *testing.T, in pgcatalog.Input, oid ids.OID, schema, name string, read, hit int64) []sqltype.Value {
	t.Helper()
	identity, err := sqltype.OIDValue(in.Source.Columns[0].Usage.Type, uint32(oid))
	if err != nil {
		t.Fatal(err)
	}
	reads, err := sqltype.Integer(in.Source.Columns[3].Usage.Type, read)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := sqltype.Integer(in.Source.Columns[4].Usage.Type, hit)
	if err != nil {
		t.Fatal(err)
	}
	return []sqltype.Value{identity, sqltype.String(in.Source.Columns[1].Usage.Type, schema), sqltype.String(in.Source.Columns[2].Usage.Type, name), reads, hits}
}

func statioSysUnitBag(t *testing.T, rows [][]sqltype.Value) map[ids.OID][]sqltype.Value {
	t.Helper()
	bag := make(map[ids.OID][]sqltype.Value, len(rows))
	for _, row := range rows {
		if len(row) != 5 {
			t.Fatal("row width", row)
		}
		oid, err := row[0].OID()
		if err != nil || bag[ids.OID(oid)] != nil {
			t.Fatal("genuine unique identity", row, err)
		}
		for _, cell := range row {
			if cell.IsNull() {
				t.Fatal("fabricated NULL", row)
			}
		}
		bag[ids.OID(oid)] = row
	}
	return bag
}

func TestPGStatioSysSequencesClassification(t *testing.T) {
	f := newDefaultUnitFixture(t)
	f.actor(f.reader)
	base := f.e.CatalogRead()
	baseInput := statioSysUnitInput(t, f.e, dtpUnitAuthority(t, f, base), pgcatalog.StatioSysSequencesOID)
	if rows := statioSysUnitRows(t, baseInput); len(rows) != 0 {
		t.Fatal("fabricated bootstrap system sequence", rows)
	}
	state := catalog.CloneState(f.e.Catalog.State)
	namespaces := map[string]ids.ObjectAddress{}
	for _, descriptor := range base.Descriptors() {
		if ns, ok := descriptor.(*catalog.Namespace); ok {
			namespaces[ns.Name()] = ns.Address()
		}
	}
	cases := []struct {
		schema string
		system bool
		n      int64
	}{
		{"pg_catalog", true, 0}, {"information_schema", true, 1}, {"pg_toast", true, 3},
		{"pg_toast_42", true, 0}, {"pg_toasty", true, 0}, {"PG_TOAST", false, 0},
		{"Pg_Catalog", false, 0}, {"information_schema_extra", false, 0}, {"public", false, 0},
		{"custom", false, 0}, {"pg_temp_3", false, 0},
	}
	relations := make([]*catalog.Relation, len(cases))
	var writer statistics.Writer
	for i, c := range cases {
		ns, ok := namespaces[c.schema]
		if !ok {
			ns = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.OID(31000 + i)}
			d := &catalog.Namespace{ObjectAddress: ns, ObjectName: c.schema, ObjectRevision: 1, Owner: f.owner, ACL: authorization.ACLValue{Explicit: true}}
			state.SetDescriptor(ns, d)
			state.BindName(catalog.DescriptorNameKey(d), ns)
			namespaces[c.schema] = ns
		}
		d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(32000 + i)}, ObjectNamespace: ns, ObjectName: fmt.Sprintf("sys_unit_%d", i), ObjectRevision: 1, Owner: f.owner, RelationKind: catalog.SequenceRelation, Sequence: &catalog.SequenceOptions{SQLType: sqltype.Int8, Increment: 1, Min: 1, Max: 9223372036854775807, Start: 1, Cache: 1}, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true}}}
		state.SetDescriptor(d.Address(), d)
		state.BindName(catalog.DescriptorNameKey(d), d.Address())
		relations[i] = d
		if c.n != 0 {
			writer.Add(&f.e.Statistics, statistics.Key{Object: f.e.Catalog.Domain.Ref(base.Epoch(), d.Address())}, statistics.Fact{SequencePageAccesses: c.n})
		}
	}
	for i, kind := range []catalog.RelationKind{catalog.TableRelation, catalog.ViewRelation, catalog.IndexRelation} {
		d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(32100 + i)}, ObjectNamespace: namespaces["pg_catalog"], ObjectName: fmt.Sprintf("sys_decoy_%d", i), ObjectRevision: 1, Owner: f.owner, RelationKind: kind}
		state.SetDescriptor(d.Address(), d)
	}
	writer.Flush(&f.e.Statistics, time.Now(), true)
	view := f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f, view)
	denied := relations[3]
	if err := auth.Check(access.Requirement{Object: f.e.Catalog.Domain.Ref(view.Epoch(), denied.Address()), Privileges: authorization.SelectPrivilege}); err == nil {
		t.Fatal("fixture sequence ACL unexpectedly admits reader")
	}
	if err := auth.CanUse(f.e.Catalog.Domain.Ref(view.Epoch(), denied.Namespace())); err == nil {
		t.Fatal("fixture namespace ACL unexpectedly admits reader")
	}
	input := statioSysUnitInput(t, f.e, auth, pgcatalog.StatioSysSequencesOID)
	held := statioSysUnitRows(t, input)
	got := statioSysUnitBag(t, held)
	all := statioSysUnitBag(t, statioSysUnitRows(t, statioSysUnitInput(t, f.e, auth, pgcatalog.StatioAllSequencesOID)))
	want := map[ids.OID][]sqltype.Value{}
	for i, c := range cases {
		d := relations[i]
		var read, hit int64
		if c.n > 0 {
			read, hit = 1, c.n-1
		}
		expected := statioSysUnitRow(t, input, d.Address().ObjectID, c.schema, d.Name(), read, hit)
		if !reflect.DeepEqual(all[d.Address().ObjectID], expected) {
			t.Fatal("all provider independent values", c.schema, all[d.Address().ObjectID], expected)
		}
		if c.system {
			want[d.Address().ObjectID] = expected
		}
	}
	if len(all) != len(cases) || !reflect.DeepEqual(got, want) {
		t.Fatal("case-sensitive classification/typed provider values", got, want, len(all))
	}
	// Successor descriptor identities, not stale statistics keys, own names and
	// membership. The held source retains its original immutable cells.
	changed := catalog.CloneState(state)
	rename := *relations[0]
	rename.ObjectName, rename.ObjectRevision = "renamed_system", rename.Revision()+1
	changed.UnbindName(catalog.DescriptorNameKey(relations[0]))
	changed.SetDescriptor(rename.Address(), &rename)
	changed.BindName(catalog.DescriptorNameKey(&rename), rename.Address())
	moveOut := *relations[1]
	moveOut.ObjectNamespace, moveOut.ObjectRevision = namespaces["public"], moveOut.Revision()+1
	changed.UnbindName(catalog.DescriptorNameKey(relations[1]))
	changed.SetDescriptor(moveOut.Address(), &moveOut)
	changed.BindName(catalog.DescriptorNameKey(&moveOut), moveOut.Address())
	moveIn := *relations[5]
	moveIn.ObjectNamespace, moveIn.ObjectRevision = namespaces["pg_toasty"], moveIn.Revision()+1
	changed.UnbindName(catalog.DescriptorNameKey(relations[5]))
	changed.SetDescriptor(moveIn.Address(), &moveIn)
	changed.BindName(catalog.DescriptorNameKey(&moveIn), moveIn.Address())
	changed.UnbindName(catalog.DescriptorNameKey(relations[2]))
	changed.RemoveDescriptor(relations[2].Address()) // Its N=3 fact intentionally remains.
	auth.Recapture(f.e.Catalog.Read(changed))
	fresh := statioSysUnitBag(t, statioSysUnitRows(t, statioSysUnitInput(t, f.e, auth, pgcatalog.StatioSysSequencesOID)))
	nextWant := map[ids.OID][]sqltype.Value{}
	for oid, row := range want {
		nextWant[oid] = row
	}
	nextWant[rename.Address().ObjectID] = statioSysUnitRow(t, input, rename.Address().ObjectID, "pg_catalog", "renamed_system", 0, 0)
	delete(nextWant, moveOut.Address().ObjectID)
	delete(nextWant, relations[2].Address().ObjectID)
	nextWant[moveIn.Address().ObjectID] = statioSysUnitRow(t, input, moveIn.Address().ObjectID, "pg_toasty", moveIn.Name(), 0, 0)
	if !reflect.DeepEqual(fresh, nextWant) || !reflect.DeepEqual(statioSysUnitBag(t, held), want) {
		t.Fatal("current descriptors/immutable prior capture", fresh, nextWant)
	}
}

func TestPGStatioSysSequencesSourceSelection(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		for _, rejected := range []bool{false, true} {
			name := "excluded_user"
			if rejected {
				name = "rejected_system_key"
			}
			t.Run(mode+"/"+name, func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				s.Settings.Mu.Lock()
				before := s.Settings.Value("stats_fetch_consistency")
				s.Settings.Values["stats_fetch_consistency"] = mode
				s.Settings.Typed["stats_fetch_consistency"] = settings.Value{Text: mode}
				s.Settings.StatsChanged(before)
				s.Settings.Mu.Unlock()
				enginetest.TransactionBegin(t, s)
				op := enginetest.TransactionOperation(t, s)
				defer op.Done()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				e.Mu.Lock()
				auth, err := e.AuthorizationLocked(op)
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				state := catalog.CloneState(auth.Catalog.State())
				ns := ids.PublicNamespaceOID
				if rejected {
					ns = ids.PGCatalogNamespaceOID
				}
				d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 32200}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ns}, ObjectName: "reader_sequence", ObjectRevision: 1, Owner: auth.State.CurrentUser, RelationKind: catalog.SequenceRelation, Sequence: &catalog.SequenceOptions{SQLType: sqltype.Int8, Increment: 1, Min: 1, Max: 9223372036854775807, Start: 1, Cache: 1}}
				state.SetDescriptor(d.Address(), d)
				auth.Recapture(e.Catalog.Read(state))
				input := statioSysUnitInput(t, e, auth, pgcatalog.StatioSysSequencesOID)
				if rejected {
					input.Keep = pgcatalog.Keep{Column: 2, Test: func(sqltype.Value) bool { return false }}
				}
				if rows := statioSysUnitRows(t, input); len(rows) != 0 {
					t.Fatal("excluded row", rows)
				}
				ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope)}}
				stamp, err := ctx.StatsSnapshotValue(false, enginetest.Usage(e, sqltype.Timestamptz).Type)
				if err != nil || !stamp.IsNull() {
					t.Fatal("excluded capture initialized reader snapshot", stamp, err)
				}
				key := statistics.Key{Object: e.Catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address())}
				var writer statistics.Writer
				writer.Add(&e.Statistics, key, statistics.Fact{SequencePageAccesses: 2})
				writer.Flush(&e.Statistics, time.Now(), true)
				if rejected {
					input.Keep = pgcatalog.Keep{}
				} else {
					input = statioSysUnitInput(t, e, auth, pgcatalog.StatioAllSequencesOID)
				}
				retained := statioSysUnitRows(t, input)
				schema := "public"
				if rejected {
					schema = "pg_catalog"
				}
				want := [][]sqltype.Value{statioSysUnitRow(t, input, d.Address().ObjectID, schema, d.Name(), 1, 1)}
				if !reflect.DeepEqual(retained, want) {
					t.Fatal("excluded capture poisoned reader cache/snapshot", retained, want)
				}
				writer.Add(&e.Statistics, key, statistics.Fact{SequencePageAccesses: 1})
				writer.Flush(&e.Statistics, time.Now(), true)
				hit := int64(1)
				if mode == "none" {
					hit = 2
				}
				fresh := statioSysUnitRows(t, input)
				if !reflect.DeepEqual(fresh, [][]sqltype.Value{statioSysUnitRow(t, input, d.Address().ObjectID, schema, d.Name(), 1, hit)}) || !reflect.DeepEqual(retained, want) {
					t.Fatal("actual reader policy/immutable cells", fresh, retained)
				}
				if _, err := ctx.StatsSnapshotValue(true, enginetest.Usage(e, sqltype.Void).Type); err != nil {
					t.Fatal(err)
				}
				if rows := statioSysUnitRows(t, input); !reflect.DeepEqual(rows, [][]sqltype.Value{statioSysUnitRow(t, input, d.Address().ObjectID, schema, d.Name(), 1, 2)}) {
					t.Fatal("clear did not recapture shared provider", rows)
				}
				if err := scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
