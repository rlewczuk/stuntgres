package tablecmds_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
)

func partitionExclusionAdoptionEngine(t *testing.T) *core.Engine {
	t.Helper()
	e := engine.New()
	for _, q := range []string{
		"CREATE TABLE root(k INT) PARTITION BY RANGE(k)",
		"CREATE TABLE leaf PARTITION OF root DEFAULT",
		"ALTER TABLE ONLY root ADD CONSTRAINT parent_idx EXCLUDE USING btree(k WITH =)",
		"ALTER TABLE leaf ADD CONSTRAINT child_idx EXCLUDE USING btree(k WITH =)",
		"INSERT INTO leaf VALUES(1)",
		"CREATE ROLE actor",
	} {
		enginetest.Run(t, e, q)
	}
	return e
}

func TestPartitionExclusionAdoptionPublication(t *testing.T) {
	for _, change := range []string{"revision", "owner", "topology", "authorization", "cancel"} {
		t.Run(change, func(t *testing.T) {
			e := partitionExclusionAdoptionEngine(t)
			op, done := roleOperation(t, e, "postgres")
			defer done()
			child := roleTableRef(t, e, "child_idx").Address
			stage := partitionConstraintAttachStage(t, e, op)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			prepared, err := e.ValidateCatalogStage(ctx, stage)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "revision":
				enginetest.Run(t, e, "ALTER INDEX parent_idx RENAME TO changed")
			case "owner":
				enginetest.Run(t, e, "ALTER TABLE leaf OWNER TO actor")
			case "topology":
				enginetest.Run(t, e, "ALTER TABLE root DETACH PARTITION leaf")
			case "authorization":
				roleExecute(t, e, op, "SET ROLE actor")
			case "cancel":
				cancel()
			}
			before := indexMaintenanceCapture(e)
			_, err = publishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
			if change == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation published candidate", err)
				}
			} else if err == nil {
				t.Fatal("stale adoption published")
			}
			indexMaintenanceUnchanged(t, e, before)
			leaf := e.CatalogRead().MustRelation(roleTableRef(t, e, "leaf").Address)
			if !leaf.Constraints.Exclusion[0].IsLocal || leaf.Constraints.Exclusion[0].Parent != 0 || !leaf.Constraints.Exclusion[0].NoInherit || leaf.Constraints.Exclusion[0].Index != child {
				t.Fatal("rejected publication changed provider")
			}
			switch change {
			case "revision":
				enginetest.Run(t, e, "ALTER INDEX changed RENAME TO parent_idx")
			case "owner":
				enginetest.Run(t, e, "ALTER TABLE leaf OWNER TO postgres")
			case "topology":
				enginetest.Run(t, e, "ALTER TABLE root ATTACH PARTITION leaf DEFAULT")
			case "authorization":
				roleExecute(t, e, op, "RESET ROLE")
			}
			retry := partitionConstraintAttachStage(t, e, op)
			healthy, err := e.ValidateCatalogStage(context.Background(), retry)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := publishCatalog(e, healthy, core.PublicationToken{Epoch: retry.Base.Epoch()}); err != nil {
				t.Fatal("healthy retry", err)
			}
			if got := e.CatalogRead().MustRelation(roleTableRef(t, e, "leaf").Address).Constraints.Exclusion; len(got) != 1 || got[0].Index != child || got[0].IsLocal || !got[0].NoInherit {
				t.Fatal("retry did not adopt once", got)
			}
		})
	}
}

