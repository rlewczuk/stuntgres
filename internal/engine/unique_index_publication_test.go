package engine

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

type uniquePublicationFixture struct {
	e              *Engine
	parent, index  *catalog.Relation
	ref            ids.ObjectRef
	auth           AuthorizationView
	keys           int
	catalog        *catalog.State
	descriptors    map[ids.ObjectAddress]catalog.Descriptor
	names          map[ids.NameKey]ids.ObjectAddress
	roles          *access.RoleRecord
	stores         map[ids.ObjectAddress]*storage.Relation
	storeSnapshots map[ids.ObjectAddress]storage.Relation
	roots          map[ids.ObjectAddress]*storage.IndexRoot
	entries        map[ids.ObjectAddress][]storage.IndexEntry
	nextRow        uint64
	nextVersion    uint64
	frontier       ids.CommitSeq
}

func uniquePublicationRows(rows []storage.RowVersion) []storage.RowVersion {
	copy := slices.Clone(rows)
	for i := range copy {
		copy[i].Values = slices.Clone(copy[i].Values)
	}
	return copy
}

func uniquePublicationStore(store *storage.Relation) storage.Relation {
	copy := *store
	copy.Versions = uniquePublicationRows(store.Versions)
	if store.Ends != nil {
		copy.Ends = &storage.DeletionRoot{Rows: maps.Clone(store.Ends.Rows)}
	}
	return copy
}

func newUniquePublicationFixture(t *testing.T, attached, composite, nnd bool, disclosure string, nullSurvivor bool) *uniquePublicationFixture {
	t.Helper()
	e := New()
	admin, adminDone := roleOperation(t, e, "postgres")
	t.Cleanup(adminDone)
	roleExecute(t, e, admin, "CREATE ROLE ui_owner LOGIN")
	owner, ownerDone := roleOperation(t, e, "ui_owner")
	t.Cleanup(ownerDone)
	keys, count := "a", 1
	if composite {
		keys, count = "a,b", 2
	}
	policy := ""
	if nnd {
		policy = " NULLS NOT DISTINCT"
	}
	table := "CREATE TABLE public.ui_t(a DATE,b INT,p TEXT"
	if attached {
		table += ",CONSTRAINT ui_index UNIQUE" + policy + "(" + keys + ")"
	}
	withPublicSchemaUnitOwner(t, e, "ui_owner", func() {
		roleExecute(t, e, owner, table+")")
		if !attached {
			roleExecute(t, e, owner, "CREATE UNIQUE INDEX ui_index ON public.ui_t("+keys+")"+policy)
		}
		seed := "INSERT INTO public.ui_t VALUES('2001-02-03',1,'old'),('2001-02-04',2,'other')"
		if nullSurvivor {
			seed += ",(NULL,1,'null old')"
		}
		roleExecute(t, e, owner, seed)
		roleExecute(t, e, owner, "CREATE TABLE public.ui_sibling(k INT)")
		roleExecute(t, e, owner, "INSERT INTO public.ui_sibling VALUES(9)")
		roleExecute(t, e, owner, "CREATE INDEX ui_sibling_index ON public.ui_sibling(k)")
	})
	ref := roleTableRef(t, e, "public.ui_t")
	actor, ok := e.Roles().Lookup("ui_owner")
	if !ok || actor.Superuser {
		t.Fatal("disclosure requires actual nonsuperuser", actor, ok)
	}
	grant := func(attribute int32, rights authorization.Privileges) {
		target := ref
		target.Address.SubID = attribute
		acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: actor.Ref, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: rights}}}
		if err := setObjectACL(e, context.Background(), target, acl); err != nil {
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
	if disclosure == "columns" && composite {
		grant(2, authorization.SelectPrivilege)
	}
	auth, err := OperationAuthorization(owner)
	if err != nil {
		t.Fatal(err)
	}
	parent := e.Catalog.State.MustRelation(ref.Address)
	idx, _ := btreeRoot(t, e, "public.ui_index")
	if !idx.SQLIndex.Unique || idx.SQLIndex.NullsNotDistinct != nnd || len(idx.SQLIndex.Keys) != count {
		t.Fatal("invalid unique fixture")
	}
	if attached {
		if idx.SQLIndex.Constraint == nil || len(parent.Constraints.Unique) != 1 || parent.Constraints.Unique[0].Index != idx.Address() {
			t.Fatal("attachment must be genuine")
		}
	} else if idx.SQLIndex.Constraint != nil || len(parent.Constraints.Unique) != 0 {
		t.Fatal("standalone fixture fabricated attachment")
	}
	if err := index.ValidateIndexDefinition(e.CatalogRead(), idx); err != nil {
		t.Fatal(err)
	}
	f := &uniquePublicationFixture{e: e, parent: parent, index: idx, ref: ref, auth: auth, keys: count, catalog: e.Catalog.State, descriptors: maps.Collect(e.Catalog.State.Descriptors()), names: maps.Collect(e.Catalog.State.Names()), roles: e.RoleStore.Published, stores: maps.Clone(e.Storage.Relations), storeSnapshots: make(map[ids.ObjectAddress]storage.Relation), roots: maps.Clone(e.Storage.IndexRoots), entries: make(map[ids.ObjectAddress][]storage.IndexEntry), nextRow: e.Storage.RowFrontier(), nextVersion: e.Storage.VersionFrontier(), frontier: e.Visibility.CommitSeq()}
	for address, store := range f.stores {
		f.storeSnapshots[address] = uniquePublicationStore(store)
	}
	for address, root := range f.roots {
		entries := slices.Clone(root.Entries)
		for i := range entries {
			entries[i].Keys = slices.Clone(entries[i].Keys)
		}
		f.entries[address] = entries
	}
	return f
}

