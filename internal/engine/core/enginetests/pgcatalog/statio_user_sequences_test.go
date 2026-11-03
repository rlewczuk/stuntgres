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

func TestPGStatioUserSequencesClassification(t *testing.T) {
	f := newDefaultUnitFixture(t)
	f.actor(f.reader)
	base := f.e.CatalogRead()
	baseInput := statioSysUnitInput(t, f.e, dtpUnitAuthority(t, f, base), pgcatalog.StatioUserSequencesOID)
	source := baseInput.Source
	if source.Name() != "pg_statio_user_sequences" || source.Namespace().ObjectID != ids.PGCatalogNamespaceOID || source.RowType != 12224 || pgcatalog.ClassKind(base, source) != "v" {
		t.Fatal("named source identity", source)
	}
	row := base.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12224})
	array := base.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12223})
	owned, ok := base.LookupType(12224)
	ownedArray, arrayOK := base.LookupArray(owned)
	if !ok || !arrayOK || row.SQLType != owned || array.SQLType != ownedArray || row.Array != 12223 || array.SQLType.Element() != row.SQLType || row.OwningRelation != source.Address() || array.OwningRelation != source.Address() || row.Owner != source.Owner || array.Owner != source.Owner {
		t.Fatal("reciprocal installed row/array authority", row, array)
	}
	if !source.Security.ACL.Explicit || len(source.Security.ACL.Entries) != 1 || !source.Security.ACL.Entries[0].Grantee.Public || source.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege || source.Security.ACL.Entries[0].Grantor != source.Owner {
		t.Fatal("actual PUBLIC SELECT ACL", source.Security.ACL)
	}
	for i, c := range source.Columns {
		fact := source.ColumnMeta.CatalogAttributes[i]
		if c.Usage.Typmod != -1 || c.Usage.Collation != c.Usage.Type.Usage().Collation || c.DeclaredDimensions != 0 || fact.NotNull || fact.Dimensions != 0 || fact.Collation != c.Usage.Collation.ID {
			t.Fatal("source-owned usage and attribute facts", i, c, fact)
		}
	}
	if rows := statioSysUnitRows(t, baseInput); len(rows) != 0 {
		t.Fatal("fabricated bootstrap user sequence", rows)
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
		{"pg_toast_42", true, 1}, {"pg_toasty", true, 3}, {"PG_TOAST", false, 3},
		{"Pg_Catalog", false, 1}, {"information_schema_extra", false, 0}, {"public", false, 3},
		{"custom", false, 1}, {"pg_temp_3", false, 0},
	}
	relations := make([]*catalog.Relation, len(cases))
	var writer statistics.Writer
	for i, c := range cases {
		ns, ok := namespaces[c.schema]
		if !ok {
			ns = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.OID(33000 + i)}
			d := &catalog.Namespace{ObjectAddress: ns, ObjectName: c.schema, ObjectRevision: 1, Owner: f.owner, ACL: authorization.ACLValue{Explicit: true}}
			state.SetDescriptor(ns, d)
			state.BindName(catalog.DescriptorNameKey(d), ns)
			namespaces[c.schema] = ns
		}
		d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(34000 + i)}, ObjectNamespace: ns, ObjectName: fmt.Sprintf("user_unit_%d", i), ObjectRevision: 1, Owner: f.owner, RelationKind: catalog.SequenceRelation, Sequence: &catalog.SequenceOptions{SQLType: sqltype.Int8, Increment: 1, Min: 1, Max: 9223372036854775807, Start: 1, Cache: 1}, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true}}}
		state.SetDescriptor(d.Address(), d)
		state.BindName(catalog.DescriptorNameKey(d), d.Address())
		relations[i] = d
		if c.n != 0 {
			writer.Add(&f.e.Statistics, statistics.Key{Object: f.e.Catalog.Domain.Ref(base.Epoch(), d.Address())}, statistics.Fact{SequencePageAccesses: c.n})
		}
	}
	for i, kind := range []catalog.RelationKind{catalog.TableRelation, catalog.ViewRelation, catalog.IndexRelation} {
		d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(34100 + i)}, ObjectNamespace: namespaces["custom"], ObjectName: fmt.Sprintf("user_decoy_%d", i), ObjectRevision: 1, Owner: f.owner, RelationKind: kind}
		state.SetDescriptor(d.Address(), d)
	}
	writer.Flush(&f.e.Statistics, time.Now(), true)
	view := f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f, view)
	denied := relations[5]
	if err := auth.Check(access.Requirement{Object: f.e.Catalog.Domain.Ref(view.Epoch(), denied.Address()), Privileges: authorization.SelectPrivilege}); err == nil {
		t.Fatal("fixture sequence ACL unexpectedly admits reader")
	}
	if err := auth.CanUse(f.e.Catalog.Domain.Ref(view.Epoch(), denied.Namespace())); err == nil {
		t.Fatal("fixture namespace ACL unexpectedly admits reader")
	}
	input := statioSysUnitInput(t, f.e, auth, pgcatalog.StatioUserSequencesOID)
	held := statioSysUnitRows(t, input)
	got := statioSysUnitBag(t, held)
	all := statioSysUnitBag(t, statioSysUnitRows(t, statioSysUnitInput(t, f.e, auth, pgcatalog.StatioAllSequencesOID)))
	sys := statioSysUnitBag(t, statioSysUnitRows(t, statioSysUnitInput(t, f.e, auth, pgcatalog.StatioSysSequencesOID)))
	want, wantSys := map[ids.OID][]sqltype.Value{}, map[ids.OID][]sqltype.Value{}
	for i, c := range cases {
		d := relations[i]
		var read, hit int64
		if c.n > 0 {
			read, hit = 1, c.n-1
		}
		expected := statioSysUnitRow(t, input, d.Address().ObjectID, c.schema, d.Name(), read, hit)
		if !reflect.DeepEqual(all[d.Address().ObjectID], expected) {
			t.Fatal("independent all-provider values", c.schema, all[d.Address().ObjectID], expected)
		}
		if c.system {
			wantSys[d.Address().ObjectID] = expected
		} else {
			want[d.Address().ObjectID] = expected
		}
	}
	if len(all) != len(cases) || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(sys, wantSys) {
		t.Fatal("exact disjoint all/user/sys partition and typed counters", all, got, want, sys, wantSys)
	}
	// Successor descriptors, not persistent statistics facts, determine current
	// names and namespace membership. Earlier rows retain their owned cells.
	changed := catalog.CloneState(state)
	rename := *relations[5]
	rename.ObjectName, rename.ObjectRevision = "renamed_user", rename.Revision()+1
	changed.UnbindName(catalog.DescriptorNameKey(relations[5]))
	changed.SetDescriptor(rename.Address(), &rename)
	changed.BindName(catalog.DescriptorNameKey(&rename), rename.Address())
	moveOut := *relations[6]
	moveOut.ObjectNamespace, moveOut.ObjectRevision = namespaces["pg_toasty"], moveOut.Revision()+1
	changed.UnbindName(catalog.DescriptorNameKey(relations[6]))
	changed.SetDescriptor(moveOut.Address(), &moveOut)
	changed.BindName(catalog.DescriptorNameKey(&moveOut), moveOut.Address())
	moveIn := *relations[2]
	moveIn.ObjectNamespace, moveIn.ObjectRevision = namespaces["custom"], moveIn.Revision()+1
	changed.UnbindName(catalog.DescriptorNameKey(relations[2]))
	changed.SetDescriptor(moveIn.Address(), &moveIn)
	changed.BindName(catalog.DescriptorNameKey(&moveIn), moveIn.Address())
	changed.UnbindName(catalog.DescriptorNameKey(relations[8]))
	changed.RemoveDescriptor(relations[8].Address()) // Leave its N=3 statistics fact.
	auth.Recapture(f.e.Catalog.Read(changed))
	fresh := statioSysUnitBag(t, statioSysUnitRows(t, statioSysUnitInput(t, f.e, auth, pgcatalog.StatioUserSequencesOID)))
	nextWant := map[ids.OID][]sqltype.Value{}
	for oid, row := range want {
		nextWant[oid] = row
	}
	nextWant[rename.Address().ObjectID] = statioSysUnitRow(t, input, rename.Address().ObjectID, "PG_TOAST", rename.Name(), 1, 2)
	delete(nextWant, moveOut.Address().ObjectID)
	delete(nextWant, relations[8].Address().ObjectID)
	nextWant[moveIn.Address().ObjectID] = statioSysUnitRow(t, input, moveIn.Address().ObjectID, "custom", moveIn.Name(), 1, 2)
	if !reflect.DeepEqual(fresh, nextWant) || !reflect.DeepEqual(statioSysUnitBag(t, held), want) {
		t.Fatal("current descriptor authority and immutable prior capture", fresh, nextWant)
	}
}

