package enginetests_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func brinPublicAddress(t *testing.T, e *core.Engine, class ids.OID, name string) ids.ObjectAddress {
	t.Helper()
	a, ok := e.CatalogRead().LookupName(ids.NameKey{ClassID: class, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})
	if !ok {
		t.Fatal("missing fixture descriptor", name)
	}
	return a
}

func brinTruthRoot(t *testing.T, e *core.Engine) *storage.IndexRoot {
	t.Helper()
	a := brinPublicAddress(t, e, ids.RelationClassID, "brin_truth_index")
	e.Mu.Lock()
	r := e.Storage.IndexRoots[a]
	e.Mu.Unlock()
	if r == nil || r.Brin == nil || len(r.Brin.Bindings) != 2 {
		t.Fatal("missing produced BRIN root", r)
	}
	return r
}

func brinTruthCounts(t *testing.T, e *core.Engine) [2]int64 {
	t.Helper()
	key := brinPublicAddress(t, e, ids.RelationClassID, "brin_key_steps")
	predicate := brinPublicAddress(t, e, ids.RelationClassID, "brin_predicate_steps")
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return [2]int64{e.Storage.Sequences[key].Last, e.Storage.Sequences[predicate].Last}
}

func brinProducedFixture(t *testing.T) *core.Engine {
	t.Helper()
	e := engine.New()
	// CHECKs observe genuine execution through the real SQL host. Neither the
	// callable key nor the predicate has a test callback or a synthetic output.
	for _, q := range []string{
		"CREATE SEQUENCE brin_key_steps",
		"CREATE SEQUENCE brin_predicate_steps",
		"CREATE DOMAIN brin_key_domain AS INT CHECK(nextval('brin_key_steps')>0)",
		"CREATE DOMAIN brin_predicate_domain AS INT CHECK(nextval('brin_predicate_steps')>0)",
		"CREATE TABLE brin_truth(id INT,n INT)",
		"INSERT INTO brin_truth VALUES(-1,-100),(2,-9),(3,-4),(4,NULL),(5,-7),(6,-2)",
		"CREATE INDEX brin_truth_index ON brin_truth USING brin ((abs(n)::brin_key_domain),abs(id)) WITH(pages_per_range=2) WHERE (id::brin_predicate_domain)::integer>0",
	} {
		enginetest.Run(t, e, q)
	}
	if got := brinTruthCounts(t, e); got != [2]int64{5, 6} {
		t.Fatal("real key/predicate execution did not produce the fixture", got)
	}
	r := brinTruthRoot(t, e)
	for ordinal, want := range [][2]int64{{9, 9}, {4, 4}, {2, 7}} {
		a, b := brinBounds(t, r.Brin.Ranges[ordinal].Keys[0])
		if [2]int64{a, b} != want || r.Brin.Ranges[ordinal].Keys[0].HasNull != (ordinal == 1) {
			t.Fatal("callable/partial build truth", ordinal, a, b)
		}
	}
	return e
}

func brinCopyRanges(root *storage.IndexRoot) []storage.BrinRange {
	ranges := slices.Clone(root.Brin.Ranges)
	for i := range ranges {
		ranges[i].Keys = slices.Clone(ranges[i].Keys)
	}
	return ranges
}