func (f *uniquePublicationFixture) row(t *testing.T, offset int, a, b string) storage.RowVersion {
	t.Helper()
	values := make([]sqltype.Value, 3)
	for i, text := range []string{a, b} {
		typ := f.parent.Columns[i].Usage.Type
		values[i] = sqltype.Null(typ)
		if text != "" {
			var err error
			values[i], err = typ.Input(context.Background(), text, -1)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	values[2] = sqltype.String(f.parent.Columns[2].Usage.Type, fmt.Sprintf("new%d", offset))
	return storage.RowVersion{Row: ids.RowID(f.nextRow + uint64(offset)), Version: ids.VersionID(f.nextVersion + uint64(offset)), Values: values}
}

func (f *uniquePublicationFixture) unchanged(t *testing.T) {
	t.Helper()
	if f.e.Catalog.State != f.catalog || f.e.RoleStore.Published != f.roles || f.e.Storage.RowFrontier() != f.nextRow || f.e.Storage.VersionFrontier() != f.nextVersion || f.e.Visibility.CommitSeq() != f.frontier || len(f.e.Storage.IndexRoots) != len(f.roots) || len(f.e.Storage.Relations) != len(f.stores) {
		t.Fatal("package preparation changed live publication state")
	}
	if !maps.Equal(maps.Collect(f.catalog.Descriptors()), f.descriptors) || !maps.Equal(maps.Collect(f.catalog.Names()), f.names) {
		t.Fatal("retained catalog membership mutated")
	}
	for address, root := range f.roots {
		if f.e.Storage.IndexRoots[address] != root || !reflect.DeepEqual(root.Entries, f.entries[address]) {
			t.Fatal("retained root mutated", address)
		}
	}
	for address, store := range f.stores {
		if f.e.Storage.Relations[address] != store || !reflect.DeepEqual(*store, f.storeSnapshots[address]) {
			t.Fatal("retained store mutated", address)
		}
	}
}

func (f *uniquePublicationFixture) prepare(t *testing.T, ctx context.Context, store *storage.Relation, rebuild, materialize bool, writes []storage.RowVersion, deletion *visibility.PrivateDeletes) (map[ids.ObjectAddress]*storage.IndexRoot, error) {
	t.Helper()
	if store == nil {
		store = f.stores[f.parent.Address()]
	}
	beforeStore := uniquePublicationStore(store)
	beforeWrites := uniquePublicationRows(writes)
	var beforeDeletes map[ids.RowID]visibility.DeleteCandidate
	if deletion != nil {
		beforeDeletes = maps.Clone(deletion.Rows)
	}
	stores := index.CaptureIndexStores(f.e.Storage.Relations, f.catalog)
	stores[f.parent.Address()] = store
	parents := map[ids.ObjectAddress]bool{f.parent.Address(): rebuild}
	publication := index.RowPublication{Address: f.parent.Address(), Relation: f.parent, OriginalRelation: f.parent, Original: f.stores[f.parent.Address()], Store: store, Root: store.Versions, Ends: store.Ends, Generation: store.Generation, Writes: writes, Deletion: deletion, SingleSlot: -1}
	if materialize {
		if err := index.PrepareRowPublication(&publication, f.e.Catalog.Domain, f.e.Catalog.Domain.Tx(ids.SessionToken{}, 0, f.catalog.Epoch(), nil, 0), true); err != nil {
			t.Fatal(err)
		}
	}
	output := sqltype.OutputEnvironment{DateStyle: sqltype.DateStyle{Style: sqltype.DateGerman, Order: sqltype.DateDMY}}
	roots, err := EnginePrepareIndexRoots(f.e, &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.catalog, f.catalog, stores, f.roots, parents, nil, []index.RowPublication{publication}, materialize, output, &f.auth, nil, nil)
	if !reflect.DeepEqual(*store, beforeStore) || !reflect.DeepEqual(writes, beforeWrites) || deletion != nil && !reflect.DeepEqual(deletion.Rows, beforeDeletes) {
		t.Fatal("publication inputs mutated")
	}
	f.unchanged(t)
	return roots, err
}

func (f *uniquePublicationFixture) duplicate(t *testing.T, roots map[ids.ObjectAddress]*storage.IndexRoot, err error, building bool, detail string) {
	t.Helper()
	if roots != nil {
		t.Fatal("failed preparation returned installation roots")
	}
	message := `duplicate key value violates unique constraint "ui_index"`
	if building {
		message = `could not create unique index "ui_index"`
	}
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: message, Detail: detail, Schema: "public", Table: "ui_t", Constraint: "ui_index"})
}

