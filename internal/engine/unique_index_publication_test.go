package engine

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

type uniquePublicationFixture struct {
	e              *Engine
	parent, index  *relationDescriptor
	ref            ObjectRef
	auth           AuthorizationView
	keys           int
	catalog        *catalogState
	descriptors    map[ObjectAddress]Descriptor
	names          map[NameKey]ObjectAddress
	roles          *roleCatalog
	stores         map[ObjectAddress]*relationStorage
	storeSnapshots map[ObjectAddress]relationStorage
	roots          map[ObjectAddress]*logicalIndexRoot
	entries        map[ObjectAddress][]logicalIndexEntry
	nextRow        uint64
	nextVersion    uint64
	frontier       CommitSeq
}

func uniquePublicationRows(rows []rowVersion) []rowVersion {
	copy := slices.Clone(rows)
	for i := range copy {
		copy[i].values = slices.Clone(copy[i].values)
	}
	return copy
}

func uniquePublicationStore(store *relationStorage) relationStorage {
	copy := *store
	copy.versions = uniquePublicationRows(store.versions)
	if store.ends != nil {
		copy.ends = &deletionRoot{rows: maps.Clone(store.ends.rows)}
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
		if err := e.SetObjectACL(context.Background(), target, acl); err != nil {
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
	auth, err := owner.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	parent := e.state.descriptors[ref.Address].(*relationDescriptor)
	index, _ := btreeRoot(t, e, "public.ui_index")
	if !index.sqlIndex.unique || index.sqlIndex.nullsNotDistinct != nnd || len(index.sqlIndex.keys) != count {
		t.Fatal("invalid unique fixture")
	}
	if attached {
		if index.sqlIndex.constraint == nil || len(parent.uniqueConstraints) != 1 || parent.uniqueConstraints[0].Index != index.address {
			t.Fatal("attachment must be genuine")
		}
	} else if index.sqlIndex.constraint != nil || len(parent.uniqueConstraints) != 0 {
		t.Fatal("standalone fixture fabricated attachment")
	}
	if err := validateIndexDefinition(e.CatalogRead(), index); err != nil {
		t.Fatal(err)
	}
	f := &uniquePublicationFixture{e: e, parent: parent, index: index, ref: ref, auth: auth, keys: count, catalog: e.state, descriptors: maps.Clone(e.state.descriptors), names: maps.Clone(e.state.names), roles: e.roles, stores: maps.Clone(e.storage), storeSnapshots: make(map[ObjectAddress]relationStorage), roots: maps.Clone(e.indexRoots), entries: make(map[ObjectAddress][]logicalIndexEntry), nextRow: e.nextRow, nextVersion: e.nextVersion, frontier: e.commitSeq}
	for address, store := range f.stores {
		f.storeSnapshots[address] = uniquePublicationStore(store)
	}
	for address, root := range f.roots {
		entries := slices.Clone(root.entries)
		for i := range entries {
			entries[i].keys = slices.Clone(entries[i].keys)
		}
		f.entries[address] = entries
	}
	return f
}

func (f *uniquePublicationFixture) row(t *testing.T, offset int, a, b string) rowVersion {
	t.Helper()
	values := make([]sqltype.Value, 3)
	for i, text := range []string{a, b} {
		typ := f.parent.columns[i].Usage.Type
		values[i] = sqltype.Null(typ)
		if text != "" {
			var err error
			values[i], err = typ.Input(context.Background(), text, -1)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	values[2] = sqltype.String(f.parent.columns[2].Usage.Type, fmt.Sprintf("new%d", offset))
	return rowVersion{row: RowID(f.nextRow + uint64(offset)), version: VersionID(f.nextVersion + uint64(offset)), values: values}
}

func (f *uniquePublicationFixture) unchanged(t *testing.T) {
	t.Helper()
	if f.e.state != f.catalog || f.e.roles != f.roles || f.e.nextRow != f.nextRow || f.e.nextVersion != f.nextVersion || f.e.commitSeq != f.frontier || len(f.e.indexRoots) != len(f.roots) || len(f.e.storage) != len(f.stores) {
		t.Fatal("package preparation changed live publication state")
	}
	if !maps.Equal(f.catalog.descriptors, f.descriptors) || !maps.Equal(f.catalog.names, f.names) {
		t.Fatal("retained catalog membership mutated")
	}
	for address, root := range f.roots {
		if f.e.indexRoots[address] != root || !reflect.DeepEqual(root.entries, f.entries[address]) {
			t.Fatal("retained root mutated", address)
		}
	}
	for address, store := range f.stores {
		if f.e.storage[address] != store || !reflect.DeepEqual(*store, f.storeSnapshots[address]) {
			t.Fatal("retained store mutated", address)
		}
	}
}

func (f *uniquePublicationFixture) prepare(t *testing.T, ctx context.Context, store *relationStorage, rebuild, materialize bool, writes []rowVersion, deletion *privateDeletionRoot) (map[ObjectAddress]*logicalIndexRoot, error) {
	t.Helper()
	if store == nil {
		store = f.stores[f.parent.address]
	}
	beforeStore := uniquePublicationStore(store)
	beforeWrites := uniquePublicationRows(writes)
	var beforeDeletes map[RowID]deleteCandidate
	if deletion != nil {
		beforeDeletes = maps.Clone(deletion.rows)
	}
	stores := captureIndexStores(f.e.storage, f.catalog)
	stores[f.parent.address] = store
	parents := map[ObjectAddress]bool{f.parent.address: rebuild}
	publication := rowPublication{address: f.parent.address, relation: f.parent, originalRelation: f.parent, original: f.stores[f.parent.address], store: store, root: store.versions, ends: store.ends, generation: store.generation, writes: writes, deletion: deletion, singleSlot: -1}
	if materialize {
		if err := f.e.prepareRowPublication(&publication, TxRef{owner: f.e, epoch: f.catalog.epoch}, true); err != nil {
			t.Fatal(err)
		}
	}
	output := sqltype.OutputEnvironment{DateStyle: sqltype.DateStyle{Style: sqltype.DateGerman, Order: sqltype.DateDMY}}
	roots, err := f.e.prepareIndexPublication(&EvalContext{Context: ctx, Scope: 1, Frame: 1}, f.catalog, f.catalog, stores, f.roots, parents, nil, []rowPublication{publication}, materialize, output, &f.auth, nil, nil)
	if !reflect.DeepEqual(*store, beforeStore) || !reflect.DeepEqual(writes, beforeWrites) || deletion != nil && !reflect.DeepEqual(deletion.rows, beforeDeletes) {
		t.Fatal("publication inputs mutated")
	}
	f.unchanged(t)
	return roots, err
}

func (f *uniquePublicationFixture) duplicate(t *testing.T, roots map[ObjectAddress]*logicalIndexRoot, err error, building bool, detail string) {
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

func (f *uniquePublicationFixture) membership(t *testing.T, roots map[ObjectAddress]*logicalIndexRoot, materialize bool, rows []rowVersion) {
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
	root := roots[f.index.address]
	if root == nil {
		t.Fatal("completion omitted substantive root")
	}
	want := make([]logicalIndexEntry, len(rows))
	nextPosition := len(f.stores[f.parent.address].versions)
	for i, row := range rows {
		_, position, found := rowByID(f.stores[f.parent.address].versions, row.row, -1)
		if !found {
			position = nextPosition
			nextPosition++
		}
		want[i] = logicalIndexEntry{row: row.row, version: row.version, position: position, keys: slices.Clone(row.values[:f.keys])}
	}
	if !reflect.DeepEqual(root.entries, want) {
		t.Fatal("wrong completion membership", root.entries, want)
	}
	for address, old := range f.roots {
		if address != f.index.address && roots[address] != old {
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
					store := *f.stores[f.parent.address]
					store.versions = uniquePublicationRows(store.versions)
					// Real valid definitions, with a deliberately duplicate build
					// input. The separate delta could hide this if applied first.
					store.versions[1].values = slices.Clone(store.versions[0].values)
					old := store.versions[1]
					var writes []rowVersion
					var deletion *privateDeletionRoot
					if delta == "delete" {
						deletion = &privateDeletionRoot{rows: map[RowID]deleteCandidate{old.row: {ref: f.ref, storage: &store, row: old.row, version: old.version}}}
					} else {
						replacement := f.row(t, 1, "2001-02-05", "3")
						replacement.row, replacement.base = old.row, old.version
						writes = []rowVersion{replacement}
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
							writes := []rowVersion{f.row(t, 1, tc.a, tc.b)}
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
								want := append(uniquePublicationRows(f.stores[f.parent.address].versions), writes...)
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
						writes := []rowVersion{f.row(t, 1, "", "1")}
						roots, err := f.prepare(t, context.Background(), nil, false, materialize, writes, nil)
						if nnd {
							f.duplicate(t, roots, err, false, uniquePublicationKey(composite, "", "1")+" already exists.")
						} else {
							if err != nil {
								t.Fatal(err)
							}
							want := append(uniquePublicationRows(f.stores[f.parent.address].versions), writes...)
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
				rows := f.stores[f.parent.address].versions
				deletion := &privateDeletionRoot{rows: map[RowID]deleteCandidate{rows[0].row: {ref: f.ref, storage: f.stores[f.parent.address], row: rows[0].row, version: rows[0].version}}}
				replacement := f.row(t, 1, "2001-02-05", "3")
				replacement.row, replacement.base = rows[1].row, rows[1].version
				writes := []rowVersion{replacement, f.row(t, 2, "2001-02-06", "4")}
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
						store := *f.stores[f.parent.address]
						store.versions = uniquePublicationRows(store.versions)
						var writes []rowVersion
						if building {
							store.versions[1].values = slices.Clone(store.versions[0].values)
						} else {
							writes = []rowVersion{f.row(t, 1, "2001-02-03", "1")}
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
					roots, err := f.prepare(t, ctx, nil, rebuild, materialize, []rowVersion{f.row(t, 1, "2001-02-05", "3")}, nil)
					var diagnostic *sqlstate.Error
					if roots != nil || !errors.Is(err, context.Canceled) || errors.As(err, &diagnostic) {
						t.Fatal("canceled preparation returned installation or SQL diagnostic", roots, err)
					}
				})
			}
		}
	}
}
