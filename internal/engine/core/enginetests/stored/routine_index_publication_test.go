package stored_test

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
)

func routinePublicationReceiptNegatives(t *testing.T, input index.RoutineRootInput, candidate index.RootCandidate) {
	t.Helper()
	for _, kind := range []string{"nil-proof", "empty-proof", "copy-target", "source-descriptor", "parent-descriptor", "generation", "ends", "length", "private-root", "foreign", "rebuild"} {
		t.Run("receipt/"+input.Index.Name()+"/"+kind, func(t *testing.T) {
			in := input
			switch kind {
			case "nil-proof":
				in.Publication = nil
			case "empty-proof":
				in.Publication = &stored.ScalarRoutinePublication{}
			case "copy-target":
				copy := *in.Index
				in.Index = &copy
			case "source-descriptor":
				state := catalog.CloneState(in.Committed.State())
				copy := *state.MustRelation(in.Index.Address())
				copy.ObjectRevision++
				state.SetDescriptor(copy.Address(), &copy)
				in.Committed = in.Committed.WithState(state)
			case "parent-descriptor":
				state := catalog.CloneState(in.Committed.State())
				copy := *state.MustRelation(in.Index.SQLIndex.Parent)
				copy.ObjectRevision++
				state.SetDescriptor(copy.Address(), &copy)
				in.Committed = in.Committed.WithState(state)
			case "generation", "ends", "length":
				copy := *in.Store
				copy.IndexSource = in.Store.IndexIdentity()
				if kind == "generation" {
					copy.Generation++
				} else if kind == "ends" {
					copy.Ends = &storage.DeletionRoot{}
				} else {
					copy.Versions = copy.Versions[:len(copy.Versions)-1]
				}
				in.Store = &copy
			case "private-root":
				in.Previous = candidate
				in.PrivateRoot = &storage.IndexRoot{}
			case "foreign":
				in.Committed = engine.New().CatalogRead()
			case "rebuild":
				in.Rebuilds = map[ids.ObjectAddress]*catalog.Relation{in.Index.Address(): in.Index}
			}
			result, err := index.PreserveRoutineRoot(in)
			if err == nil || result.Root != nil {
				t.Fatal("unproven root preservation returned a candidate", kind)
			}
		})
	}
	receipts := map[ids.ObjectAddress]index.RootCandidate{input.Index.Address(): candidate}
	wrongRoots := maps.Clone(input.CommittedRoots)
	wrongRoots[input.Index.Address()] = &storage.IndexRoot{}
	if index.RoutineSourcesMatch(receipts, input.Committed, wrongRoots, input.CommittedStores) {
		t.Fatal("final source fence accepted a replaced physical root")
	}
	wrongSource := catalog.CloneState(input.Committed.State())
	copy := *wrongSource.MustRelation(input.Index.Address())
	copy.ObjectRevision++
	wrongSource.SetDescriptor(copy.Address(), &copy)
	if index.RoutineSourcesMatch(receipts, input.Committed.WithState(wrongSource), input.CommittedRoots, input.CommittedStores) {
		t.Fatal("final source fence accepted changed predecessor")
	}
	wrongEpoch := catalog.CloneState(input.Committed.State())
	wrongEpoch.SetEpoch(input.Committed.Epoch() + 1)
	if index.RoutineSourcesMatch(receipts, input.Committed.WithState(wrongEpoch), input.CommittedRoots, input.CommittedStores) {
		t.Fatal("final source fence accepted old epoch")
	}
	forged := candidate
	forged.Root = &storage.IndexRoot{}
	if index.RoutineSourcesMatch(map[ids.ObjectAddress]index.RootCandidate{input.Index.Address(): forged}, input.Committed, input.CommittedRoots, input.CommittedStores) {
		t.Fatal("copied candidate changed its sealed root")
	}
	// Independently vary each pinned window on an owned copy of the real populated
	// store. The engine's live/retained row vectors are never mutated by injection.
	for _, kind := range []string{"generation", "ends", "length"} {
		model := *input.Store
		model.IndexSource = nil
		in := input
		in.Store = &model
		in.CommittedStores = maps.Clone(input.CommittedStores)
		in.CommittedStores[in.Index.SQLIndex.Parent] = &model
		sealed, err := index.PreserveRoutineRoot(in)
		if err != nil {
			t.Fatal("actual populated window fixture", err)
		}
		switch kind {
		case "generation":
			model.Generation++
		case "ends":
			model.Ends = &storage.DeletionRoot{}
		case "length":
			model.Versions = model.Versions[:len(model.Versions)-1]
		}
		if index.RoutineSourcesMatch(map[ids.ObjectAddress]index.RootCandidate{input.Index.Address(): sealed}, in.Committed, in.CommittedRoots, in.CommittedStores) {
			t.Fatal("final source window fence accepted changed " + kind)
		}
	}
}