func (f *uniquePublicationFixture) membership(t *testing.T, roots map[ids.ObjectAddress]*storage.IndexRoot, materialize bool, rows []storage.RowVersion) {
	t.Helper()
	if !materialize {
		if roots != nil {
			t.Fatal("validation-only success returned installation map")
		}
		return
	}
	if len(roots) != len(f.roots) {
		t.Fatal("completion omitted index roots", len(roots), len(f.roots))
	}
	root := roots[f.index.Address()]
	if root == nil {
		t.Fatal("completion omitted substantive root")
	}
	want := make([]storage.IndexEntry, len(rows))
	nextPosition := len(f.stores[f.parent.Address()].Versions)
	for i, row := range rows {
		_, position, found := storage.RowByID(f.stores[f.parent.Address()].Versions, row.Row, -1)
		if !found {
			position = nextPosition
			nextPosition++
		}
		want[i] = storage.IndexEntry{Row: row.Row, Version: row.Version, Position: position, Keys: slices.Clone(row.Values[:f.keys])}
	}
	if !reflect.DeepEqual(root.Entries, want) {
		t.Fatal("wrong completion membership", root.Entries, want)
	}
	for address, old := range f.roots {
		if address != f.index.Address() && roots[address] != old {
			t.Fatal("unchanged sibling root not retained", address)
		}
	}
}

func uniquePublicationKey(composite bool, a, b string) string {
	// Independent German DATE spelling; do not ask the provider under test to
	// compute the expected presentation.
	date := "null"
	if a != "" {
		date = a[8:10] + "." + a[5:7] + "." + a[:4]
	}
	if !composite {
		return "Key (a)=(" + date + ")"
	}
	if b == "" {
		b = "null"
	}
	return "Key (a, b)=(" + date + ", " + b + ")"
}