func brinProducedVacuum(t *testing.T) {
	e := brinProducedFixture(t)
	original := brinTruthRoot(t, e)
	originalRanges := brinCopyRanges(original)
	counts := brinTruthCounts(t, e)
	parent := brinPublicAddress(t, e, ids.RelationClassID, "brin_truth")
	idx := brinPublicAddress(t, e, ids.RelationClassID, "brin_truth_index")
	enginetest.Run(t, e, "ALTER INDEX brin_truth_index SET(pages_per_range=1)")
	enginetest.Run(t, e, "VACUUM(FREEZE) brin_truth")
	frozen := brinTruthRoot(t, e)
	if frozen == original || frozen.Brin.EffectivePagesPerRange != 2 || !reflect.DeepEqual(frozen.Brin.Ranges, originalRanges) {
		t.Fatal("freeze-only work changed produced truth/effective width", frozen)
	}
	for _, row := range e.Storage.Relations[parent].Versions {
		if !row.Frozen {
			t.Fatal("fixture did not actually execute freeze-only work")
		}
	}
	if brinTruthCounts(t, e) != counts {
		t.Fatal("freeze replayed a callable key or predicate")
	}
	enginetest.Run(t, e, "DELETE FROM brin_truth WHERE id=2")
	before := brinTruthRoot(t, e)
	store := e.Storage.Relations[parent]
	rows, ends, generation := slices.Clone(store.Versions), store.Ends, store.Generation
	beforeRanges := brinCopyRanges(before)
	view := e.CatalogRead()
	capture := table.VacuumCapture{Rows: store.Versions, Ends: ends, Generation: generation, Frontier: e.Visibility.CommitSeq(), Cutoff: ^uint64(0), Retire: true, Catalog: view.State(), Relation: view.State().MustRelation(parent), Indexes: map[ids.ObjectAddress]*storage.IndexRoot{idx: before}, Attached: 1, IndexCount: 1}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	indexPhase := false
	pass, err := table.PrepareVacuumPass(ctx, capture, store, func(phase string, _, processed int64) {
		if phase == "vacuuming indexes" && processed == 0 {
			// The real heap candidate has already been materialized. Interrupt
			// before its BRIN work can return an installable pass.
			indexPhase = true
			cancel()
		}
	})
	if !indexPhase || !errors.Is(err, context.Canceled) || pass.Roots != nil || pass.Candidate.Versions != nil {
		t.Fatal("canceled produced-truth pass remained installable", indexPhase, pass, err)
	}
	if brinTruthRoot(t, e) != before || !reflect.DeepEqual(store.Versions, rows) || store.Ends != ends || store.Generation != generation || !reflect.DeepEqual(before.Brin.Ranges, beforeRanges) || brinTruthCounts(t, e) != counts {
		t.Fatal("canceled vacuum mutated captured rows, summaries or SQL truth")
	}
	enginetest.Run(t, e, "VACUUM brin_truth")
	next := brinTruthRoot(t, e)
	if next == before || next.Brin.Prefix != 5 || next.Brin.EffectivePagesPerRange != 1 || len(next.Brin.Ranges) != 5 {
		t.Fatal("actual compaction did not install destination ranges", next)
	}
	for ordinal, want := range [][2]int64{{9, 9}, {4, 4}, {4, 4}, {2, 7}, {2, 7}} {
		r := next.Brin.Ranges[ordinal]
		a, b := brinBounds(t, r.Keys[0])
		if !r.Summarized || [2]int64{a, b} != want || r.Keys[0].HasNull != (ordinal == 1 || ordinal == 2) {
			t.Fatal("produced summaries were replayed instead of conservatively regrouped", ordinal, r)
		}
	}
	for slot, row := range e.Storage.Relations[parent].Versions {
		id, err := row.Values[0].Integer()
		if err != nil {
			t.Fatal(err)
		}
		if id < 0 {
			continue // Nonmember abs(-100) must not enter the summary.
		}
		strategy, value := "IS NULL", sqltype.Value{}
		if id != 4 {
			strategy = "="
			n := map[int64]int64{3: 4, 5: 7, 6: 2}[id]
			base, _ := e.CatalogRead().LookupType(sqltype.Int4)
			value, err = next.Brin.Bindings[0].Usage.Type.Wrap(brinInteger(t, base, n))
			if err != nil {
				t.Fatal(err)
			}
		}
		hit, err := index.BrinConsistent(context.Background(), next.Brin, slot, 0, strategy, value, sqltype.InputEnvironment{})
		if err != nil || !hit {
			t.Fatal("surviving produced key lost its conservative candidate", slot, id, hit, err)
		}
	}
	if brinTruthCounts(t, e) != counts || !reflect.DeepEqual(original.Brin.Ranges, originalRanges) || original.Brin.EffectivePagesPerRange != 2 || original.Brin.Prefix != 6 || !reflect.DeepEqual(before.Brin.Ranges, beforeRanges) {
		t.Fatal("vacuum replayed recipes or mutated retained roots")
	}
}