func routinePublicationIndexLifecycle(t *testing.T, e *core.Engine, published catalog.Read, parent *catalog.Relation, roots map[ids.ObjectAddress]*storage.IndexRoot) {
	t.Helper()
	sourceStore := e.Storage.Relations[parent.Address()]
	var sourceEntries = make(map[ids.ObjectAddress][]storage.IndexEntry, len(roots))
	for address, root := range roots {
		sourceEntries[address] = slices.Clone(root.Entries)
		for i := range sourceEntries[address] {
			sourceEntries[address][i].Keys = slices.Clone(sourceEntries[address][i].Keys)
		}
	}
	session := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, session, "BEGIN")
	active := true
	defer func() {
		if active {
			enginetest.SessionSQL(t, session, "ROLLBACK")
		}
	}()
	enginetest.SessionSQL(t, session, "INSERT INTO scalar_consumer(v,w) VALUES(10,11)")
	originalWrites := session.Transactions.Writes[parent.Address()]
	if len(originalWrites) != 1 {
		t.Fatal("missing actual pending write")
	}
	snapshotWrites := slices.Clone(originalWrites)
	for i := range snapshotWrites {
		snapshotWrites[i].IndexOutputs = maps.Clone(snapshotWrites[i].IndexOutputs)
		for address, output := range snapshotWrites[i].IndexOutputs {
			output.Keys = slices.Clone(output.Keys)
			snapshotWrites[i].IndexOutputs[address] = output
		}
	}
	keyIndex := transformStoredRelation(t, published, "scalar_key")
	predicateIndex := transformStoredRelation(t, published, "scalar_predicate")
	produced := originalWrites[0].IndexOutputs[keyIndex.Address()]
	if len(produced.Keys) != 1 {
		t.Fatal("missing actual produced scalar key")
	}
	nonmember, hasNonmember := originalWrites[0].IndexOutputs[predicateIndex.Address()]
	if !hasNonmember || nonmember.Member {
		t.Fatal("missing actual produced nonmember output")
	}
	key, err := produced.Keys[0].Integer()
	if err != nil || key != 12 || !produced.Member || originalWrites[0].IndexOutputs[predicateIndex.Address()].Member {
		t.Fatal("missing genuine old-generation key/nonmember outputs", key, err)
	}
	for _, q := range []string{
		"CREATE OR REPLACE FUNCTION scalar_publication(bigint) RETURNS integer LANGUAGE plpgsql IMMUTABLE AS $$BEGIN RETURN $1+3; END$$",
		"CREATE OR REPLACE FUNCTION scalar_publication(bigint) RETURNS integer LANGUAGE plpgsql IMMUTABLE AS $$BEGIN RETURN $1+200; END$$",
	} {
		enginetest.SessionSQL(t, session, q)
		e.Mu.Lock()
		journal := session.Transactions.Catalog
		private := e.PrivateCatalogLocked(journal)
		candidates := index.JournalIndexesOrZero(journal).Candidates
		valid := index.RoutineSourcesMatch(candidates, e.Catalog.Current(), e.Storage.IndexRoots, e.Storage.Relations)
		for address, root := range roots {
			next := private.MustRelation(address)
			candidate := candidates[address]
			valid = valid && candidate.Root == root && candidate.MatchesIndex(next, sourceStore) && e.Storage.IndexRoots[address] == root
		}
		e.Mu.Unlock()
		if !valid || !reflect.DeepEqual(originalWrites, snapshotWrites) {
			t.Fatal("private replacement chain changed source roots or old produced outputs")
		}
	}
	enginetest.SessionSQL(t, session, "ROLLBACK")
	active = false
	if e.CatalogRead().State() != published.State() || e.Storage.Relations[parent.Address()] != sourceStore {
		t.Fatal("rollback changed committed catalog/rows")
	}
	for address, root := range roots {
		if e.Storage.IndexRoots[address] != root || !reflect.DeepEqual(root.Entries, sourceEntries[address]) {
			t.Fatal("private rollback rebuilt/mutated committed index")
		}
	}
	// A copied same-address recipe is not an authenticated output predecessor.
	enginetest.SessionSQL(t, session, "BEGIN")
	active = true
	enginetest.SessionSQL(t, session, "INSERT INTO scalar_consumer(v,w) VALUES(10,11)")
	enginetest.SessionSQL(t, session, "CREATE OR REPLACE FUNCTION scalar_publication(bigint) RETURNS integer LANGUAGE plpgsql IMMUTABLE AS $$BEGIN RETURN $1+3; END$$")
	badWrites := slices.Clone(session.Transactions.Writes[parent.Address()])
	badWrites[0].IndexOutputs = maps.Clone(badWrites[0].IndexOutputs)
	output := badWrites[0].IndexOutputs[keyIndex.Address()]
	forged := *output.Index
	definition := *forged.SQLIndex
	definition.Keys = slices.Clone(definition.Keys)
	derived := *definition.Keys[0].Derived
	definition.Keys[0].Derived = &derived
	forged.SQLIndex = &definition
	output.Index = &forged
	badWrites[0].IndexOutputs[keyIndex.Address()] = output
	session.Transactions.Writes = maps.Clone(session.Transactions.Writes)
	session.Transactions.Writes[parent.Address()] = badWrites
	op, err := session.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, failure := e.Execute(op, enginetest.Statement(t, "COMMIT"), 0, nil)
	op.Done()
	if result.Rows != nil {
		result.Rows.Close()
	}
	if failure == nil {
		t.Fatal("forged produced output acquired routine lineage")
	}
	enginetest.SessionSQL(t, session, "ROLLBACK")
	active = false
	if e.CatalogRead().State() != published.State() {
		t.Fatal("forged output partially installed candidate")
	}
	for address, root := range roots {
		if e.Storage.IndexRoots[address] != root {
			t.Fatal("forged output changed published index")
		}
	}
	enginetest.SessionSQL(t, session, "BEGIN")
	active = true
	enginetest.SessionSQL(t, session, "INSERT INTO scalar_consumer(v,w) VALUES(10,11)")
	committedPrefix := session.Transactions.Writes[parent.Address()]
	prefixSnapshot := slices.Clone(committedPrefix)
	for i := range prefixSnapshot {
		prefixSnapshot[i].IndexOutputs = maps.Clone(prefixSnapshot[i].IndexOutputs)
		for address, output := range prefixSnapshot[i].IndexOutputs {
			output.Keys = slices.Clone(output.Keys)
			prefixSnapshot[i].IndexOutputs[address] = output
		}
	}
	for _, q := range []string{
		"CREATE OR REPLACE FUNCTION scalar_publication(bigint) RETURNS integer LANGUAGE plpgsql IMMUTABLE AS $$BEGIN RETURN $1+3; END$$",
		"CREATE OR REPLACE FUNCTION scalar_publication(bigint) RETURNS integer LANGUAGE plpgsql IMMUTABLE AS $$BEGIN RETURN $1+200; END$$",
	} {
		enginetest.SessionSQL(t, session, q)
	}
	enginetest.SessionSQL(t, session, "COMMIT")
	active = false
	committed := e.Storage.IndexRoots[keyIndex.Address()]
	if committed == nil || len(committed.Entries) != 3 || !reflect.DeepEqual(committedPrefix, prefixSnapshot) {
		t.Fatal("commit lost/mutated produced prefix")
	}
	originalOutput := committedPrefix[0].IndexOutputs[keyIndex.Address()]
	if len(originalOutput.Keys) != 1 || &committed.Entries[0].Keys[0] != &originalOutput.Keys[0] || committed.Entries[0].Row != committedPrefix[0].Row || committed.Entries[0].Version != committedPrefix[0].Version {
		t.Fatal("transport copied produced key backing or changed row/version identity")
	}
	for i, want := range []int64{12, 102, 104} {
		got, err := committed.Entries[i].Keys[0].Integer()
		if err != nil || got != want {
			t.Fatal("commit recomputed old produced key", got, want, err)
		}
	}
	if got := e.Storage.IndexRoots[predicateIndex.Address()]; got == nil || len(got.Entries) != 2 {
		t.Fatal("commit recomputed old nonmember predicate output")
	}
	for address, root := range roots {
		if !reflect.DeepEqual(root.Entries, sourceEntries[address]) {
			t.Fatal("commit mutated retained source entries")
		}
	}
	// A genuine referenced-column TYPE rewrite supersedes preservation authority
	// and evaluates the successor, including the previously nonmember row.
	enginetest.Run(t, e, "ALTER TABLE scalar_consumer ALTER COLUMN v TYPE bigint")
	current := e.CatalogRead()
	nextKey := transformStoredRelation(t, current, "scalar_key")
	keyRoot := e.Storage.IndexRoots[nextKey.Address()]
	if keyRoot == nil || keyRoot == committed || len(keyRoot.Entries) != 3 {
		t.Fatal("genuine ALTER did not produce its own populated root")
	}
	for i, want := range []int64{210, 301, 303} {
		got, err := keyRoot.Entries[i].Keys[0].Integer()
		if err != nil || got != want {
			t.Fatal("ALTER used wrong executable key generation", got, want, err)
		}
	}
	if got := e.Storage.IndexRoots[predicateIndex.Address()]; got == nil || len(got.Entries) != 3 {
		t.Fatal("genuine rebuild consumed old transported nonmember output")
	}
	for address, root := range roots {
		if e.Storage.IndexRoots[address] == root || !reflect.DeepEqual(root.Entries, sourceEntries[address]) {
			t.Fatal("ALTER reused preservation receipt or mutated retained source entries")
		}
	}
}