func TestPartitionExclusionAdoptionReplay(t *testing.T) {
	e := partitionExclusionAdoptionEngine(t)
	enginetest.Run(t, e, "ALTER INDEX parent_idx ATTACH PARTITION child_idx")
	before := e.CatalogRead()
	leaf := before.MustRelation(roleTableRef(t, e, "leaf").Address)
	old := leaf.Constraints.Exclusion[0]
	var script bytes.Buffer
	if err := engine.Dump(context.Background(), e, &script); err != nil {
		t.Fatal(err)
	}
	// A retained temporary object forces replacement identities through the real
	// collision/remap path rather than comparing a reconstructed descriptor fixture.
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TEMP TABLE retained(k INT)")
	for range 2 {
		if err := engine.Restore(context.Background(), e, bytes.NewReader(script.Bytes())); err != nil {
			t.Fatal(err)
		}
		view := e.CatalogRead()
		target := view.MustRelation(roleTableRef(t, e, "leaf").Address)
		c := target.Constraints.Exclusion[0]
		idx := view.MustRelation(c.Index)
		parent := view.MustRelation(roleTableRef(t, e, "parent_idx").Address)
		if c.IsLocal || !c.NoInherit || c.Parent != parent.SQLIndex.Constraint.OID || idx.SQLIndex.PartitionParent != parent.Address() || idx.SQLIndex.Constraint.OID != c.OID || &c.Operators[0] != &idx.SQLIndex.Exclusion[0] {
			t.Fatal("replacement lost provenance, immediate edges or operator owner")
		}
		if view.Owner() != before.Owner() || view.Epoch() == before.Epoch() || leaf.Constraints.Exclusion[0].OID != old.OID || &leaf.Constraints.Exclusion[0].Operators[0] != &old.Operators[0] {
			t.Fatal("replacement mutated retained catalog definition")
		}
		if root := e.Storage.IndexRoots[c.Index]; root == nil || len(root.Entries) != 1 {
			t.Fatal("replacement missing physical entries")
		}
		var again bytes.Buffer
		if err := engine.Dump(context.Background(), e, &again); err != nil || !bytes.Equal(script.Bytes(), again.Bytes()) {
			t.Fatal("noncanonical replacement", err, script.String(), again.String())
		}
	}
	stale := e.BeginCatalogStage(before)
	defer e.DiscardCatalogStage(stale)
	prepared, err := e.ValidateCatalogStage(context.Background(), stale)
	if err == nil {
		_, err = publishCatalog(e, prepared, core.PublicationToken{Epoch: before.Epoch()})
	}
	if err == nil {
		t.Fatal("retired epoch accepted")
	}
	partitionExclusionFreshReplay(t)
	partitionExclusionInvalidFreshReplay(t)
	partitionExclusionDroppedInvalidFreshReplay(t)
	partitionExclusionComposedFreshReplay(t)
	partitionExclusionRestoreAdmission(t)
}

func partitionExclusionAdoptionIneligible(t *testing.T) {
	t.Helper()
	for _, state := range []string{"not_ready", "not_live", "invalid_physical", "already_owned"} {
		t.Run(state, func(t *testing.T) {
			e := partitionExclusionAdoptionEngine(t)
			if state == "already_owned" {
				enginetest.Run(t, e, "ALTER TABLE ONLY root ADD CONSTRAINT other_parent EXCLUDE USING btree(k WITH =)")
				enginetest.Run(t, e, "ALTER INDEX other_parent ATTACH PARTITION child_idx")
			}
			op, done := roleOperation(t, e, "postgres")
			defer done()
			before := e.CatalogRead()
			parent := before.MustRelation(roleTableRef(t, e, "parent_idx").Address)
			child := before.MustRelation(roleTableRef(t, e, "child_idx").Address)
			table := before.MustRelation(child.SQLIndex.Parent)
			backing := e.Storage.IndexRoots[child.Address()]
			stage := e.BeginCatalogStage(before)
			defer e.DiscardCatalogStage(stage)
			next, definition := *child, *child.SQLIndex
			next.SQLIndex = &definition
			switch state {
			case "not_ready":
				definition.Ready = false
			case "not_live":
				definition.Live = false
			case "invalid_physical":
				definition.Valid = false
			}
			if state != "already_owned" {
				next.ObjectRevision++
				if err := stage.Replace(&next); err != nil {
					t.Fatal(err)
				}
			}
			e.Mu.Lock()
			fresh, err := index.StagePartitionIndexChild(tablecmds.TableHost{core.DDLHost{E: e, Op: op, CatalogStage: stage}}, before.MustRelation(parent.SQLIndex.Parent), parent, table, true)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if fresh.Address() == child.Address() || fresh.SQLIndex.PartitionParent != parent.Address() {
				t.Fatal("ineligible provider was adopted", state)
			}
			projected := e.StageRead(stage).MustRelation(table.Address()).Constraints.Exclusion
			if len(projected) != len(table.Constraints.Exclusion)+1 || projected[len(projected)-1].NoInherit || projected[len(projected)-1].IsLocal {
				t.Fatal("fresh fallback provenance", projected)
			}
			if e.Catalog.State != before.State() || e.Storage.IndexRoots[child.Address()] != backing || child.SQLIndex.Ready != true || child.SQLIndex.Live != true || !child.SQLIndex.Valid {
				t.Fatal("rejected candidate mutated published backing")
			}
		})
	}
}

