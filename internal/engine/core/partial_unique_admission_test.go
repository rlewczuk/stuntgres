package core_test

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func partialUniqueScope(t *testing.T, f *uniquePublicationFixture) *engine.StatementScope {
	t.Helper()
	op, done := enginetest.RoleOperation(t, f.e, "postgres")
	t.Cleanup(done)
	scope, err := f.e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(nil) })
	result, err := scope.Execute(enginetest.Statement(t, "SELECT 1"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		result.Rows.Close()
	}
	return scope
}

func TestPartialIndexUniqueAdmissionPrivatePrefix(t *testing.T) {
	for _, source := range []string{"own", "stage", "prefix"} {
		for _, membership := range []string{"false", "", "true"} {
			for _, candidate := range []string{"false", "", "true"} {
				t.Run(fmt.Sprintf("%s/old=%q/new=%q", source, membership, candidate), func(t *testing.T) {
					f := newPartialUniquePublication(t, true, false, "table")
					scope := partialUniqueScope(t, f)
					old := partialUniqueRow(t, f, 1, "2001-02-05", "3", membership)
					next := partialUniqueRow(t, f, 2, "2001-02-05", "3", candidate)
					var prefix [][]sqltype.Value
					scope.OwnWrites = make(map[ids.ObjectAddress][]storage.RowVersion)
					scope.Stage.Rows = make(map[ids.ObjectAddress][]storage.RowVersion)
					switch source {
					case "own":
						scope.OwnWrites[f.ref.Address] = []storage.RowVersion{old}
					case "stage":
						scope.Stage.Rows[f.ref.Address] = []storage.RowVersion{old}
					case "prefix":
						prefix = [][]sqltype.Value{old.Values}
					}
					err := dml.CheckUniqueRow(scope.WriteHost(), f.ref, &index.WriteCandidate{Values: next.Values}, nil, index.WriteBatch{Values: prefix})
					if membership == "true" && candidate == "true" {
						nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `duplicate key value violates unique constraint "ui_index"`, Detail: "Key (a, b)=(2001-02-05, 3) already exists.", Schema: "public", Table: "ui_t", Constraint: "ui_index"})
					} else if err != nil {
						t.Fatal("nonmember participates in unique admission", err)
					}
					if !reflect.DeepEqual(old.Values, partialUniqueRow(t, f, 1, "2001-02-05", "3", membership).Values) {
						t.Fatal("private row mutated")
					}
					f.unchanged(t)
					// The synthetic overlay belongs only to this direct owner witness, not a
					// transaction installation. Remove it before closing the real scope.
					delete(scope.OwnWrites, f.ref.Address)
					delete(scope.Stage.Rows, f.ref.Address)
				})
			}
		}
	}
}

func TestPartialIndexUniqueAdmissionCommittedAndClaims(t *testing.T) {
	for _, active := range []string{"false", "", "true"} {
		t.Run(fmt.Sprintf("active=%q", active), func(t *testing.T) {
			f := newPartialUniquePublication(t, true, false, "table")
			scope := partialUniqueScope(t, f)
			row := partialUniqueRow(t, f, 1, "2001-02-03", "1", active)
			err := dml.CheckUniqueRow(scope.WriteHost(), f.ref, &index.WriteCandidate{Values: row.Values}, nil, index.WriteBatch{})
			if active == "true" {
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `duplicate key value violates unique constraint "ui_index"`, Detail: "Key (a, b)=(2001-02-03, 1) already exists.", Schema: "public", Table: "ui_t", Constraint: "ui_index"})
			} else {
				if err != nil {
					t.Fatal(err)
				}
				uniqueKeyBucketsEmpty(t, f.e)
			}
			f.unchanged(t)
		})
	}
	// Old-key claiming has a separate consumer from admission. Excluded deletes
	// and leaving-row predecessors must not reserve an unrelated member's key.
	for _, active := range []string{"false", ""} {
		t.Run("claim/"+active, func(t *testing.T) {
			f := newPartialUniquePublication(t, true, false, "table")
			scope := partialUniqueScope(t, f)
			row := partialUniqueRow(t, f, 1, "2001-02-03", "1", active)
			if err := dml.ClaimUniqueRow(scope.WriteHost(), f.ref, row); err != nil {
				t.Fatal(err)
			}
			uniqueKeyBucketsEmpty(t, f.e)
		})
	}
}

