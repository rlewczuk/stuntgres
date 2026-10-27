package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func TestDroppedForeignKeyIdentities(t *testing.T) {
	e := engine.New()
	for _, q := range []string{
		"CREATE FUNCTION dropped_ri_owner() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$",
		"CREATE EVENT TRIGGER dropped_ri_owner ON sql_drop EXECUTE FUNCTION dropped_ri_owner()",
		"CREATE TABLE dropped_ri_parent(i integer PRIMARY KEY)",
		"CREATE TABLE dropped_ri_child(i integer REFERENCES dropped_ri_parent(i))",
	} {
		enginetest.Run(t, e, q)
	}
	base := e.CatalogRead()
	parent := enginetest.RoleTableRef(t, e, "dropped_ri_parent")
	child := enginetest.RoleTableRef(t, e, "dropped_ri_child")
	relation := base.MustRelation(child.Address)
	key := relation.Constraints.ForeignKeys[0]
	reserved := map[ids.OID]bool{}
	catalog.ReserveDefinitionOIDs(reserved, relation)
	seen := map[ids.OID]bool{}
	for slot, oid := range key.TriggerOIDs {
		if oid == 0 || seen[oid] || !reserved[oid] || !catalog.DefinitionOIDUsed(relation, oid) {
			t.Fatal("unowned or duplicate RI identity", slot, oid)
		}
		seen[oid] = true
	}
	facts := 0
	for fact := range catalog.ForeignKeyTriggerFacts(base) {
		if fact.Constraint.OID != key.OID {
			continue
		}
		want := child.Address
		if fact.Slot < 2 {
			want = parent.Address
		}
		if fact.Relation != want || fact.Owner != child.Address || fact.OID != key.TriggerOIDs[fact.Slot] {
			t.Fatal("RI slot owner", fact)
		}
		facts++
	}
	if facts != 4 {
		t.Fatal("RI fact completeness", facts)
	}
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	stage.MarkDrop(parent.Address, catalog.DropProvenance{Original: true})
	if err := stage.Remove(parent); err != nil {
		t.Fatal(err)
	}
	if err := core.StageDropProvider(context.Background(), stage, nil, nil, nil).ExpressionDependencies([]ids.ObjectAddress{parent.Address}, parser.DropCascade, ids.ObjectAddress{}); err != nil {
		t.Fatal(err)
	}
	captured := map[ids.OID]bool{}
	for _, record := range stage.DropRecords {
		if record.Address.ClassID == 2620 && seen[record.Address.ObjectID] {
			if record.Original || record.Normal || record.Kind != "trigger" {
				t.Fatal("RI INTERNAL provenance", record)
			}
			captured[record.Address.ObjectID] = true
		}
	}
	if len(captured) != 4 || stage.Removed(child.Address) {
		t.Fatal("FK retirement omitted implementation identities or removed survivor", captured)
	}
	if len(base.MustRelation(child.Address).Constraints.ForeignKeys) != 1 {
		t.Fatal("retirement mutated published predecessor")
	}
}

func TestDroppedForeignKeyRemap(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE dropped_remap_parent(i integer PRIMARY KEY)")
	enginetest.Run(t, e, "CREATE TABLE dropped_remap_child(i integer REFERENCES dropped_remap_parent(i))")
	base := e.CatalogRead()
	parent := enginetest.RoleTableRef(t, e, "dropped_remap_parent")
	child := enginetest.RoleTableRef(t, e, "dropped_remap_child")
	original := base.MustRelation(child.Address)
	for _, exhausted := range []bool{false, true} {
		candidate := base.WithState(catalog.CloneState(base.State()))
		copy := *original
		copy.Constraints.ForeignKeys = append([]catalog.ForeignKeyConstraint(nil), original.Constraints.ForeignKeys...)
		copy.Constraints.ForeignKeys[0].TriggerOIDs[0] = parent.Address.ObjectID
		candidate.State().SetDescriptor(child.Address, &copy)
		failure := errors.New("identity frontier exhausted")
		err := control.RemapDefinitionOIDs(candidate, func() (ids.OID, error) {
			if exhausted {
				return 0, failure
			}
			return 900000, nil
		})
		if exhausted {
			if !errors.Is(err, failure) {
				t.Fatal("RI remap lost allocator failure", err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			remapped := candidate.MustRelation(child.Address).Constraints.ForeignKeys[0]
			if remapped.OID != original.Constraints.ForeignKeys[0].OID || remapped.TriggerOIDs[0] != 900000 {
				t.Fatal("RI collision skipped when FK identity stayed unchanged", remapped)
			}
			if err := catalog.ValidateDefinitionOIDs(candidate); err != nil {
				t.Fatal(err)
			}
		}
		if base.MustRelation(child.Address) != original || e.Catalog.State != base.State() || original.Constraints.ForeignKeys[0].TriggerOIDs[0] == parent.Address.ObjectID {
			t.Fatal("private RI remap mutated retained authority")
		}
	}
}
