package core_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// This fixture admits a standalone definition through the real catalog owner;
// no attached constraint is manufactured to smuggle a predicate into UNIQUE.
func newPartialUniquePublication(t *testing.T, composite, nnd bool, disclosure string) *uniquePublicationFixture {
	t.Helper()
	e := engine.New()
	admin, ad := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(ad)
	enginetest.RoleExecute(t, e, admin, "CREATE ROLE ui_owner LOGIN")
	owner, od := enginetest.RoleOperation(t, e, "ui_owner")
	t.Cleanup(od)
	keys, count := "a", 1
	withPublicSchemaUnitOwner(t, e, "ui_owner", func() {
		enginetest.RoleExecute(t, e, owner, "CREATE TABLE public.ui_t(a DATE,b INT,p TEXT,active BOOL)")
		if composite {
			keys, count = "a,b", 2
		}
		policy := ""
		if nnd {
			policy = " NULLS NOT DISTINCT"
		}
		enginetest.RoleExecute(t, e, owner, "CREATE UNIQUE INDEX ui_index ON public.ui_t("+keys+") INCLUDE(p)"+policy+" WHERE active")
		enginetest.RoleExecute(t, e, owner, "INSERT INTO public.ui_t VALUES('2001-02-03',1,'old',TRUE),('2001-02-04',2,'other',TRUE),('2001-02-03',1,'outside',FALSE),('2001-02-03',1,'unknown',NULL)")
		enginetest.RoleExecute(t, e, owner, "CREATE TABLE ui_sibling(k INT)")
		enginetest.RoleExecute(t, e, owner, "INSERT INTO ui_sibling VALUES(9)")
		enginetest.RoleExecute(t, e, owner, "CREATE INDEX ui_sibling_index ON ui_sibling(k)")
	})
	ref := roleTableRef(t, e, "public.ui_t")
	actor, ok := e.Roles().Lookup("ui_owner")
	if !ok || actor.Superuser {
		t.Fatal("requires nonsuperuser")
	}
	grant := func(attr int32, rights authorization.Privileges) {
		r := ref
		r.Address.SubID = attr
		err := enginetest.SetObjectACL(e, context.Background(), r, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: actor.Ref, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: rights}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	rights := authorization.InsertPrivilege
	if disclosure == "table" {
		rights |= authorization.SelectPrivilege
	}
	grant(0, rights)
	if disclosure == "partial" || disclosure == "columns" {
		grant(1, authorization.SelectPrivilege)
	}
	if disclosure == "columns" {
		grant(2, authorization.SelectPrivilege)
	}
	auth, err := core.OperationAuthorization(owner)
	if err != nil {
		t.Fatal(err)
	}
	parent := e.Catalog.State.MustRelation(ref.Address)
	idx, root := btreeRoot(t, e, "public.ui_index")
	if idx.SQLIndex.Constraint != nil || len(parent.Constraints.Unique) != 0 || idx.SQLIndex.Predicate == nil {
		t.Fatal("not real standalone partial")
	}
	if err := index.ValidateIndexDefinition(e.CatalogRead(), idx); err != nil {
		t.Fatal(err)
	}
	if len(root.Entries) != 2 || !maps.Equal(root.Membership, map[ids.RowID]ids.VersionID{e.Storage.Relations[ref.Address].Versions[0].Row: e.Storage.Relations[ref.Address].Versions[0].Version, e.Storage.Relations[ref.Address].Versions[1].Row: e.Storage.Relations[ref.Address].Versions[1].Version}) {
		t.Fatal("seed membership", root)
	}
	f := &uniquePublicationFixture{e: e, parent: parent, index: idx, ref: ref, auth: auth, keys: count, catalog: e.Catalog.State, descriptors: maps.Collect(e.Catalog.State.Descriptors()), names: maps.Collect(e.Catalog.State.Names()), roles: e.RoleStore.Published, stores: maps.Clone(e.Storage.Relations), storeSnapshots: make(map[ids.ObjectAddress]storage.Relation), roots: maps.Clone(e.Storage.IndexRoots), entries: make(map[ids.ObjectAddress][]storage.IndexEntry), nextRow: e.Storage.RowFrontier(), nextVersion: e.Storage.VersionFrontier(), frontier: e.Visibility.CommitSeq()}
	for a, s := range f.stores {
		f.storeSnapshots[a] = uniquePublicationStore(s)
	}
	for a, r := range f.roots {
		entries := slices.Clone(r.Entries)
		for i := range entries {
			entries[i].Keys = slices.Clone(entries[i].Keys)
		}
		f.entries[a] = entries
	}
	return f
}

func partialUniqueRow(t *testing.T, f *uniquePublicationFixture, offset int, a, b, active string) storage.RowVersion {
	t.Helper()
	r := f.row(t, offset, a, b)
	typ := f.parent.Columns[3].Usage.Type
	v := sqltype.Null(typ)
	if active != "" {
		var err error
		v, err = typ.Input(context.Background(), active, -1)
		if err != nil {
			t.Fatal(err)
		}
	}
	r.Values = append(r.Values, v)
	return r
}

func partialUniqueMembership(t *testing.T, f *uniquePublicationFixture, roots map[ids.ObjectAddress]*storage.IndexRoot, materialize bool, rows []storage.RowVersion) {
	t.Helper()
	f.membership(t, roots, materialize, rows)
	if !materialize {
		return
	}
	want := make(map[ids.RowID]ids.VersionID, len(rows))
	for _, r := range rows {
		want[r.Row] = r.Version
	}
	got := roots[f.index.Address()].Membership
	if got == nil || !maps.Equal(got, want) {
		t.Fatal("exact partial membership", got, want)
	}
	for a, r := range f.roots {
		wantOld := make(map[ids.RowID]ids.VersionID)
		for _, entry := range f.entries[a] {
			wantOld[entry.Row] = entry.Version
		}
		if r.Membership != nil && !maps.Equal(r.Membership, wantOld) {
			t.Fatal("retained membership mutated")
		}
	}
}

func TestPartialIndexUniquePublicationCombined(t *testing.T) {
	for _, materialize := range []bool{false, true} {
		for _, rebuild := range []bool{false, true} {
			for _, mode := range []string{"leave", "enter", "delete", "empty"} {
				t.Run(fmt.Sprintf("materialize=%t/rebuild=%t/%s", materialize, rebuild, mode), func(t *testing.T) {
					f := newPartialUniquePublication(t, true, true, "table")
					rows := f.stores[f.parent.Address()].Versions
					replacement := partialUniqueRow(t, f, 1, "2001-02-05", "3", "false")
					replacement.Row, replacement.Base = rows[0].Row, rows[0].Version
					var deletion *visibility.PrivateDeletes
					writes := []storage.RowVersion{replacement}
					want := []storage.RowVersion{rows[1]}
					if mode == "enter" {
						replacement = partialUniqueRow(t, f, 1, "2001-02-05", "3", "true")
						replacement.Row, replacement.Base = rows[2].Row, rows[2].Version
						writes = []storage.RowVersion{replacement}
						want = []storage.RowVersion{rows[0], rows[1], replacement}
					}
					if mode == "delete" || mode == "empty" {
						deletion = &visibility.PrivateDeletes{Rows: map[ids.RowID]visibility.DeleteCandidate{rows[1].Row: {Ref: f.ref, Storage: f.stores[f.parent.Address()], Row: rows[1].Row, Version: rows[1].Version}}}
						want = nil
						if mode == "delete" {
							member := partialUniqueRow(t, f, 2, "2001-02-06", "4", "true")
							writes = append(writes, member)
							want = []storage.RowVersion{member}
						}
					}
					roots, err := f.prepare(t, context.Background(), nil, rebuild, materialize, writes, deletion)
					if err != nil {
						t.Fatal(err)
					}
					partialUniqueMembership(t, f, roots, materialize, want)
				})
			}
		}
	}
}

func TestPartialIndexUniquePublicationRebuiltBeforeDelta(t *testing.T) {
	for _, materialize := range []bool{false, true} {
		for _, outside := range []bool{false, true} {
			for _, delta := range []string{"delete", "replace"} {
				t.Run(fmt.Sprintf("materialize=%t/outside=%t/%s", materialize, outside, delta), func(t *testing.T) {
					f := newPartialUniquePublication(t, true, false, "table")
					store := uniquePublicationStore(f.stores[f.parent.Address()])
					store.Versions[1].Values = slices.Clone(store.Versions[0].Values)
					if outside {
						store.Versions[1].Values[3] = store.Versions[2].Values[3]
					}
					old := store.Versions[1]
					var writes []storage.RowVersion
					var deletion *visibility.PrivateDeletes
					if delta == "delete" {
						deletion = &visibility.PrivateDeletes{Rows: map[ids.RowID]visibility.DeleteCandidate{old.Row: {Ref: f.ref, Storage: &store, Row: old.Row, Version: old.Version}}}
					} else {
						r := partialUniqueRow(t, f, 1, "2001-02-05", "3", "true")
						r.Row, r.Base = old.Row, old.Version
						writes = []storage.RowVersion{r}
					}
					roots, err := f.prepare(t, context.Background(), &store, true, materialize, writes, deletion)
					if !outside {
						f.duplicate(t, roots, err, true, "Key (a, b)=(03.02.2001, 1) is duplicated.")
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					want := append([]storage.RowVersion{store.Versions[0]}, writes...)
					partialUniqueMembership(t, f, roots, materialize, want)
				})
			}
		}
	}
}

func TestPartialIndexUniquePublicationAdmissionMatrix(t *testing.T) {
	for _, composite := range []bool{false, true} {
		for _, nnd := range []bool{false, true} {
			for _, materialize := range []bool{false, true} {
				for _, rebuild := range []bool{false, true} {
					for _, tc := range []struct {
						name, a, b, secondB, active string
						survivor                    bool
					}{
						{"outside_survivor", "2001-02-03", "1", "1", "false", true}, {"unknown_survivor", "2001-02-03", "1", "1", "", true}, {"member_survivor", "2001-02-03", "1", "1", "true", true}, {"member_pair", "2001-02-05", "3", "3", "true", false}, {"outside_pair", "2001-02-05", "3", "3", "false", false}, {"different_component", "2001-02-05", "3", "4", "true", false}, {"null_pair", "", "1", "1", "true", false}, {"null_different", "", "1", "2", "true", false},
						{"null_second", "2001-02-05", "", "", "true", false}, {"null_both", "", "", "", "true", false}, {"outside_null", "", "1", "1", "false", false},
					} {
						t.Run(fmt.Sprintf("composite=%t/nnd=%t/materialize=%t/rebuild=%t/%s", composite, nnd, materialize, rebuild, tc.name), func(t *testing.T) {
							f := newPartialUniquePublication(t, composite, nnd, "table")
							writes := []storage.RowVersion{partialUniqueRow(t, f, 1, tc.a, tc.b, tc.active)}
							if !tc.survivor {
								writes = append(writes, partialUniqueRow(t, f, 2, tc.a, tc.secondB, tc.active))
							}
							duplicate := tc.active == "true" && (tc.survivor || tc.b == tc.secondB || !composite) && ((tc.a != "" && (!composite || tc.b != "")) || nnd)
							roots, err := f.prepare(t, context.Background(), nil, rebuild, materialize, writes, nil)
							if duplicate {
								f.duplicate(t, roots, err, false, uniquePublicationKey(composite, tc.a, tc.b)+" already exists.")
								return
							}
							if err != nil {
								t.Fatal(err)
							}
							want := uniquePublicationRows(f.stores[f.parent.Address()].Versions[:2])
							if tc.active == "true" {
								want = append(want, writes...)
							}
							partialUniqueMembership(t, f, roots, materialize, want)
						})
					}
				}
			}
		}
	}
}

func TestPartialIndexUniquePublicationDisclosureCancellation(t *testing.T) {
	for _, materialize := range []bool{false, true} {
		for _, mode := range []string{"none", "partial", "columns", "table"} {
			for _, building := range []bool{false, true} {
				t.Run(fmt.Sprintf("materialize=%t/%s/build=%t", materialize, mode, building), func(t *testing.T) {
					f := newPartialUniquePublication(t, true, false, mode)
					store := uniquePublicationStore(f.stores[f.parent.Address()])
					var writes []storage.RowVersion
					if building {
						store.Versions[1].Values = slices.Clone(store.Versions[0].Values)
					} else {
						writes = []storage.RowVersion{partialUniqueRow(t, f, 1, "2001-02-03", "1", "true")}
					}
					roots, err := f.prepare(t, context.Background(), &store, building, materialize, writes, nil)
					detail := ""
					if building {
						detail = "Duplicate keys exist."
					}
					if mode == "columns" || mode == "table" {
						detail = "Key (a, b)=(03.02.2001, 1) already exists."
						if building {
							detail = "Key (a, b)=(03.02.2001, 1) is duplicated."
						}
					}
					f.duplicate(t, roots, err, building, detail)
					ctx, cancel := context.WithCancel(context.Background())
					cancel()
					roots, err = f.prepare(t, ctx, nil, building, materialize, []storage.RowVersion{partialUniqueRow(t, f, 1, "2001-02-05", "3", "true")}, nil)
					var diagnostic *sqlstate.Error
					if roots != nil || !errors.Is(err, context.Canceled) || errors.As(err, &diagnostic) {
						t.Fatal("cancellation", roots, err)
					}
				})
			}
		}
	}
}

func TestPartialIndexUniquePredicateIdentityFence(t *testing.T) {
	f := newPartialUniquePublication(t, true, false, "table")
	same := *f.index
	same.ObjectName = "renamed"
	if !index.SameIndexKeySemantics(f.index, &same) {
		t.Fatal("retained predicate rename lost reuse")
	}
	next := *f.index
	definition := *f.index.SQLIndex
	predicate := *definition.Predicate
	definition.Predicate = &predicate
	next.SQLIndex = &definition
	if index.SameIndexKeySemantics(f.index, &next) {
		t.Fatal("new predicate identity passed root/admission reuse fence")
	}
	// Independently verify that the retained expression and root were untouched.
	if !reflect.DeepEqual(f.index.SQLIndex.Predicate, &predicate) {
		t.Fatal("predicate mutated")
	}
	f.unchanged(t)
}

func TestPartialIndexUniquePublicationRebuildOnlyIdentity(t *testing.T) {
	for _, materialize := range []bool{false, true} {
		t.Run(fmt.Sprint(materialize), func(t *testing.T) {
			f := newPartialUniquePublication(t, true, false, "table")
			next := *f.catalog
			idx := *f.index
			definition := *idx.SQLIndex
			predicate := *definition.Predicate
			definition.Predicate = &predicate
			idx.SQLIndex = &definition
			next.SetDescriptor(idx.Address(), &idx)
			roots, err := core.EnginePrepareIndexRoots(f.e, &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.catalog, &next, index.CaptureIndexStores(f.e.Storage.Relations, f.catalog), f.roots, nil, nil, nil, materialize, sqltype.OutputEnvironment{}, &f.auth, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			partialUniqueMembership(t, f, roots, materialize, f.stores[f.parent.Address()].Versions[:2])
			f.unchanged(t)
		})
	}
}