func brinProducedGeneration(t *testing.T) {
	e := brinProducedFixture(t)
	enginetest.Run(t, e, "DELETE FROM brin_truth WHERE id=2")
	original := brinTruthRoot(t, e)
	ranges := brinCopyRanges(original)
	counts := brinTruthCounts(t, e)
	parent := brinPublicAddress(t, e, ids.RelationClassID, "brin_truth")
	store := e.Storage.Relations[parent]
	rows, ends, generation := slices.Clone(store.Versions), store.Ends, store.Generation
	oldType := original.Brin.Bindings[0].Usage.Type
	// NOT VALID changes authority for new truth, not the already-produced
	// expression/predicate outputs. FALSE would expose an attempted replay.
	enginetest.Run(t, e, "ALTER DOMAIN brin_key_domain ADD CONSTRAINT future_key CHECK(FALSE) NOT VALID")
	enginetest.Run(t, e, "ALTER DOMAIN brin_predicate_domain ADD CONSTRAINT future_predicate CHECK(FALSE) NOT VALID")
	next := brinTruthRoot(t, e)
	current := e.Storage.Relations[parent]
	keyAddress := brinPublicAddress(t, e, ids.TypeClassID, "brin_key_domain")
	keyType := e.CatalogRead().State().MustType(keyAddress).SQLType
	if next == original || keyType == oldType || keyType.ID() != oldType.ID() || next.Brin.Bindings[0].Usage.Type != keyType || next.Brin.Storage != current.IndexIdentity() || next.Brin.Generation != current.Generation || next.Brin.Prefix != len(rows) || next.Brin.EffectivePagesPerRange != original.Brin.EffectivePagesPerRange {
		t.Fatal("same-ID generation did not reown the captured root/provider", next)
	}
	if len(current.Versions) != len(rows) || current.Ends != ends || current.Generation != generation {
		t.Fatal("generation transport changed all-version storage lineage")
	}
	for slot, row := range rows {
		n := current.Versions[slot]
		if n.Row != row.Row || n.Version != row.Version || n.Born != row.Born || !reflect.DeepEqual(n.Values, row.Values) {
			t.Fatal("generation transport changed physical version identity/order", slot)
		}
	}
	for ordinal, old := range ranges {
		r := next.Brin.Ranges[ordinal]
		if r.Summarized != old.Summarized || r.Keys[0].HasNull != old.Keys[0].HasNull || r.Keys[0].HasNonNull != old.Keys[0].HasNonNull {
			t.Fatal("generation changed produced membership/NULL truth", ordinal)
		}
		a, b := brinBounds(t, old.Keys[0])
		c, d := brinBounds(t, r.Keys[0])
		if a != c || b != d || r.Keys[0].Min.Type() != keyType || r.Keys[0].Max.Type() != keyType {
			t.Fatal("generation did not reown unchanged extrema", ordinal)
		}
		provider := next.Brin.Bindings[0].Comparison
		if _, err := provider.Compare(context.Background(), r.Keys[0].Min, r.Keys[0].Max, sqltype.InputEnvironment{}); err != nil {
			t.Fatal("provider retained source-generation authority", err)
		}
		if _, err := provider.Compare(context.Background(), old.Keys[0].Min, old.Keys[0].Max, sqltype.InputEnvironment{}); err == nil {
			t.Fatal("destination provider accepted stale-generation datums")
		}
	}
	if brinTruthCounts(t, e) != counts || !reflect.DeepEqual(original.Brin.Ranges, ranges) || original.Brin.Bindings[0].Usage.Type != oldType {
		t.Fatal("generation transport replayed recipes or mutated source authority")
	}
	_, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, "INSERT INTO brin_truth VALUES(7,-11)"), nil)
	var state *sqlstate.Error
	if !errors.As(err, &state) || state.Code != "23514" || brinTruthRoot(t, e) != next {
		t.Fatal("fresh DML did not enforce the new domain generation atomically", err)
	}
}