func partitionExclusionFreshReplay(t *testing.T) {
	t.Helper()
	for _, shape := range []string{"leaf", "nested", "equivalent", "competing"} {
		t.Run("fresh_"+shape, func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, "CREATE TABLE root(k INT) PARTITION BY RANGE(k)")
			enginetest.Run(t, e, "ALTER TABLE root ADD CONSTRAINT origin EXCLUDE USING btree(k WITH =)")
			if shape == "equivalent" {
				enginetest.Run(t, e, "ALTER TABLE root ADD CONSTRAINT second EXCLUDE USING btree(k WITH =)")
			}
			nested := shape != "leaf"
			suffix := ""
			if nested {
				suffix = " PARTITION BY RANGE(k)"
			}
			enginetest.Run(t, e, "CREATE TABLE detached PARTITION OF root DEFAULT"+suffix)
			if nested {
				enginetest.Run(t, e, "CREATE TABLE physical PARTITION OF detached DEFAULT")
			}
			enginetest.Run(t, e, "INSERT INTO root VALUES(1)")
			enginetest.Run(t, e, "ALTER TABLE root DETACH PARTITION detached")
			enginetest.Run(t, e, "ALTER TABLE root DROP CONSTRAINT origin")
			if shape == "equivalent" {
				enginetest.Run(t, e, "ALTER TABLE root DROP CONSTRAINT second")
			}
			if shape == "competing" {
				enginetest.Run(t, e, "ALTER TABLE detached ADD CONSTRAINT local EXCLUDE USING btree(k WITH =)")
			}
			before := e.CatalogRead()
			source := before.MustRelation(roleTableRef(t, e, "detached").Address)
			count := 1
			if shape == "equivalent" || shape == "competing" {
				count = 2
			}
			if len(source.Constraints.Exclusion) != count {
				t.Fatal("missing real source providers")
			}
			falseCount := 0
			for _, c := range source.Constraints.Exclusion {
				if !c.IsLocal || c.Parent != 0 {
					t.Fatal("source is not detached")
				}
				if !c.NoInherit {
					falseCount++
				}
			}
			wantFalse := count
			if shape == "competing" {
				wantFalse = 1
			}
			if falseCount != wantFalse {
				t.Fatal("source provenance", falseCount, wantFalse)
			}
			projected, replay := partition.ConstraintDumpCatalog(control.Resolver{}, before)
			changed := projected.MustRelation(source.Address())
			for i, c := range changed.Constraints.Exclusion {
				original := source.Constraints.Exclusion[i]
				if c.NoInherit != original.NoInherit || c.IsLocal != original.IsLocal || c.OID != original.OID || c.Index != original.Index || c.Name != replay.Temporary[c.Index] || &c.Operators[0] != &original.Operators[0] {
					t.Fatal("name projection lost actual immutable provenance")
				}
			}
			var script bytes.Buffer
			if err := engine.Dump(context.Background(), e, &script); err != nil {
				t.Fatal(err)
			}
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TEMP TABLE retained_fresh(k INT)")
			for range 2 {
				if err := engine.Restore(context.Background(), e, bytes.NewReader(script.Bytes())); err != nil {
					t.Fatal(err, script.String())
				}
				view := e.CatalogRead()
				target := view.MustRelation(roleTableRef(t, e, "detached").Address)
				if len(target.Constraints.Exclusion) != count {
					t.Fatal("replacement lost multiplicity")
				}
				actualFalse := 0
				for _, c := range target.Constraints.Exclusion {
					if !c.IsLocal || c.Parent != 0 {
						t.Fatal("replacement changed local root ancestry")
					}
					if !c.NoInherit {
						actualFalse++
					}
					idx := view.MustRelation(c.Index)
					if idx.SQLIndex.Constraint.OID != c.OID || &idx.SQLIndex.Exclusion[0] != &c.Operators[0] || idx.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
						t.Fatal("replacement lost backing/operator ownership")
					}
					if err := index.ValidateIndexDefinition(view, idx); err != nil {
						t.Fatal(err)
					}
				}
				if actualFalse != wantFalse {
					t.Fatal("replacement changed independent declaration provenance")
				}
				physical := target
				if nested {
					physical = view.MustRelation(roleTableRef(t, e, "physical").Address)
				}
				if len(physical.Constraints.Exclusion) != count {
					t.Fatal("replacement lost physical providers")
				}
				for _, c := range physical.Constraints.Exclusion {
					idx := view.MustRelation(c.Index)
					root := e.Storage.IndexRoots[c.Index]
					if root == nil || len(root.Entries) != 1 || &idx.SQLIndex.Exclusion[0] != &c.Operators[0] {
						t.Fatal("replacement missing owned physical entries")
					}
					if nested {
						if c.IsLocal || c.NoInherit || c.Parent == 0 || idx.SQLIndex.PartitionParent == (ids.ObjectAddress{}) {
							t.Fatal("replacement changed inherited leaf provenance")
						}
					}
				}
				var again bytes.Buffer
				if err := engine.Dump(context.Background(), e, &again); err != nil || !bytes.Equal(script.Bytes(), again.Bytes()) {
					t.Fatal("fresh recipe not canonical", err, script.String(), again.String())
				}
			}
			if source.Constraints.Exclusion[0].NoInherit || projected.MustRelation(source.Address()).Constraints.Exclusion[0].OID != source.Constraints.Exclusion[0].OID {
				t.Fatal("replacement mutated retained source or projection")
			}
		})
	}
}