func routinePublicationNoRebuild(t *testing.T) {
	e := engine.New()
	for _, q := range []string{"CREATE FUNCTION scalar_preflight(integer) RETURNS integer IMMUTABLE RETURN $1", "CREATE TABLE scalar_preflight_rows(id integer)", "CREATE INDEX scalar_preflight_index ON scalar_preflight_rows(scalar_preflight(id))", "INSERT INTO scalar_preflight_rows VALUES(1)", "CREATE FUNCTION scalar_preflight_guard() RETURNS text IMMUTABLE LANGUAGE plpgsql AS $$BEGIN RAISE NOTICE 'replacement-preflight'; RETURN 'checked'; END$$", "CREATE DOMAIN scalar_preflight_text AS text CHECK(scalar_preflight_guard() IS NOT NULL)"} {
		enginetest.Run(t, e, q)
	}
	before := e.CatalogRead()
	idx := transformStoredRelation(t, before, "scalar_preflight_index")
	parent := transformStoredRelation(t, before, "scalar_preflight_rows")
	root := e.Storage.IndexRoots[idx.Address()]
	store := e.Storage.Relations[parent.Address()]
	if root == nil || len(root.Entries) != 1 {
		t.Fatal("missing populated no-rewrite fixture")
	}
	enginetest.Run(t, e, "CREATE OR REPLACE FUNCTION scalar_preflight(integer) RETURNS integer IMMUTABLE LANGUAGE plpgsql AS $$BEGIN RETURN 1/($1-$1); END$$")
	if e.Storage.IndexRoots[idx.Address()] != root || e.Storage.Relations[parent.Address()] != store {
		t.Fatal("replacement evaluated/rebuilt no-demand index")
	}
	enginetest.Run(t, e, "ALTER TABLE scalar_preflight_rows ADD COLUMN token text DEFAULT (('filled'::scalar_preflight_text)::text)")
	if len(root.Entries) != 1 || before.Get(idx.Address()) != idx {
		t.Fatal("preflight changed retained source root/descriptor")
	}
}
