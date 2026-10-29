package partitioncmds_test

import (
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/partitioncmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func exclusionCaptureExecute(t *testing.T, e *core.Engine, op *session.Operation, q string, records map[ids.ObjectAddress]partition.ReplayTeardown, prepare bool) parser.Statement {
	t.Helper()
	raw := enginetest.Statement(t, q)
	if alter, ok := raw.(*parser.AlterTable); ok && prepare {
		if err := partitioncmds.PreparePartitionReplayExtension(e, op, alter, records); err != nil {
			t.Fatal("extension Prepare", err)
		}
	}
	result, err := e.Execute(op, raw, 0, nil)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	if result.Rows != nil {
		if err := result.Rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return raw
}

// These are actual successful statement/After transitions on the private import
// engine. A later permitted transition must not renew stale accepted authority
// or modify an earlier capture through its retained slice backing.
func TestPartitionExclusionReplayCaptureExtension(t *testing.T) {
	for _, method := range []string{"RANGE", "LIST", "HASH"} {
		for _, scenario := range []string{"reject_stale_backing", "reject_late_backing", "later_recursive_seed", "later_only_origin"} {
			t.Run(scenario+"_"+method, func(t *testing.T) {
				e, op, records, name := exclusionTeardownFixture(t, true, method)
				child := exclusionTeardownRelation(t, e, "__stuntgres_partition_replay_2")
				target := exclusionTeardownRelation(t, e, "actual_target")
				gap := exclusionTeardownRelation(t, e, "__stuntgres_partition_replay_3")
				accepted := records[child.Address()]
				if !accepted.Complete || len(accepted.Backings) != 1 || len(accepted.Indexes) != 1 {
					t.Fatal("missing initial accepted invalid-child authority", accepted)
				}
				want := accepted
				want.Backings = slices.Clone(accepted.Backings)
				want.Indexes = slices.Clone(accepted.Indexes)
				childIndex := e.CatalogRead().MustRelation(accepted.Backings[0].Index.Address)
				oldIndex := *childIndex
				oldDefinition := *childIndex.SQLIndex
				oldChildConstraint := child.Constraints.Exclusion[0]
				oldTargetConstraint := target.Constraints.Exclusion[0]
				gapStore := e.Storage.Relations[gap.Address()]
				if gapStore == nil {
					t.Fatal("missing actual physical gap storage")
				}
				assertRetained := func() {
					t.Helper()
					if !reflect.DeepEqual(accepted, want) {
						t.Error("later completion mutated retained accepted capture slices")
					}
					if childIndex.Revision() != oldIndex.Revision() || childIndex.Name() != oldIndex.Name() || !reflect.DeepEqual(*childIndex.SQLIndex, oldDefinition) || !reflect.DeepEqual(child.Constraints.Exclusion[0], oldChildConstraint) || !reflect.DeepEqual(target.Constraints.Exclusion[0], oldTargetConstraint) {
						t.Error("later transition mutated retained predecessor graph/backing definitions")
					}
					if e.Storage.Relations[gap.Address()] != gapStore {
						t.Error("capture hook replaced actual physical gap storage")
					}
				}
				seed := "__stuntgres_partition_replay_6"
				if scenario == "reject_stale_backing" {
					roleExecute(t, e, op, "ALTER INDEX public.__stuntgres_partition_replay_4 RENAME TO changed_backing")
				} else if scenario == "later_only_origin" {
					q := "ALTER TABLE ONLY public.__stuntgres_partition_replay_2 ADD CONSTRAINT __stuntgres_partition_replay_6 EXCLUDE USING btree(k WITH =)"
					raw := exclusionCaptureExecute(t, e, op, q, records, true)
					before := e.Catalog.State
					if err := partitioncmds.CapturePartitionReplayAfter(e, op, raw, records); err != nil {
						t.Fatal("legitimate later ONLY origin was rejected", err)
					}
					if e.Catalog.State != before || records[child.Address()].Complete || len(records[child.Address()].Backings) != 2 {
						t.Fatal("later ONLY capture lost its pending actual provider or published catalog state")
					}
					assertRetained()
					seed = "__stuntgres_partition_replay_7"
				}
				q := "ALTER TABLE public.__stuntgres_partition_replay_1 ADD CONSTRAINT " + seed + " EXCLUDE USING btree(k WITH =)"
				if scenario == "reject_stale_backing" {
					before := e.Catalog.State
					candidate := enginetest.Statement(t, q).(*parser.AlterTable)
					if err := partitioncmds.PreparePartitionReplayExtension(e, op, candidate, records); err == nil {
						t.Error("Prepare accepted stale completed backing fence")
					}
					if e.Catalog.State != before || !reflect.DeepEqual(records[child.Address()], want) {
						t.Error("rejected Prepare changed catalog or accepted capture")
					}
				}
				raw := exclusionCaptureExecute(t, e, op, q, records, scenario != "reject_stale_backing")
				if scenario == "reject_late_backing" {
					roleExecute(t, e, op, "ALTER INDEX public.__stuntgres_partition_replay_4 RENAME TO changed_backing")
				}
				before := e.Catalog.State
				err := partitioncmds.CapturePartitionReplayAfter(e, op, raw, records)
				if e.Catalog.State != before {
					t.Error("After capture published catalog state")
				}
				assertRetained()
				if scenario == "reject_stale_backing" || scenario == "reject_late_backing" {
					if err == nil {
						t.Error("unleased or vetoed seed extension was accepted")
					}
					if !reflect.DeepEqual(records[child.Address()], want) {
						t.Error("later seed refreshed stale accepted revisions or inventory")
					}
					beforeDrop := e.Catalog.State
					if err := partitioncmds.ValidatePartitionReplayTeardown(e, op, name, records); err == nil {
						t.Error("later successful seed laundered stale backing into DROP authority")
					}
					if e.Catalog.State != beforeDrop || !reflect.DeepEqual(records[child.Address()], want) {
						t.Error("rejected DROP changed catalog or lost previous accepted capture")
					}
					assertRetained()
					return
				}
				if err != nil {
					t.Fatal("legitimate later recursive seed was rejected", err)
				}
				current := records[child.Address()]
				expectedBackings, expectedIndexes := 1, 3
				if scenario == "later_only_origin" {
					expectedBackings, expectedIndexes = 2, 2
				}
				if !current.Complete || len(current.Backings) != expectedBackings || len(current.Indexes) != expectedIndexes || current.Backings[0] != want.Backings[0] {
					t.Fatal("legitimate extension lost original backing or actual added provider inventory", current)
				}
				if err := partitioncmds.ValidatePartitionReplayTeardown(e, op, name, records); err != nil {
					t.Fatal("legitimate extended capture refused ordinary teardown", err)
				}
				if e.Catalog.State != before || len(records) != 0 {
					t.Fatal("teardown admission published catalog or failed to consume exact capture")
				}
				roleExecute(t, e, op, "DROP TABLE public.__stuntgres_partition_replay_2")
				actual := exclusionTeardownRelation(t, e, "actual_target")
				if len(actual.Partition.Children) != 0 || len(actual.Constraints.Exclusion) != 2 || actual.Constraints.Exclusion[0].OID != oldTargetConstraint.OID || actual.Constraints.Exclusion[0].Index != oldTargetConstraint.Index || e.CatalogRead().MustRelation(oldTargetConstraint.Index).SQLIndex.Valid {
					t.Fatal("ordinary extended teardown replaced or healed original false-invalid provider")
				}
				for _, c := range actual.Constraints.Exclusion {
					if c.NoInherit || c.IsLocal || c.Parent == 0 {
						t.Fatal("extended target lost actual inherited seed linkage", c)
					}
				}
				for _, address := range []ids.ObjectAddress{child.Address(), gap.Address()} {
					if _, exists := e.CatalogRead().Relation(address); exists || e.Storage.Relations[address] != nil {
						t.Fatal("ordinary teardown retained captured child/gap graph or storage", address)
					}
				}
				assertRetainedGraph := func(r *catalog.Relation, expected catalog.ExclusionConstraint) {
					t.Helper()
					if !reflect.DeepEqual(r.Constraints.Exclusion[0], expected) {
						t.Error("DROP mutated retained predecessor constraint graph")
					}
				}
				assertRetainedGraph(child, oldChildConstraint)
				assertRetainedGraph(target, oldTargetConstraint)
				if !reflect.DeepEqual(accepted, want) {
					t.Error("successful teardown mutated retired accepted capture")
				}
			})
		}
	}
}

func TestPartitionExclusionReplayCaptureIncompleteExtension(t *testing.T) {
	e, op, records, name := exclusionTeardownFixture(t, true, "RANGE")
	child := exclusionTeardownRelation(t, e, "__stuntgres_partition_replay_2")
	for _, local := range []string{"__stuntgres_partition_replay_6", "__stuntgres_partition_replay_7"} {
		q := "ALTER TABLE ONLY public.__stuntgres_partition_replay_2 ADD CONSTRAINT " + local + " EXCLUDE USING btree(k WITH =)"
		raw := exclusionCaptureExecute(t, e, op, q, records, true)
		before := e.Catalog.State
		if err := partitioncmds.CapturePartitionReplayAfter(e, op, raw, records); err != nil {
			t.Fatal(err)
		}
		if e.Catalog.State != before {
			t.Fatal("ONLY capture published catalog state")
		}
	}
	pending := records[child.Address()]
	want := pending
	want.Backings = slices.Clone(pending.Backings)
	want.Indexes = slices.Clone(pending.Indexes)
	if pending.Complete || len(pending.Backings) != 3 || pending.Backings[1].Parent != 0 || pending.Backings[2].Parent != 0 {
		t.Fatal("missing two real pending local origins", pending)
	}
	// One recursive seed adopts only one of the two unowned equivalent roots.
	// A permitted partial transition publishes an owned incomplete successor,
	// while previously retained pending capture values remain immutable.
	q := "ALTER TABLE public.__stuntgres_partition_replay_1 ADD CONSTRAINT __stuntgres_partition_replay_8 EXCLUDE USING btree(k WITH =)"
	raw := exclusionCaptureExecute(t, e, op, q, records, true)
	actual := exclusionTeardownRelation(t, e, "__stuntgres_partition_replay_2")
	if actual.Constraints.Exclusion[1].Parent == 0 || actual.Constraints.Exclusion[2].Parent != 0 {
		t.Fatal("one seed did not establish the distinguishing partial adoption")
	}
	before := e.Catalog.State
	if err := partitioncmds.CapturePartitionReplayAfter(e, op, raw, records); err != nil {
		t.Fatal("ordinary partial adoption hook rejected", err)
	}
	successor := records[child.Address()]
	if e.Catalog.State != before || !reflect.DeepEqual(pending, want) || successor.Complete || successor.Backings[1].Parent == 0 || successor.Backings[2].Parent != 0 || successor.Extension != nil {
		t.Error("partial completion lost owned pending successor or mutated old capture")
	}
	if err := partitioncmds.ValidatePartitionReplayTeardown(e, op, name, records); err == nil {
		t.Error("partial adoption gained completed DROP authority")
	}
	if e.Catalog.State != before || !reflect.DeepEqual(pending, want) || !reflect.DeepEqual(records[child.Address()], successor) {
		t.Error("incomplete DROP admission changed catalog or previous capture")
	}
	q = "ALTER TABLE public.__stuntgres_partition_replay_1 ADD CONSTRAINT __stuntgres_partition_replay_9 EXCLUDE USING btree(k WITH =)"
	raw = exclusionCaptureExecute(t, e, op, q, records, true)
	before = e.Catalog.State
	if err := partitioncmds.CapturePartitionReplayAfter(e, op, raw, records); err != nil {
		t.Fatal("final legitimate seed hook rejected", err)
	}
	complete := records[child.Address()]
	if e.Catalog.State != before || !complete.Complete || len(complete.Backings) != 3 || len(complete.Indexes) != 3 {
		t.Fatal("final seed did not complete actual three-provider authority", complete)
	}
	if !reflect.DeepEqual(pending, want) {
		t.Error("successful completion mutated retained pending capture backing")
	}
	if err := partitioncmds.ValidatePartitionReplayTeardown(e, op, name, records); err != nil {
		t.Fatal("fully adopted multi-frontier capture rejected", err)
	}
	if e.Catalog.State != before || len(records) != 0 {
		t.Fatal("completed admission published catalog or retained consumed authority")
	}
	roleExecute(t, e, op, "DROP TABLE public.__stuntgres_partition_replay_2")
	target := exclusionTeardownRelation(t, e, "actual_target")
	if len(target.Partition.Children) != 0 || len(target.Constraints.Exclusion) != 3 {
		t.Fatal("ordinary multi-frontier cleanup lost surviving providers")
	}
	for _, c := range target.Constraints.Exclusion {
		if c.NoInherit || e.CatalogRead().MustRelation(c.Index).SQLIndex.Valid {
			t.Fatal("ordinary cleanup changed false-origin invalidity", c)
		}
	}
}

func TestPartitionExclusionReplayCaptureInitialPeerFence(t *testing.T) {
	e := engine.New().RestoreStage()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	records := make(map[ids.ObjectAddress]partition.ReplayTeardown)
	for _, q := range []string{
		"CREATE TABLE public.actual_target(k INT) PARTITION BY RANGE(k)",
		"CREATE TABLE public.__stuntgres_partition_replay_2 PARTITION OF public.actual_target DEFAULT PARTITION BY RANGE(k)",
		"CREATE TABLE public.__stuntgres_partition_replay_3 PARTITION OF public.__stuntgres_partition_replay_2 DEFAULT",
	} {
		exclusionCaptureExecute(t, e, op, q, records, true)
	}
	q := "ALTER TABLE ONLY public.__stuntgres_partition_replay_2 ADD CONSTRAINT __stuntgres_partition_replay_4 EXCLUDE USING btree(k WITH =)"
	raw := exclusionCaptureExecute(t, e, op, q, records, true)
	if err := partitioncmds.CapturePartitionReplayAfter(e, op, raw, records); err != nil {
		t.Fatal(err)
	}
	child := exclusionTeardownRelation(t, e, "__stuntgres_partition_replay_2")
	accepted := records[child.Address()]
	want := accepted
	want.Backings, want.Indexes = slices.Clone(accepted.Backings), slices.Clone(accepted.Indexes)
	roleExecute(t, e, op, "CREATE ROLE pending_actor")
	roleExecute(t, e, op, "GRANT CREATE ON SCHEMA public TO pending_actor")
	roleExecute(t, e, op, "ALTER TABLE public.actual_target OWNER TO pending_actor")
	roleExecute(t, e, op, "CREATE TABLE public.__stuntgres_partition_replay_1(k INT) PARTITION BY RANGE(k)")
	roleExecute(t, e, op, "ALTER TABLE public.__stuntgres_partition_replay_1 ATTACH PARTITION public.actual_target DEFAULT")
	seed := enginetest.Statement(t, "ALTER TABLE public.__stuntgres_partition_replay_1 ADD CONSTRAINT __stuntgres_partition_replay_5 EXCLUDE USING btree(k WITH =)").(*parser.AlterTable)
	before := e.Catalog.State
	if err := partitioncmds.PreparePartitionReplayExtension(e, op, seed, records); err == nil {
		t.Error("legal initial membership absorbed unrelated captured-peer owner change")
	}
	if e.Catalog.State != before || !reflect.DeepEqual(accepted, want) || !reflect.DeepEqual(records[child.Address()], want) {
		t.Error("rejected initial membership extension changed catalog or retained capture")
	}
}