func partitionExclusionRestoreAdmission(t *testing.T) {
	t.Helper()
	for _, c := range []struct {
		name, sql string
		accepted  bool
	}{
		{"captured_recursive", "ALTER TABLE public.t ADD CONSTRAINT __stuntgres_partition_key_1 EXCLUDE USING btree(k WITH =)", true},
		{"captured_only", "ALTER TABLE ONLY public.t ADD CONSTRAINT __stuntgres_partition_key_1 EXCLUDE USING btree(k WITH =)", true},
		{"scratch_recursive", "ALTER TABLE public.__stuntgres_partition_replay_1 ADD CONSTRAINT __stuntgres_partition_replay_2 EXCLUDE USING btree(k WITH =)", true},
		{"scratch_only", "ALTER TABLE ONLY public.__stuntgres_partition_replay_1 ADD CONSTRAINT __stuntgres_partition_replay_2 EXCLUDE USING btree(k WITH =)", true},
		{"ordinary_name", "ALTER TABLE public.t ADD CONSTRAINT ordinary EXCLUDE USING btree(k WITH =)", false},
		{"unqualified", "ALTER TABLE t ADD CONSTRAINT __stuntgres_partition_key_1 EXCLUDE USING btree(k WITH =)", false},
		{"seed_on_ordinary_table", "ALTER TABLE public.t ADD CONSTRAINT __stuntgres_partition_replay_2 EXCLUDE USING btree(k WITH =)", false},
		{"ordinary_seed_name", "ALTER TABLE public.__stuntgres_partition_replay_1 ADD CONSTRAINT ordinary EXCLUDE USING btree(k WITH =)", false},
	} {
		t.Run("restore_"+c.name, func(t *testing.T) {
			raw, ok := enginetest.Statement(t, c.sql).(*parser.AlterTable)
			if !ok {
				t.Fatal("expected real ALTER TABLE syntax")
			}
			err := table.RestoreAlterConstraints(raw)
			if c.accepted {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				enginetest.State(t, err, "0A000")
			}
		})
	}
}