func TestUniqueIndexPublicationRebuiltPredecessor(t *testing.T) {
	for _, attached := range []bool{false, true} {
		for _, materialize := range []bool{false, true} {
			for _, delta := range []string{"delete", "replace"} {
				t.Run(fmt.Sprintf("attached=%t/materialize=%t/%s", attached, materialize, delta), func(t *testing.T) {
					f := newUniquePublicationFixture(t, attached, true, false, "table", false)
					store := *f.stores[f.parent.Address()]
					store.Versions = uniquePublicationRows(store.Versions)
					// Real valid definitions, with a deliberately duplicate build
					// input. The separate delta could hide this if applied first.
					store.Versions[1].Values = slices.Clone(store.Versions[0].Values)
					old := store.Versions[1]
					var writes []storage.RowVersion
					var deletion *visibility.PrivateDeletes
					if delta == "delete" {
						deletion = &visibility.PrivateDeletes{Rows: map[ids.RowID]visibility.DeleteCandidate{old.Row: {Ref: f.ref, Storage: &store, Row: old.Row, Version: old.Version}}}
					} else {
						replacement := f.row(t, 1, "2001-02-05", "3")
						replacement.Row, replacement.Base = old.Row, old.Version
						writes = []storage.RowVersion{replacement}
					}
					roots, err := f.prepare(t, context.Background(), &store, true, materialize, writes, deletion)
					f.duplicate(t, roots, err, true, "Key (a, b)=(03.02.2001, 1) is duplicated.")
				})
			}
		}
	}
}

func TestUniqueIndexPublicationStreaming(t *testing.T) {
	for _, attached := range []bool{false, true} {
		for _, composite := range []bool{false, true} {
			for _, nnd := range []bool{false, true} {
				for _, materialize := range []bool{false, true} {
					for _, tc := range []struct {
						name, a, b string
						secondB    string
						duplicate  bool
					}{
						{"survivor_addition", "2001-02-03", "1", "", true},
						{"addition_addition", "2001-02-05", "3", "3", true},
						{"different_component", "2001-02-05", "3", "4", !composite},
						{"null_first", "", "1", "1", nnd},
						{"null_second", "2001-02-05", "", "", nnd || !composite},
						{"null_both", "", "", "", nnd},
						{"null_different_component", "", "1", "2", nnd && !composite},
					} {
						t.Run(fmt.Sprintf("attached=%t/composite=%t/nnd=%t/materialize=%t/%s", attached, composite, nnd, materialize, tc.name), func(t *testing.T) {
							f := newUniquePublicationFixture(t, attached, composite, nnd, "table", false)
							writes := []storage.RowVersion{f.row(t, 1, tc.a, tc.b)}
							if tc.name != "survivor_addition" {
								writes = append(writes, f.row(t, 2, tc.a, tc.secondB))
							}
							roots, err := f.prepare(t, context.Background(), nil, false, materialize, writes, nil)
							if tc.duplicate {
								f.duplicate(t, roots, err, false, uniquePublicationKey(composite, tc.a, tc.b)+" already exists.")
							} else {
								if err != nil {
									t.Fatal(err)
								}
								want := append(uniquePublicationRows(f.stores[f.parent.Address()].Versions), writes...)
								f.membership(t, roots, materialize, want)
							}
						})
					}
				}
			}
		}
	}
}

func TestUniqueIndexPublicationNullSurvivor(t *testing.T) {
	for _, attached := range []bool{false, true} {
		for _, composite := range []bool{false, true} {
			for _, nnd := range []bool{false, true} {
				for _, materialize := range []bool{false, true} {
					t.Run(fmt.Sprintf("attached=%t/composite=%t/nnd=%t/materialize=%t", attached, composite, nnd, materialize), func(t *testing.T) {
						f := newUniquePublicationFixture(t, attached, composite, nnd, "table", true)
						writes := []storage.RowVersion{f.row(t, 1, "", "1")}
						roots, err := f.prepare(t, context.Background(), nil, false, materialize, writes, nil)
						if nnd {
							f.duplicate(t, roots, err, false, uniquePublicationKey(composite, "", "1")+" already exists.")
						} else {
							if err != nil {
								t.Fatal(err)
							}
							want := append(uniquePublicationRows(f.stores[f.parent.Address()].Versions), writes...)
							f.membership(t, roots, materialize, want)
						}
					})
				}
			}
		}
	}
}