func TestPGStatioUserSequencesSourceSelection(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		for _, selection := range []string{"excluded_system", "rejected_relid", "rejected_relname"} {
			t.Run(mode+"/"+selection, func(t *testing.T) {
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
				defer func() {
					if err := scope.Finish(nil); err != nil {
						t.Error(err)
					}
				}()
				e.Mu.Lock()
				auth, err := e.AuthorizationLocked(op)
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				state := catalog.CloneState(auth.Catalog.State())
				ns, schema := ids.PublicNamespaceOID, "public"
				if selection == "excluded_system" {
					ns, schema = ids.PGCatalogNamespaceOID, "pg_catalog"
				}
				d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 34200}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ns}, ObjectName: "user_reader_sequence", ObjectRevision: 1, Owner: auth.State.CurrentUser, RelationKind: catalog.SequenceRelation, Sequence: &catalog.SequenceOptions{SQLType: sqltype.Int8, Increment: 1, Min: 1, Max: 9223372036854775807, Start: 1, Cache: 1}}
				state.SetDescriptor(d.Address(), d)
				auth.Recapture(e.Catalog.Read(state))
				input := statioSysUnitInput(t, e, auth, pgcatalog.StatioUserSequencesOID)
				if selection != "excluded_system" {
					column := 0
					if selection == "rejected_relname" {
						column = 2
					}
					input.Keep = pgcatalog.Keep{Column: column, Test: func(sqltype.Value) bool { return false }}
				}
				if rows := statioSysUnitRows(t, input); len(rows) != 0 {
					t.Fatal("excluded/rejected user capture", rows)
				}
				ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope)}}
				stamp, err := ctx.StatsSnapshotValue(false, enginetest.Usage(e, sqltype.Timestamptz).Type)
				if err != nil || !stamp.IsNull() {
					t.Fatal("excluded capture initialized shared snapshot", stamp, err)
				}
				key := statistics.Key{Object: e.Catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address())}
				var writer statistics.Writer
				writer.Add(&e.Statistics, key, statistics.Fact{SequencePageAccesses: 2})
				writer.Flush(&e.Statistics, time.Now(), true)
				if selection == "excluded_system" {
					input = statioSysUnitInput(t, e, auth, pgcatalog.StatioAllSequencesOID)
				} else {
					input.Keep = pgcatalog.Keep{}
				}
				retained := statioSysUnitRows(t, input)
				want := [][]sqltype.Value{statioSysUnitRow(t, input, d.Address().ObjectID, schema, d.Name(), 1, 1)}
				if !reflect.DeepEqual(retained, want) {
					t.Fatal("excluded capture seeded a cached zero", retained, want)
				}
				writer.Add(&e.Statistics, key, statistics.Fact{SequencePageAccesses: 1})
				writer.Flush(&e.Statistics, time.Now(), true)
				hit := int64(1)
				if mode == "none" {
					hit = 2
				}
				fresh := statioSysUnitRows(t, input)
				if !reflect.DeepEqual(fresh, [][]sqltype.Value{statioSysUnitRow(t, input, d.Address().ObjectID, schema, d.Name(), 1, hit)}) || !reflect.DeepEqual(retained, want) {
					t.Fatal("real reader policy and retained ownership", fresh, retained)
				}
				if _, err := ctx.StatsSnapshotValue(true, enginetest.Usage(e, sqltype.Void).Type); err != nil {
					t.Fatal(err)
				}
				if rows := statioSysUnitRows(t, input); !reflect.DeepEqual(rows, [][]sqltype.Value{statioSysUnitRow(t, input, d.Address().ObjectID, schema, d.Name(), 1, 2)}) {
					t.Fatal("clear did not recapture shared statistics", rows)
				}
			})
		}
	}
}