func partitionExclusionInvalidFreshReplay(t *testing.T) {
	t.Helper()
	for _, explicit := range []bool{false, true} {
		label := "invalid_fresh_local"
		if explicit {
			label = "invalid_fresh_explicit_edge"
		}
		t.Run(label, func(t *testing.T) {
			e := engine.New()
			for _, q := range []string{
				"CREATE TABLE trunk(k INT) PARTITION BY RANGE(k)",
				"CREATE TABLE branch PARTITION OF trunk DEFAULT PARTITION BY RANGE(k)",
				"CREATE TABLE gap PARTITION OF branch FOR VALUES FROM(0) TO(10)",
				"ALTER TABLE ONLY branch ADD CONSTRAINT invalid_local EXCLUDE USING btree(k WITH =)",
				"CREATE TABLE physical PARTITION OF branch DEFAULT",
				"CREATE TABLE scratch(k INT) PARTITION BY RANGE(k)",
				"ALTER TABLE scratch ATTACH PARTITION trunk DEFAULT",
				"ALTER TABLE scratch ADD CONSTRAINT seed EXCLUDE USING btree(k WITH =)",
				"ALTER TABLE scratch DETACH PARTITION trunk",
				"DROP TABLE scratch",
			} {
				enginetest.Run(t, e, q)
			}
			if explicit {
				for _, q := range []string{
					"CREATE TABLE ancestor(k INT) PARTITION BY RANGE(k)",
					"ALTER TABLE ancestor ATTACH PARTITION trunk DEFAULT",
					"CREATE TABLE ancestor_gap PARTITION OF ancestor FOR VALUES FROM(-10) TO(0)",
					"ALTER TABLE ONLY ancestor ADD CONSTRAINT top EXCLUDE USING btree(k WITH =)",
					"ALTER INDEX top ATTACH PARTITION trunk_k_excl",
				} {
					enginetest.Run(t, e, q)
				}
			}
			enginetest.Run(t, e, "INSERT INTO trunk VALUES(1),(1),(20)")
			before := e.CatalogRead()
			source := before.MustRelation(roleTableRef(t, e, "trunk").Address)
			old := source.Constraints.Exclusion[0]
			var script bytes.Buffer
			if err := engine.Dump(context.Background(), e, &script); err != nil {
				t.Fatal(err)
			}
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TEMP TABLE retained_invalid(k INT)")
			for range 2 {
				if err := engine.Restore(context.Background(), e, bytes.NewReader(script.Bytes())); err != nil {
					t.Fatal(err, script.String())
				}
				view := e.CatalogRead()
				trunk := view.MustRelation(roleTableRef(t, e, "trunk").Address)
				c := trunk.Constraints.Exclusion[0]
				idx := view.MustRelation(c.Index)
				if c.NoInherit || c.IsLocal == explicit || idx.SQLIndex.Valid || &idx.SQLIndex.Exclusion[0] != &c.Operators[0] {
					t.Fatal("false invalid origin lost")
				}
				if explicit {
					parent := view.MustRelation(roleTableRef(t, e, "top").Address)
					if c.Parent != parent.SQLIndex.Constraint.OID || idx.SQLIndex.PartitionParent != parent.Address() || parent.SQLIndex.Valid {
						t.Fatal("explicit invalid edge changed")
					}
				} else if c.Parent != 0 || idx.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
					t.Fatal("independent invalid root acquired an edge")
				}
				branch := view.MustRelation(roleTableRef(t, e, "branch").Address).Constraints.Exclusion[0]
				branchIndex := view.MustRelation(branch.Index)
				if !branch.NoInherit || branch.IsLocal || branch.Parent != c.OID || branchIndex.SQLIndex.PartitionParent != c.Index || branchIndex.SQLIndex.Valid {
					t.Fatal("adopted invalid subtree was healed or remapped incorrectly")
				}
				gap := view.MustRelation(roleTableRef(t, e, "gap").Address)
				if len(gap.Constraints.Exclusion) != 0 {
					t.Fatal("recipe filled a captured gap")
				}
				physical := view.MustRelation(roleTableRef(t, e, "physical").Address).Constraints.Exclusion[0]
				backing := e.Storage.IndexRoots[physical.Index]
				if physical.NoInherit || physical.IsLocal || physical.Parent != branch.OID || backing == nil || len(backing.Entries) != 1 {
					t.Fatal("covered physical backing lost")
				}
				for _, owned := range []*catalog.Relation{idx, branchIndex, view.MustRelation(physical.Index)} {
					if err := index.ValidateIndexDefinition(view, owned); err != nil {
						t.Fatal(err)
					}
				}
				var again bytes.Buffer
				if err := engine.Dump(context.Background(), e, &again); err != nil || !bytes.Equal(script.Bytes(), again.Bytes()) {
					t.Fatal("invalid fresh recipe not canonical", err, script.String(), again.String())
				}
			}
			if source.Constraints.Exclusion[0].NoInherit != old.NoInherit || source.Constraints.Exclusion[0].Parent != old.Parent || &source.Constraints.Exclusion[0].Operators[0] != &old.Operators[0] || &before.MustRelation(old.Index).SQLIndex.Exclusion[0] != &old.Operators[0] {
				t.Fatal("replacement mutated retained invalid definition")
			}
		})
	}
}