func TestUniqueIndexPublicationReplacementMembership(t *testing.T) {
	for _, attached := range []bool{false, true} {
		for _, materialize := range []bool{false, true} {
			t.Run(fmt.Sprintf("attached=%t/materialize=%t", attached, materialize), func(t *testing.T) {
				f := newUniquePublicationFixture(t, attached, true, true, "table", false)
				rows := f.stores[f.parent.Address()].Versions
				deletion := &visibility.PrivateDeletes{Rows: map[ids.RowID]visibility.DeleteCandidate{rows[0].Row: {Ref: f.ref, Storage: f.stores[f.parent.Address()], Row: rows[0].Row, Version: rows[0].Version}}}
				replacement := f.row(t, 1, "2001-02-05", "3")
				replacement.Row, replacement.Base = rows[1].Row, rows[1].Version
				writes := []storage.RowVersion{replacement, f.row(t, 2, "2001-02-06", "4")}
				roots, err := f.prepare(t, context.Background(), nil, false, materialize, writes, deletion)
				if err != nil {
					t.Fatal(err)
				}
				f.membership(t, roots, materialize, writes)
			})
		}
	}
}

func TestUniqueIndexPublicationDisclosure(t *testing.T) {
	for _, attached := range []bool{false, true} {
		for _, materialize := range []bool{false, true} {
			for _, disclosure := range []string{"none", "partial", "columns", "table"} {
				for _, building := range []bool{false, true} {
					t.Run(fmt.Sprintf("attached=%t/materialize=%t/%s/building=%t", attached, materialize, disclosure, building), func(t *testing.T) {
						f := newUniquePublicationFixture(t, attached, true, false, disclosure, false)
						store := *f.stores[f.parent.Address()]
						store.Versions = uniquePublicationRows(store.Versions)
						var writes []storage.RowVersion
						if building {
							store.Versions[1].Values = slices.Clone(store.Versions[0].Values)
						} else {
							writes = []storage.RowVersion{f.row(t, 1, "2001-02-03", "1")}
						}
						roots, err := f.prepare(t, context.Background(), &store, building, materialize, writes, nil)
						detail := ""
						if building {
							detail = "Duplicate keys exist."
						}
						if disclosure == "columns" || disclosure == "table" {
							detail = "Key (a, b)=(03.02.2001, 1) already exists."
							if building {
								detail = "Key (a, b)=(03.02.2001, 1) is duplicated."
							}
						}
						f.duplicate(t, roots, err, building, detail)
					})
				}
			}
		}
	}
}

func TestUniqueIndexPublicationCancellation(t *testing.T) {
	for _, attached := range []bool{false, true} {
		for _, materialize := range []bool{false, true} {
			for _, rebuild := range []bool{false, true} {
				t.Run(fmt.Sprintf("attached=%t/materialize=%t/rebuild=%t", attached, materialize, rebuild), func(t *testing.T) {
					f := newUniquePublicationFixture(t, attached, true, true, "table", false)
					ctx, cancel := context.WithCancel(context.Background())
					cancel()
					roots, err := f.prepare(t, ctx, nil, rebuild, materialize, []storage.RowVersion{f.row(t, 1, "2001-02-05", "3")}, nil)
					var diagnostic *sqlstate.Error
					if roots != nil || !errors.Is(err, context.Canceled) || errors.As(err, &diagnostic) {
						t.Fatal("canceled preparation returned installation or SQL diagnostic", roots, err)
					}
				})
			}
		}
	}
}