func brinProducedReplacement(t *testing.T) {
	source := brinProducedFixture(t)
	enginetest.Run(t, source, "DELETE FROM brin_truth WHERE id=2")
	original := brinTruthRoot(t, source)
	ranges := brinCopyRanges(original)
	counts := brinTruthCounts(t, source)
	parent := brinPublicAddress(t, source, ids.RelationClassID, "brin_truth")
	idx := brinPublicAddress(t, source, ids.RelationClassID, "brin_truth_index")
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), source, &dump); err != nil {
		t.Fatal(err)
	}
	target := engine.New()
	temporary := enginetest.StartSession(t, target, nil)
	retained := make(map[ids.ObjectAddress]catalog.Descriptor)
	var collision catalog.Descriptor
	var maximum ids.OID
	for i := 0; collision == nil && (i == 0 || maximum < idx.ObjectID); i++ {
		previous := maximum
		enginetest.SessionSQL(t, temporary, fmt.Sprintf("CREATE TEMP TABLE brin_retained_%d(v INT)", i))
		for a, d := range target.CatalogRead().Descriptors() {
			if a != temporary.TemporaryNamespace && d.Namespace() != temporary.TemporaryNamespace {
				continue
			}
			retained[a] = d
			maximum = max(maximum, a.ObjectID)
			if a.ObjectID == idx.ObjectID {
				collision = d
			}
		}
		if maximum <= previous {
			t.Fatal("real retained TEMP allocation made no progress")
		}
	}
	if collision == nil {
		t.Fatal("real TEMP allocation skipped the imported index identity", idx, maximum)
	}
	enginetest.SessionSQL(t, temporary, "INSERT INTO pg_temp.brin_retained_0 VALUES(73)")
	if err := engine.Restore(context.Background(), target, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	newParent := brinPublicAddress(t, target, ids.RelationClassID, "brin_truth")
	newIndex := brinPublicAddress(t, target, ids.RelationClassID, "brin_truth_index")
	if newIndex == idx || newParent == parent {
		t.Fatal("fixture did not force actual index and table identity collisions", idx, newIndex, parent, newParent)
	}
	for a, d := range retained {
		if target.CatalogRead().Get(a) != d {
			t.Fatal("replacement changed a retained TEMP identity", a)
		}
	}
	enginetest.TransactionRows(t, temporary, "SELECT v FROM pg_temp.brin_retained_0", 73)
	next := brinTruthRoot(t, target)
	if next.Brin.Prefix != 5 || next.Brin.EffectivePagesPerRange != 2 || len(next.Brin.Ranges) != 3 || next.Brin.Storage != target.Storage.Relations[newParent].IndexIdentity() {
		t.Fatal("admitted repair did not rebuild the compact destination layout", next)
	}
	keyAddress := brinPublicAddress(t, target, ids.TypeClassID, "brin_key_domain")
	keyType := target.CatalogRead().State().MustType(keyAddress).SQLType
	sourceType := original.Brin.Bindings[0].Usage.Type
	if keyType.ID() == sourceType.ID() || keyType == sourceType || next.Brin.Bindings[0].Usage.Type != keyType {
		t.Fatal("actual TEMP collision did not remap domain/provider identity", sourceType.ID(), keyType.ID())
	}
	// Both keys come from supported ordinary columns. The source's first
	// range contains the now-deleted 9/id2 extrema; exact destination bounds
	// therefore require admitted repair, not range-ordinal transport.
	idBounds := [][2]int64{{3, 3}, {4, 5}, {6, 6}}
	for ordinal, want := range [][2]int64{{4, 4}, {7, 7}, {2, 2}} {
		r := next.Brin.Ranges[ordinal]
		a, b := brinBounds(t, r.Keys[0])
		idLow, idHigh := brinBounds(t, r.Keys[1])
		if !r.Summarized || [2]int64{a, b} != want || r.Keys[0].HasNull != (ordinal == 1) || [2]int64{idLow, idHigh} != idBounds[ordinal] {
			t.Fatal("repair did not produce compact destination callable/partial truth", ordinal, r)
		}
		if r.Keys[0].Min.Type() != keyType || r.Keys[0].Max.Type() != keyType {
			t.Fatal("locked remint did not reown repaired extrema", ordinal)
		}
		provider := next.Brin.Bindings[0].Comparison
		if _, err := provider.Compare(context.Background(), r.Keys[0].Min, r.Keys[0].Max, sqltype.InputEnvironment{}); err != nil {
			t.Fatal("destination provider did not adopt remapped extrema", err)
		}
		if _, err := provider.Compare(context.Background(), original.Brin.Ranges[0].Keys[0].Min, original.Brin.Ranges[0].Keys[0].Max, sqltype.InputEnvironment{}); err == nil {
			t.Fatal("destination provider accepted source catalog identity")
		}
	}
	// Dump's sequence completion resets the private replay's CREATE INDEX
	// effects. Exactly four live members/five predicates then run in admitted
	// repair; locked remint must not run either recipe a second time.
	if got := brinTruthCounts(t, target); got != [2]int64{counts[0] + 4, counts[1] + 5} {
		t.Fatal("replacement omitted repair or replayed recipes during locked remint", got, counts)
	}
	enginetest.Run(t, target, "INSERT INTO brin_truth VALUES(7,-11),(8,-6),(0,-200)")
	maintained := brinTruthRoot(t, target)
	a, b := brinBounds(t, maintained.Brin.Ranges[2].Keys[0])
	if maintained == next || maintained.Brin.Prefix != 8 || a != 2 || b != 11 || maintained.Brin.Ranges[3].Summarized || brinTruthCounts(t, target) != [2]int64{counts[0] + 6, counts[1] + 8} {
		t.Fatal("destination callable/partial publication did not maintain repaired summaries", maintained, a, b)
	}
	a, b = brinBounds(t, next.Brin.Ranges[2].Keys[0])
	if a != 2 || b != 2 || brinTruthCounts(t, source) != counts || !reflect.DeepEqual(original.Brin.Ranges, ranges) || original.Brin.Prefix != 6 {
		t.Fatal("replacement/publication mutated retained source or imported roots")
	}
}