func partitionExclusionDroppedInvalidFreshReplay(t *testing.T) {
	t.Helper()
	for _, method := range []string{"RANGE", "LIST", "HASH"} {
		for _, covered := range []bool{false, true} {
			label := "stale_invalid_" + method + "_empty"
			if covered {
				label = "stale_invalid_" + method + "_covered"
			}
			t.Run(label, func(t *testing.T) {
				e := engine.New()
				bound := "DEFAULT"
				if method == "HASH" {
					bound = "FOR VALUES WITH(MODULUS 1,REMAINDER 0)"
				}
				for _, q := range []string{
					"CREATE TABLE trunk(k INT) PARTITION BY " + method + "(k)",
					"CREATE TABLE discarded PARTITION OF trunk " + bound + " PARTITION BY " + method + "(k)",
					"CREATE TABLE discarded_gap PARTITION OF discarded " + bound,
					"ALTER TABLE ONLY discarded ADD CONSTRAINT invalid_local EXCLUDE USING btree(k WITH =)",
					"CREATE TABLE scratch(k INT) PARTITION BY RANGE(k)",
					"ALTER TABLE scratch ATTACH PARTITION trunk DEFAULT",
					"ALTER TABLE scratch ADD CONSTRAINT seed EXCLUDE USING btree(k WITH =)",
					"DROP TABLE discarded",
					"ALTER TABLE scratch DETACH PARTITION trunk",
					"DROP TABLE scratch",
				} {
					enginetest.Run(t, e, q)
				}
				if covered {
					enginetest.Run(t, e, "CREATE TABLE physical PARTITION OF trunk "+bound)
					enginetest.Run(t, e, "INSERT INTO trunk VALUES(1)")
				}
				before := e.CatalogRead()
				source := before.MustRelation(roleTableRef(t, e, "trunk").Address)
				old := source.Constraints.Exclusion[0]
				if old.NoInherit || !old.IsLocal || old.Parent != 0 || before.MustRelation(old.Index).SQLIndex.Valid {
					t.Fatal("source lacks independent false stale-invalid identity")
				}
				var script bytes.Buffer
				if err := engine.Dump(context.Background(), e, &script); err != nil {
					t.Fatal(err)
				}
				s := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, s, "CREATE TEMP TABLE retained_stale(k INT)")
				for range 2 {
					if err := engine.Restore(context.Background(), e, bytes.NewReader(script.Bytes())); err != nil {
						t.Fatal(err, script.String())
					}
					view := e.CatalogRead()
					trunk := view.MustRelation(roleTableRef(t, e, "trunk").Address)
					c := trunk.Constraints.Exclusion[0]
					idx := view.MustRelation(c.Index)
					if c.NoInherit || !c.IsLocal || c.Parent != 0 || idx.SQLIndex.Valid || idx.SQLIndex.PartitionParent != (ids.ObjectAddress{}) || idx.SQLIndex.Constraint.OID != c.OID || &idx.SQLIndex.Exclusion[0] != &c.Operators[0] {
						t.Fatal("replacement healed or changed the real stale-invalid origin")
					}
					if len(trunk.Partition.Children) != len(source.Partition.Children) {
						t.Fatal("temporary members leaked into captured topology")
					}
					if err := index.ValidateIndexDefinition(view, idx); err != nil {
						t.Fatal(err)
					}
					if covered {
						leaf := view.MustRelation(roleTableRef(t, e, "physical").Address)
						child := leaf.Constraints.Exclusion[0]
						backing := view.MustRelation(child.Index)
						root := e.Storage.IndexRoots[child.Index]
						if child.NoInherit || child.IsLocal || child.Parent != c.OID || !backing.SQLIndex.Valid || backing.SQLIndex.PartitionParent != c.Index || backing.SQLIndex.Constraint.OID != child.OID || &child.Operators[0] != &backing.SQLIndex.Exclusion[0] || root == nil || len(root.Entries) != 1 {
							t.Fatal("captured valid physical provider/entries lost")
						}
						if err := index.ValidateIndexDefinition(view, backing); err != nil {
							t.Fatal(err)
						}
					}
					var again bytes.Buffer
					if err := engine.Dump(context.Background(), e, &again); err != nil || !bytes.Equal(script.Bytes(), again.Bytes()) {
						t.Fatal("stale-invalid recipe not canonical", err, script.String(), again.String())
					}
				}
				if source.Constraints.Exclusion[0].OID != old.OID || source.Constraints.Exclusion[0].NoInherit || before.MustRelation(old.Index).SQLIndex.Valid || &source.Constraints.Exclusion[0].Operators[0] != &old.Operators[0] {
					t.Fatal("replacement mutated retained source authority")
				}
			})
		}
	}
}