func TestPartialIndexUniqueAdmissionStrongSlots(t *testing.T) {
	for _, ordinary := range []bool{false, true} {
		t.Run(fmt.Sprint(ordinary), func(t *testing.T) {
			f := newPartialUniquePublication(t, true, false, "table")
			if ordinary {
				peer, done := enginetest.RoleOperation(t, f.e, "postgres")
				defer done()
				enginetest.RoleExecute(t, f.e, peer, "CREATE UNIQUE INDEX ui_ordinary ON ui_t(p)")
			}
			scope := partialUniqueScope(t, f)
			binding, err := dml.BindUniqueWrite(scope.WriteHost(), f.ref)
			if err != nil {
				t.Fatal(err)
			}
			want := []int(nil)
			if ordinary {
				want = []int{2}
			}
			if !reflect.DeepEqual(binding.KeySlots, want) {
				t.Fatal("partial keys became strong row-lock slots", binding.KeySlots, want)
			}
			old := partialUniqueRow(t, f, 1, "2001-02-05", "3", "true")
			next := partialUniqueRow(t, f, 2, "2001-02-06", "3", "false")
			next.Values[2] = old.Values[2]
			changed, err := dml.UniqueKeyChanged(scope.WriteHost(), f.ref, old.Values, next.Values)
			if err != nil || changed {
				t.Fatal("partial key/predicate update stamped strong history", changed, err)
			}
			if ordinary {
				next = partialUniqueRow(t, f, 2, "2001-02-06", "4", "false")
				changed, err = dml.UniqueKeyChanged(scope.WriteHost(), f.ref, old.Values, next.Values)
				if err != nil || !changed {
					t.Fatal("ordinary key control lost strength", changed, err)
				}
			}
		})
	}
}

func TestPartialIndexUniqueAdmissionNullPredicateError(t *testing.T) {
	for _, claim := range []bool{false, true} {
		t.Run(fmt.Sprint(claim), func(t *testing.T) {
			f := newPartialUniquePublication(t, false, false, "table")
			peer, done := enginetest.RoleOperation(t, f.e, "postgres")
			defer done()
			enginetest.RoleExecute(t, f.e, peer, "CREATE UNIQUE INDEX ui_error ON ui_t(a) WHERE active AND 10/b>0")
			scope := partialUniqueScope(t, f)
			row := partialUniqueRow(t, f, 1, "", "0", "true")
			var err error
			if claim {
				err = dml.ClaimUniqueRow(scope.WriteHost(), f.ref, row)
			} else {
				err = dml.CheckUniqueRow(scope.WriteHost(), f.ref, &index.WriteCandidate{Values: row.Values}, nil, index.WriteBatch{})
			}
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "22012", Message: "division by zero"})
			uniqueKeyBucketsEmpty(t, f.e)
		})
	}
}

type uniquePublicationFixture struct {
	e              *engine.Engine
	parent, index  *catalog.Relation
	ref            ids.ObjectRef
	auth           engine.AuthorizationView
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
	ref := enginetest.RoleTableRef(t, e, "public.ui_t")
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
	auth, err := engine.OperationAuthorization(owner)
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

func uniquePublicationStore(store *storage.Relation) storage.Relation {
	copy := *store
	copy.Versions = uniquePublicationRows(store.Versions)
	if store.Ends != nil {
		copy.Ends = &storage.DeletionRoot{Rows: maps.Clone(store.Ends.Rows)}
	}
	return copy
}

func uniquePublicationRows(rows []storage.RowVersion) []storage.RowVersion {
	copy := slices.Clone(rows)
	for i := range copy {
		copy[i].Values = slices.Clone(copy[i].Values)
	}
	return copy
}
