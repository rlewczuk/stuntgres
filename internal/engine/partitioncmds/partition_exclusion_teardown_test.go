package partitioncmds_test

import (
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

func exclusionTeardownRelation(t *testing.T, e *core.Engine, name string) *catalog.Relation {
	t.Helper()
	for _, descriptor := range e.CatalogRead().Descriptors() {
		if r, ok := descriptor.(*catalog.Relation); ok && r.Name() == name {
			return r
		}
	}
	t.Fatal("missing actual teardown relation", name)
	return nil
}

func exclusionTeardownFixture(t *testing.T, invalid bool, method string) (*core.Engine, *session.Operation, map[ids.ObjectAddress]partition.ReplayTeardown, parser.ObjectName) {
	t.Helper()
	e := engine.New().RestoreStage()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	records := make(map[ids.ObjectAddress]partition.ReplayTeardown)
	exec := func(q string) parser.Statement {
		t.Helper()
		return exclusionCaptureExecute(t, e, op, q, records, true)
	}
	const scratch = "public.__stuntgres_partition_replay_1"
	const target = "public.actual_target"
	const child = "public.__stuntgres_partition_replay_2"
	const gap = "public.__stuntgres_partition_replay_3"
	const local = "__stuntgres_partition_replay_4"
	const seed = "__stuntgres_partition_replay_5"
	bound := "DEFAULT"
	if method == "HASH" {
		bound = "FOR VALUES WITH(MODULUS 1,REMAINDER 0)"
	}
	if invalid {
		exec("CREATE TABLE " + target + "(k INT) PARTITION BY " + method + "(k)")
		exec("CREATE TABLE " + child + " PARTITION OF " + target + " " + bound + " PARTITION BY " + method + "(k)")
		exec("CREATE TABLE " + gap + " PARTITION OF " + child + " " + bound)
		q := "ALTER TABLE ONLY " + child + " ADD CONSTRAINT " + local + " EXCLUDE USING btree(k WITH =)"
		raw := exec(q)
		if err := partitioncmds.CapturePartitionReplayAfter(e, op, raw, records); err != nil {
			t.Fatal(err)
		}
	} else {
		exec("CREATE TABLE " + target + "(k INT)")
		exec("INSERT INTO " + target + " VALUES(1)")
	}
	exec("CREATE TABLE " + scratch + "(k INT) PARTITION BY RANGE(k)")
	exec("ALTER TABLE " + scratch + " ATTACH PARTITION " + target + " DEFAULT")
	q := "ALTER TABLE " + scratch + " ADD CONSTRAINT " + seed + " EXCLUDE USING btree(k WITH =)"
	executed := exec(q)
	if err := partitioncmds.CapturePartitionReplayAfter(e, op, executed, records); err != nil {
		t.Fatal(err)
	}
	if invalid {
		raw := enginetest.Statement(t, "ALTER TABLE ONLY "+child+" ADD CONSTRAINT unused EXCLUDE USING btree(k WITH =)").(*parser.AlterTable)
		record := records[exclusionTeardownRelation(t, e, "__stuntgres_partition_replay_2").Address()]
		if !record.Complete || record.Kind != partition.ReplayInvalidChild || record.Backings[0].Parent == 0 {
			t.Fatal("actual recursive adoption did not complete capture", record)
		}
		return e, op, records, raw.Name
	}
	q = "ALTER TABLE " + scratch + " DETACH PARTITION " + target
	raw := enginetest.Statement(t, q).(*parser.AlterTableDetachPartition)
	record, err := partitioncmds.CapturePartitionReplayTeardown(e, op, raw)
	if err != nil || record.Kind != partition.ReplaySeedParent || record.Complete {
		t.Fatal("actual pre-detach capture", record, err)
	}
	records[record.Target.Address] = record
	exec(q)
	if err := partitioncmds.CapturePartitionReplayAfter(e, op, raw, records); err != nil {
		t.Fatal(err)
	}
	return e, op, records, raw.Name
}

func TestPartitionExclusionReplayTeardown(t *testing.T) {
	exclusionGuardTeardown(t)
	exclusionPromotionTeardown(t)
	for _, private := range []bool{false, true} {
		label := "public_import_authority_rejected"
		if private {
			label = "legacy_detached_gap"
		}
		t.Run(label, func(t *testing.T) {
			e := engine.New()
			if private {
				e = e.RestoreStage()
			}
			op, done := roleOperation(t, e, "postgres")
			defer done()
			for _, q := range []string{
				"CREATE TABLE public.retained_parent(k INT) PARTITION BY RANGE(k)",
				"CREATE TABLE public.__stuntgres_partition_replay_1 PARTITION OF public.retained_parent DEFAULT",
				"ALTER TABLE ONLY public.retained_parent ADD CONSTRAINT __stuntgres_partition_key_1 EXCLUDE USING btree(k WITH =)",
			} {
				roleExecute(t, e, op, q)
			}
			q := "ALTER TABLE public.retained_parent DETACH PARTITION public.__stuntgres_partition_replay_1"
			raw := enginetest.Statement(t, q).(*parser.AlterTableDetachPartition)
			before := e.Catalog.State
			record, err := partitioncmds.CapturePartitionReplayTeardown(e, op, raw)
			if !private {
				if err == nil || e.Catalog.State != before {
					t.Fatal("public engine gained private teardown authority", err)
				}
				return
			}
			if err != nil || record.Kind != partition.ReplayDetachedGap {
				t.Fatal(record, err)
			}
			records := map[ids.ObjectAddress]partition.ReplayTeardown{record.Target.Address: record}
			if err := partitioncmds.ValidatePartitionReplayTeardown(e, op, raw.Partition, records); err == nil {
				t.Fatal("pre-detach incomplete capture authorized DROP")
			}
			roleExecute(t, e, op, q)
			if err := partitioncmds.CapturePartitionReplayAfter(e, op, raw, records); err != nil {
				t.Fatal(err)
			}
			if err := partitioncmds.ValidatePartitionReplayTeardown(e, op, raw.Partition, records); err != nil {
				t.Fatal(err)
			}
			roleExecute(t, e, op, "DROP TABLE public.__stuntgres_partition_replay_1")
			parent := exclusionTeardownRelation(t, e, "retained_parent")
			if e.CatalogRead().MustRelation(parent.Constraints.Exclusion[0].Index).SQLIndex.Valid {
				t.Fatal("legacy gap cleanup healed retained invalidity")
			}
		})
	}
	t.Run("seed_parent_preserves_populated_backing", func(t *testing.T) {
		e, op, records, name := exclusionTeardownFixture(t, false, "RANGE")
		target := exclusionTeardownRelation(t, e, "actual_target")
		c := target.Constraints.Exclusion[0]
		store, backing := e.Storage.Relations[target.Address()], e.Storage.IndexRoots[c.Index]
		if store == nil || backing == nil || len(backing.Entries) != 1 || c.NoInherit || !c.IsLocal || c.Parent != 0 {
			t.Fatal("missing detached real false provider")
		}
		before := e.Catalog.State
		if err := partitioncmds.ValidatePartitionReplayTeardown(e, op, name, records); err != nil {
			t.Fatal(err)
		}
		if e.Catalog.State != before || len(records) != 0 {
			t.Fatal("admission changed published state or retained authority")
		}
		roleExecute(t, e, op, "DROP TABLE public.__stuntgres_partition_replay_1")
		actual := exclusionTeardownRelation(t, e, "actual_target")
		if actual.Constraints.Exclusion[0].Index != c.Index || e.Storage.Relations[target.Address()] != store || e.Storage.IndexRoots[c.Index] != backing || len(backing.Entries) != 1 {
			t.Fatal("ordinary scratch DROP changed actual target backing")
		}
		if err := partitioncmds.ValidatePartitionReplayTeardown(e, op, name, records); err == nil {
			t.Fatal("consumed capture authorized another DROP")
		}
	})
	for _, method := range []string{"RANGE", "LIST", "HASH"} {
		t.Run("invalid_child_"+method, func(t *testing.T) {
			e, op, records, name := exclusionTeardownFixture(t, true, method)
			target := exclusionTeardownRelation(t, e, "actual_target")
			c := target.Constraints.Exclusion[0]
			if c.NoInherit || e.CatalogRead().MustRelation(c.Index).SQLIndex.Valid {
				t.Fatal("missing actual false invalid parent")
			}
			gap := exclusionTeardownRelation(t, e, "__stuntgres_partition_replay_3").Address()
			child := exclusionTeardownRelation(t, e, "__stuntgres_partition_replay_2")
			childIndex := child.Constraints.Exclusion[0].Index
			if err := partitioncmds.ValidatePartitionReplayTeardown(e, op, name, records); err != nil {
				t.Fatal(err)
			}
			roleExecute(t, e, op, "DROP TABLE public.__stuntgres_partition_replay_2")
			target = exclusionTeardownRelation(t, e, "actual_target")
			if len(target.Partition.Children) != 0 || target.Constraints.Exclusion[0].Index != c.Index || e.CatalogRead().MustRelation(c.Index).SQLIndex.Valid {
				t.Fatal("ordinary dependency closure healed/replaced retained invalid provider")
			}
			for _, address := range []ids.ObjectAddress{child.Address(), childIndex, gap} {
				if _, ok := e.CatalogRead().Relation(address); ok {
					t.Fatal("ordinary DROP retained actual dependent", address)
				}
				if e.Storage.Relations[address] != nil || e.Storage.IndexRoots[address] != nil {
					t.Fatal("ordinary DROP retained actual storage", address)
				}
			}
		})
	}
	for _, shape := range []string{"seed", "invalid"} {
		for _, mutation := range []string{"unpaired", "foreign_engine", "replaced", "owner", "extra_provider", "stale_backing", "populated_scope"} {
			t.Run(shape+"_reject_"+mutation, func(t *testing.T) {
				invalid := shape == "invalid"
				e, op, records, name := exclusionTeardownFixture(t, invalid, "RANGE")
				target := "public.__stuntgres_partition_replay_1"
				backing := "__stuntgres_partition_replay_5"
				if invalid {
					target = "public.__stuntgres_partition_replay_2"
					backing = "__stuntgres_partition_replay_4"
				}
				switch mutation {
				case "unpaired":
					records = nil
				case "foreign_engine":
					other, otherOp, _, _ := exclusionTeardownFixture(t, invalid, "RANGE")
					e, op = other, otherOp
				case "replaced":
					roleExecute(t, e, op, "DROP TABLE "+target)
					roleExecute(t, e, op, "CREATE TABLE "+target+"(k INT) PARTITION BY RANGE(k)")
				case "owner":
					roleExecute(t, e, op, "CREATE ROLE captured_actor")
					roleExecute(t, e, op, "GRANT CREATE ON SCHEMA public TO captured_actor")
					roleExecute(t, e, op, "ALTER TABLE "+target+" OWNER TO captured_actor")
				case "extra_provider":
					roleExecute(t, e, op, "ALTER TABLE ONLY "+target+" ADD CONSTRAINT unexpected_provider EXCLUDE USING btree(k WITH =)")
				case "stale_backing":
					roleExecute(t, e, op, "ALTER INDEX public."+backing+" RENAME TO changed_backing")
				case "populated_scope":
					if invalid {
						roleExecute(t, e, op, "INSERT INTO public.__stuntgres_partition_replay_3 VALUES(1)")
					} else {
						roleExecute(t, e, op, "CREATE TABLE public.unexpected_child PARTITION OF "+target+" DEFAULT")
						roleExecute(t, e, op, "INSERT INTO public.unexpected_child VALUES(1)")
					}
				}
				before := e.Catalog.State
				if err := partitioncmds.ValidatePartitionReplayTeardown(e, op, name, records); err == nil {
					t.Fatal("stale/foreign/populated/unpaired scope gained teardown authority")
				}
				if e.Catalog.State != before {
					t.Fatal("failed admission mutated published catalog")
				}
			})
		}
	}
}

func exclusionGuardTeardown(t *testing.T) {
	t.Helper()
	t.Run("guard_seed_preserves_actual_true_and_false", func(t *testing.T) {
		e := engine.New().RestoreStage()
		op, done := roleOperation(t, e, "postgres")
		defer done()
		for _, q := range []string{
			"CREATE TABLE public.actual_target(k INT)",
			"ALTER TABLE public.actual_target ADD CONSTRAINT original_local EXCLUDE USING btree(k WITH =)",
			"INSERT INTO public.actual_target VALUES(1)",
			"CREATE TABLE public.__stuntgres_partition_replay_1(k INT) PARTITION BY RANGE(k)",
			"ALTER TABLE public.__stuntgres_partition_replay_1 ADD CONSTRAINT __stuntgres_partition_replay_2 EXCLUDE USING btree(k WITH =)",
			"ALTER TABLE public.__stuntgres_partition_replay_1 ADD CONSTRAINT __stuntgres_partition_replay_3 EXCLUDE USING btree(k WITH =)",
			"ALTER TABLE public.__stuntgres_partition_replay_1 ATTACH PARTITION public.actual_target DEFAULT",
		} {
			roleExecute(t, e, op, q)
		}
		source := exclusionTeardownRelation(t, e, "actual_target")
		old, fresh := source.Constraints.Exclusion[0], source.Constraints.Exclusion[1]
		oldRoot, freshRoot := e.Storage.IndexRoots[old.Index], e.Storage.IndexRoots[fresh.Index]
		q := "ALTER TABLE public.__stuntgres_partition_replay_1 DETACH PARTITION public.actual_target"
		raw := enginetest.Statement(t, q).(*parser.AlterTableDetachPartition)
		record, err := partitioncmds.CapturePartitionReplayTeardown(e, op, raw)
		if err != nil || record.Kind != partition.ReplaySeedParent || len(record.Backings) != 2 || !record.Backings[0].NoInherit || record.Backings[1].NoInherit {
			t.Fatal("actual guard/fresh capture lost provenance", record, err)
		}
		records := map[ids.ObjectAddress]partition.ReplayTeardown{record.Target.Address: record}
		roleExecute(t, e, op, q)
		if err := partitioncmds.CapturePartitionReplayAfter(e, op, raw, records); err != nil {
			t.Fatal(err)
		}
		if err := partitioncmds.ValidatePartitionReplayTeardown(e, op, raw.Name, records); err != nil {
			t.Fatal(err)
		}
		roleExecute(t, e, op, "DROP TABLE public.__stuntgres_partition_replay_1")
		target := exclusionTeardownRelation(t, e, "actual_target")
		for i, c := range target.Constraints.Exclusion {
			expected := old
			if i == 1 {
				expected = fresh
			}
			if c.OID != expected.OID || c.Index != expected.Index || c.NoInherit != expected.NoInherit || !c.IsLocal || c.Parent != 0 {
				t.Fatal("guard DROP changed captured real identity", c)
			}
		}
		if oldRoot == nil || freshRoot == nil || e.Storage.IndexRoots[old.Index] != oldRoot || e.Storage.IndexRoots[fresh.Index] != freshRoot || len(oldRoot.Entries) != 1 || len(freshRoot.Entries) != 1 {
			t.Fatal("guard DROP rebuilt or discarded populated backings")
		}
	})
}

func exclusionPromotionTeardown(t *testing.T) {
	t.Helper()
	for _, mutation := range []string{"accepted", "owner", "stale_index", "populated", "extra_index"} {
		t.Run("promotion_gap_"+mutation, func(t *testing.T) {
			e := engine.New().RestoreStage()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			for _, q := range []string{
				"CREATE TABLE public.actual_parent(k INT) PARTITION BY RANGE(k)",
				"CREATE TABLE public.actual_target(k INT)",
				"ALTER TABLE public.actual_target ADD CONSTRAINT chosen EXCLUDE USING btree(k WITH =)",
				"ALTER TABLE public.actual_parent ATTACH PARTITION public.actual_target DEFAULT",
				"CREATE TABLE public.__stuntgres_partition_replay_1 PARTITION OF public.actual_parent FOR VALUES FROM(MINVALUE) TO(MAXVALUE)",
				"ALTER TABLE ONLY public.actual_parent ADD CONSTRAINT parent_key EXCLUDE USING btree(k WITH =)",
				"ALTER INDEX public.parent_key ATTACH PARTITION public.chosen",
			} {
				roleExecute(t, e, op, q)
			}
			q := "ALTER TABLE public.actual_parent DETACH PARTITION public.__stuntgres_partition_replay_1"
			raw := enginetest.Statement(t, q).(*parser.AlterTableDetachPartition)
			record, err := partitioncmds.CapturePartitionReplayTeardown(e, op, raw)
			if err != nil || record.Kind != partition.ReplayPromotionGap {
				t.Fatal("missing actual post-explicit promotion capture", record, err)
			}
			records := map[ids.ObjectAddress]partition.ReplayTeardown{record.Target.Address: record}
			roleExecute(t, e, op, q)
			if err := partitioncmds.CapturePartitionReplayAfter(e, op, raw, records); err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "owner":
				roleExecute(t, e, op, "CREATE ROLE promotion_actor")
				roleExecute(t, e, op, "GRANT CREATE ON SCHEMA public TO promotion_actor")
				roleExecute(t, e, op, "ALTER TABLE public.actual_parent OWNER TO promotion_actor")
			case "stale_index":
				roleExecute(t, e, op, "ALTER INDEX public.parent_key RENAME TO changed_parent_key")
			case "populated":
				roleExecute(t, e, op, "INSERT INTO public.__stuntgres_partition_replay_1 VALUES(1)")
			case "extra_index":
				roleExecute(t, e, op, "ALTER TABLE ONLY public.actual_parent ADD CONSTRAINT unexpected EXCLUDE USING btree(k WITH =)")
			}
			before := e.Catalog.State
			err = partitioncmds.ValidatePartitionReplayTeardown(e, op, raw.Partition, records)
			if mutation != "accepted" {
				if err == nil || e.Catalog.State != before {
					t.Fatal("changed promotion scope gained teardown authority", err)
				}
				return
			}
			if err != nil || e.Catalog.State != before || len(records) != 0 {
				t.Fatal("exact promotion admission changed catalog", err)
			}
			roleExecute(t, e, op, "DROP TABLE public.__stuntgres_partition_replay_1")
			parent := exclusionTeardownRelation(t, e, "actual_parent")
			target := exclusionTeardownRelation(t, e, "actual_target")
			c := target.Constraints.Exclusion[0]
			idx := e.CatalogRead().MustRelation(parent.Constraints.Exclusion[0].Index)
			if idx.SQLIndex.Valid || c.Parent != idx.SQLIndex.Constraint.OID || !c.NoInherit || c.IsLocal || e.CatalogRead().MustRelation(c.Index).SQLIndex.PartitionParent != idx.Address() {
				t.Fatal("ordinary gap DROP changed intended edge or healed parent")
			}
		})
	}
}