func partitionExclusionComposedFreshReplay(t *testing.T) {
	t.Helper()
	for _, shape := range []string{"competing_valid", "competing_stale", "interleaved"} {
		t.Run(shape, func(t *testing.T) {
			e := engine.New()
			run := func(q string) { t.Helper(); enginetest.Run(t, e, q) }
			if shape == "interleaved" {
				for _, q := range []string{
					"CREATE TABLE trunk(k INT) PARTITION BY RANGE(k)",
					"CREATE TABLE discarded PARTITION OF trunk DEFAULT PARTITION BY RANGE(k)",
					"CREATE TABLE discarded_gap PARTITION OF discarded DEFAULT",
					"ALTER TABLE ONLY discarded ADD CONSTRAINT dead EXCLUDE USING btree(k WITH =)",
					"CREATE TABLE scratch(k INT) PARTITION BY RANGE(k)",
					"ALTER TABLE scratch ATTACH PARTITION trunk DEFAULT",
					"ALTER TABLE scratch ADD CONSTRAINT stale_seed EXCLUDE USING btree(k WITH =)",
					"DROP TABLE discarded",
					"ALTER INDEX trunk_k_excl RENAME TO stale",
					"CREATE TABLE branch PARTITION OF trunk DEFAULT PARTITION BY RANGE(k)",
					"CREATE TABLE gap PARTITION OF branch FOR VALUES FROM(0) TO(10)",
					"ALTER TABLE ONLY branch ADD CONSTRAINT original_local EXCLUDE USING btree(k WITH =)",
					"CREATE TABLE physical PARTITION OF branch DEFAULT",
					"ALTER TABLE scratch ADD CONSTRAINT retained_seed EXCLUDE USING btree(k WITH =)",
					"ALTER TABLE scratch DETACH PARTITION trunk",
					"DROP TABLE scratch",
					"INSERT INTO trunk VALUES(1),(20)",
				} {
					run(q)
				}
			} else {
				for _, q := range []string{
					"CREATE TABLE physical(k INT)",
					"ALTER TABLE physical ADD CONSTRAINT original_local EXCLUDE USING btree(k WITH =)",
					"CREATE TABLE scratch(k INT) PARTITION BY RANGE(k)",
					"ALTER TABLE scratch ADD CONSTRAINT guard_seed EXCLUDE USING btree(k WITH =)",
					"ALTER TABLE scratch ADD CONSTRAINT fresh_seed EXCLUDE USING btree(k WITH =)",
					"ALTER TABLE scratch ATTACH PARTITION physical DEFAULT",
					"ALTER TABLE scratch DETACH PARTITION physical",
					"DROP TABLE scratch",
					"ALTER INDEX physical_k_excl RENAME TO independent_fresh",
					"CREATE TABLE trunk(k INT) PARTITION BY RANGE(k)",
					"ALTER TABLE trunk ATTACH PARTITION physical DEFAULT",
				} {
					run(q)
				}
				if shape == "competing_stale" {
					run("CREATE TABLE promotion_gap PARTITION OF trunk FOR VALUES FROM(MINVALUE) TO(MAXVALUE)")
				}
				run("ALTER TABLE ONLY trunk ADD CONSTRAINT target EXCLUDE USING btree(k WITH =)")
				run("ALTER INDEX target ATTACH PARTITION original_local")
				if shape == "competing_stale" {
					run("DROP TABLE promotion_gap")
				}
				run("INSERT INTO trunk VALUES(20)")
			}
			before := e.CatalogRead()
			retainedRoot := before.MustRelation(roleTableRef(t, e, "trunk").Address)
			var script bytes.Buffer
			if err := engine.Dump(context.Background(), e, &script); err != nil {
				t.Fatal(err)
			}
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TEMP TABLE retained_composition(k INT)")
			for range 2 {
				if err := engine.Restore(context.Background(), e, bytes.NewReader(script.Bytes())); err != nil {
					t.Fatal(err, script.String())
				}
				view := e.CatalogRead()
				for _, descriptor := range before.Descriptors() {
					source, ok := descriptor.(*catalog.Relation)
					if !ok || len(source.Constraints.Exclusion) == 0 {
						continue
					}
					actual := view.MustRelation(roleTableRef(t, e, source.Name()).Address)
					if len(actual.Constraints.Exclusion) != len(source.Constraints.Exclusion) {
						t.Fatal("provider multiplicity changed", source.Name())
					}
					for i := range source.Constraints.Exclusion {
						expected := &source.Constraints.Exclusion[i]
						var c *catalog.ExclusionConstraint
						for j := range actual.Constraints.Exclusion {
							if actual.Constraints.Exclusion[j].Name == expected.Name {
								c = &actual.Constraints.Exclusion[j]
								break
							}
						}
						if c == nil || c.NoInherit != expected.NoInherit || c.IsLocal != expected.IsLocal {
							t.Fatal("actual provider provenance changed", source.Name(), expected.Name)
						}
						original, idx := before.MustRelation(expected.Index), view.MustRelation(c.Index)
						if idx.Name() != original.Name() || idx.SQLIndex.Valid != original.SQLIndex.Valid || idx.SQLIndex.Ready != original.SQLIndex.Ready || idx.SQLIndex.Live != original.SQLIndex.Live || idx.SQLIndex.Constraint.OID != c.OID || &idx.SQLIndex.Exclusion[0] != &c.Operators[0] {
							t.Fatal("actual backing/flags/operator owner changed", expected.Name)
						}
						if expected.Parent == 0 {
							if c.Parent != 0 || idx.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
								t.Fatal("independent provider acquired an owner", expected.Name)
							}
						} else {
							sourceParent := before.MustRelation(original.SQLIndex.PartitionParent)
							parent := view.MustRelation(roleTableRef(t, e, sourceParent.Name()).Address)
							if c.Parent != parent.SQLIndex.Constraint.OID || idx.SQLIndex.PartitionParent != parent.Address() {
								t.Fatal("immediate owning identities changed", expected.Name)
							}
						}
						if actual.Partition == nil {
							backing := e.Storage.IndexRoots[c.Index]
							if backing == nil || len(backing.Entries) != 1 {
								t.Fatal("actual populated backing missing", expected.Name)
							}
						}
						if err := index.ValidateIndexDefinition(view, idx); err != nil {
							t.Fatal(err)
						}
					}
				}
				var again bytes.Buffer
				if err := engine.Dump(context.Background(), e, &again); err != nil || !bytes.Equal(script.Bytes(), again.Bytes()) {
					t.Fatal("composed replay not canonical", err, script.String(), again.String())
				}
			}
			if &before.MustRelation(retainedRoot.Constraints.Exclusion[0].Index).SQLIndex.Exclusion[0] != &retainedRoot.Constraints.Exclusion[0].Operators[0] {
				t.Fatal("replacement mutated retained operator authority")
			}
		})
	}
}
