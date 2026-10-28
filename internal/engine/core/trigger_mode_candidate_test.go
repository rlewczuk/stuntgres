package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func triggerModeUnitSetup(t *testing.T) (*core.Engine, *session.Session) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE TABLE mode_parent(id integer PRIMARY KEY,v integer)",
		"CREATE TABLE mode_child(id integer,pid integer,CONSTRAINT mode_fk FOREIGN KEY(pid) REFERENCES mode_parent(id) ON DELETE CASCADE)",
		"CREATE FUNCTION mode_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+100; RETURN NEW; END$$",
		"CREATE TRIGGER a BEFORE INSERT OR UPDATE OF v ON mode_parent FOR EACH ROW WHEN(NEW.v>0) EXECUTE FUNCTION mode_fn('kept')",
	} {
		sessionSQL(t, s, q)
	}
	return e, s
}

func TestTriggerModeCandidate(t *testing.T) {
	e, s := triggerModeUnitSetup(t)
	sessionSQL(t, s, "CREATE TRIGGER z BEFORE INSERT ON mode_parent FOR EACH ROW EXECUTE FUNCTION mode_fn()")
	read := e.CatalogRead()
	ref := enginetest.RoleTableRef(t, e, "mode_parent")
	old := read.MustRelation(ref.Address)
	original := stored.TriggersOf(old)[0]
	before := original.Parts()
	for _, tc := range []struct {
		kind parser.AlterTableActionKind
		mode catalog.TriggerMode
	}{
		{parser.DisableTrigger, catalog.TriggerDisabled}, {parser.EnableReplicaTrigger, catalog.TriggerReplica}, {parser.EnableAlwaysTrigger, catalog.TriggerAlways},
	} {
		action := parser.AlterTableAction{Action: tc.kind, Trigger: parser.Identifier{Text: "a"}}
		candidates, err := table.TriggerModeCandidate(context.Background(), read, old, action, true, true)
		if err != nil || len(candidates) != 1 {
			t.Fatal(candidates, err)
		}
		next := candidates[0]
		got := stored.TriggersOf(next)[0]
		parts := got.Parts()
		parts.Mode = before.Mode
		if next == old || next.Revision() != old.Revision() || got.Mode() != tc.mode || !reflect.DeepEqual(parts, before) {
			t.Fatal("candidate changed definition", next, got)
		}
		if got.Parts().When != before.When || got.Parts().Binding != before.Binding || got.Parts().Presentation != before.Presentation || &got.Parts().Arguments[0] != &before.Arguments[0] || &got.Parts().UpdateColumns[0] != &before.UpdateColumns[0] || &got.Parts().References[0] != &before.References[0] {
			t.Fatal("mode rebuilt executable backing")
		}
		unchanged, prior := stored.TriggersOf(next)[1], stored.TriggersOf(old)[1]
		if &stored.TriggersOf(next)[0] == &stored.TriggersOf(old)[0] || !unchanged.SameExecutable(prior) || unchanged.Mode() != prior.Mode() || &unchanged.Parts().References[0] != &prior.Parts().References[0] {
			t.Fatal("candidate shared changed list or rebuilt unaffected definition")
		}
		same, err := table.TriggerModeCandidate(context.Background(), read, next, action, true, true)
		if err != nil || len(same) != 0 {
			t.Fatal("no-op replaced owner", same, err)
		}
		if stored.TriggersOf(old)[0].Mode() != catalog.TriggerOrigin || read.MustRelation(old.Address()) != old {
			t.Fatal("borrowed catalog mutated")
		}
	}
}

func TestTriggerModeForeignKeyOwnership(t *testing.T) {
	e, s := triggerModeUnitSetup(t)
	sessionSQL(t, s, "CREATE TABLE mode_other_parent(id integer PRIMARY KEY)")
	sessionSQL(t, s, "ALTER TABLE mode_child ADD CONSTRAINT mode_other_fk FOREIGN KEY(id) REFERENCES mode_other_parent(id)")
	read := e.CatalogRead()
	parent := read.MustRelation(enginetest.RoleTableRef(t, e, "mode_parent").Address)
	child := read.MustRelation(enginetest.RoleTableRef(t, e, "mode_child").Address)
	before := child.Constraints.ForeignKeys[0]
	action := parser.AlterTableAction{Action: parser.DisableTrigger, TriggerSelector: parser.TriggerAll}
	changed, err := table.TriggerModeCandidate(context.Background(), read, parent, action, true, true)
	if err != nil || len(changed) != 2 {
		t.Fatal(changed, err)
	}
	for _, next := range changed {
		if next.Address() != child.Address() {
			continue
		}
		fk := next.Constraints.ForeignKeys[0]
		if &next.Constraints.ForeignKeys[0] == &child.Constraints.ForeignKeys[0] || fk.TriggerMode(0) != catalog.TriggerDisabled || fk.TriggerMode(1) != catalog.TriggerDisabled || fk.TriggerMode(2) != catalog.TriggerOrigin || fk.TriggerMode(3) != catalog.TriggerOrigin {
			t.Fatal("incorrect private slot ownership", fk)
		}
		if &fk.Attributes[0] != &before.Attributes[0] || &fk.Referenced[0] != &before.Referenced[0] {
			t.Fatal("mode rebuilt FK attribute backing")
		}
		if !reflect.DeepEqual(next.Constraints.ForeignKeys[1], child.Constraints.ForeignKeys[1]) {
			t.Fatal("mode touched unrelated FK")
		}
		fk.TriggerModes = before.TriggerModes
		if !reflect.DeepEqual(fk, before) {
			t.Fatal("mode damaged FK identity/provider", fk, before)
		}
	}
	if !reflect.DeepEqual(child.Constraints.ForeignKeys[0], before) {
		t.Fatal("borrowed FK mutated")
	}
	if _, err := table.TriggerModeCandidate(context.Background(), read, parent, parser.AlterTableAction{Action: parser.EnableTrigger, TriggerSelector: parser.TriggerAll}, true, false); err == nil {
		t.Fatal("system no-op bypassed authorization")
	}
	onlyUser, err := table.TriggerModeCandidate(context.Background(), read, parent, parser.AlterTableAction{Action: parser.DisableTrigger, TriggerSelector: parser.TriggerUser}, true, false)
	if err != nil || len(onlyUser) != 1 || onlyUser[0].Address() != parent.Address() {
		t.Fatal("USER selected FK owner", onlyUser, err)
	}
	t.Run("changed-owner-fence", func(t *testing.T) {
		e, _ := triggerModeUnitSetup(t)
		base := e.CatalogRead()
		parent := base.MustRelation(enginetest.RoleTableRef(t, e, "mode_parent").Address)
		child := base.MustRelation(enginetest.RoleTableRef(t, e, "mode_child").Address)
		candidates, err := table.TriggerModeCandidate(context.Background(), base, parent, parser.AlterTableAction{Action: parser.DisableTrigger, TriggerSelector: parser.TriggerAll}, true, true)
		if err != nil || len(candidates) != 2 {
			t.Fatal(candidates, err)
		}
		stage := e.BeginCatalogStage(base)
		defer e.DiscardCatalogStage(stage)
		for _, next := range candidates {
			prior := base.MustRelation(next.Address())
			if err := table.BumpAlterRevision(prior, next); err != nil {
				t.Fatal(err)
			}
			if err := stage.ReplaceDescriptor(next, false, false); err != nil {
				t.Fatal(err)
			}
		}
		prepared, err := e.ValidateCatalogStage(context.Background(), stage)
		if err != nil {
			t.Fatal(err)
		}
		competing, err := table.TriggerModeCandidate(context.Background(), base, child, parser.AlterTableAction{Action: parser.DisableTrigger, TriggerSelector: parser.TriggerAll}, false, true)
		if err != nil || len(competing) != 1 {
			t.Fatal(competing, err)
		}
		other := e.BeginCatalogStage(base)
		defer e.DiscardCatalogStage(other)
		if err := table.BumpAlterRevision(child, competing[0]); err != nil {
			t.Fatal(err)
		}
		if err := other.Replace(competing[0]); err != nil {
			t.Fatal(err)
		}
		otherPrepared, err := e.ValidateCatalogStage(context.Background(), other)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := enginetest.PublishCatalog(e, otherPrepared, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
			t.Fatal(err)
		}
		acceptedChild := e.CatalogRead().MustRelation(child.Address())
		changes, err := enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()})
		if err == nil || len(changes.Definitions) != 0 {
			t.Fatal("stale FK owner published", changes, err)
		}
		if e.CatalogRead().MustRelation(parent.Address()) != parent || e.CatalogRead().MustRelation(child.Address()) != acceptedChild || stored.TriggersOf(parent)[0].Mode() != catalog.TriggerOrigin {
			t.Fatal("failed multi-owner publication leaked a prefix")
		}
	})
	t.Run("event-eligibility", triggerModeEventOwnership)
}

func TestTriggerModeBulkCandidate(t *testing.T) {
	t.Run("independent-owner-payloads", func(t *testing.T) {
		e, s := triggerModeUnitSetup(t)
		for _, q := range []string{
			"CREATE TRIGGER b BEFORE INSERT OR UPDATE OF v ON mode_parent FOR EACH ROW WHEN(NEW.v>0) EXECUTE FUNCTION mode_fn('b')",
			"CREATE TRIGGER z BEFORE INSERT OR UPDATE OF v ON mode_parent FOR EACH ROW WHEN(NEW.v>0) EXECUTE FUNCTION mode_fn('z')",
			"ALTER TABLE mode_parent ADD CONSTRAINT mode_self_fk FOREIGN KEY(id) REFERENCES mode_parent(id)",
			"ALTER TABLE mode_child ADD CONSTRAINT mode_second_fk FOREIGN KEY(id) REFERENCES mode_parent(id)",
			"CREATE TABLE mode_other_parent(id integer PRIMARY KEY)",
			"ALTER TABLE mode_child ADD CONSTRAINT mode_unrelated_fk FOREIGN KEY(pid) REFERENCES mode_other_parent(id)",
			"CREATE FUNCTION mode_keep() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
			"CREATE TRIGGER keep BEFORE INSERT ON mode_child FOR EACH ROW EXECUTE FUNCTION mode_keep()",
		} {
			sessionSQL(t, s, q)
		}
		read := e.CatalogRead()
		parent := read.MustRelation(enginetest.RoleTableRef(t, e, "mode_parent").Address)
		child := read.MustRelation(enginetest.RoleTableRef(t, e, "mode_child").Address)
		originalUsers := stored.TriggersOf(parent)
		parentKeys := append([]catalog.ForeignKeyConstraint(nil), parent.Constraints.ForeignKeys...)
		childKeys := append([]catalog.ForeignKeyConstraint(nil), child.Constraints.ForeignKeys...)
		action := parser.AlterTableAction{Action: parser.DisableTrigger, TriggerSelector: parser.TriggerAll}
		candidates, err := table.TriggerModeCandidate(context.Background(), read, parent, action, true, true)
		if err != nil || len(candidates) != 2 { t.Fatal("bulk owner selection", candidates, err) }
		var nextParent, nextChild *catalog.Relation
		for _, next := range candidates {
			switch next.Address() {
			case parent.Address(): nextParent = next
			case child.Address(): nextChild = next
			default: t.Fatal("unrelated owner selected", next)
			}
		}
		if nextParent == nil || nextChild == nil || nextParent == parent || nextChild == child { t.Fatal("missing private owner envelope") }
		users := stored.TriggersOf(nextParent)
		if len(users) != 3 || &users[0] == &originalUsers[0] { t.Fatal("bulk user payload was not detached") }
		for i, trigger := range users {
			before, after := originalUsers[i].Parts(), trigger.Parts()
			after.Mode = before.Mode
			if trigger.Mode() != catalog.TriggerDisabled || originalUsers[i].Mode() != catalog.TriggerOrigin || !reflect.DeepEqual(before, after) || !trigger.SameExecutable(originalUsers[i]) {
				t.Fatal("bulk user edit damaged source or definition", i, before, after)
			}
			if trigger.Parts().When != before.When || trigger.Parts().Binding != before.Binding || trigger.Parts().Presentation != before.Presentation ||
				&trigger.Parts().Arguments[0] != &before.Arguments[0] || &trigger.Parts().UpdateColumns[0] != &before.UpdateColumns[0] || &trigger.Parts().References[0] != &before.References[0] {
				t.Fatal("bulk user edit rebuilt executable backing", i)
			}
		}
		if &stored.TriggersOf(nextChild)[0] != &stored.TriggersOf(child)[0] { t.Fatal("FK-only owner copied its user payload") }
		for _, pair := range []struct{ before, after *catalog.Relation }{{parent, nextParent}, {child, nextChild}} {
			if &pair.before.Constraints.ForeignKeys[0] == &pair.after.Constraints.ForeignKeys[0] { t.Fatal("bulk FK payload was borrowed") }
			for i, key := range pair.after.Constraints.ForeignKeys {
				before := pair.before.Constraints.ForeignKeys[i]
				for slot := range key.TriggerOIDs {
					want := catalog.TriggerOrigin
					if key.Name != "mode_unrelated_fk" && (pair.before == parent || slot < 2) { want = catalog.TriggerDisabled }
					if key.TriggerMode(slot) != want || before.TriggerMode(slot) != catalog.TriggerOrigin { t.Fatal("bulk FK slot ownership", key.Name, slot, key.TriggerMode(slot), want) }
				}
				if &key.Attributes[0] != &before.Attributes[0] || &key.Referenced[0] != &before.Referenced[0] { t.Fatal("bulk FK edit rebuilt column backing") }
				key.TriggerModes = before.TriggerModes
				if !reflect.DeepEqual(key, before) { t.Fatal("bulk FK edit damaged identity/provider", key.Name) }
			}
		}
		if !reflect.DeepEqual(parent.Constraints.ForeignKeys, parentKeys) || !reflect.DeepEqual(child.Constraints.ForeignKeys, childKeys) { t.Fatal("bulk candidate mutated borrowed FK arrays") }
		onlyUser, err := table.TriggerModeCandidate(context.Background(), read, parent, parser.AlterTableAction{Action: parser.DisableTrigger, TriggerSelector: parser.TriggerUser}, false, false)
		if err != nil || len(onlyUser) != 1 || &onlyUser[0].Constraints.ForeignKeys[0] != &parent.Constraints.ForeignKeys[0] { t.Fatal("user-only edit copied FK payload", onlyUser, err) }
		stage := e.BeginCatalogStage(read)
		defer e.DiscardCatalogStage(stage)
		for _, next := range candidates {
			if err := table.BumpAlterRevision(read.MustRelation(next.Address()), next); err != nil { t.Fatal(err) }
			if err := stage.ReplaceDescriptor(next, false, false); err != nil { t.Fatal(err) }
		}
		current := e.StageRead(stage)
		stagedParent := current.MustRelation(parent.Address())
		unchanged, err := table.TriggerModeCandidate(context.Background(), current, stagedParent, action, true, true)
		if err != nil || len(unchanged) != 0 || e.StageRead(stage).MustRelation(parent.Address()) != stagedParent { t.Fatal("bulk no-op replaced staged owner", unchanged, err) }
		if _, err := table.TriggerModeCandidate(context.Background(), current, stagedParent, action, true, false); err == nil { t.Fatal("bulk system no-op bypassed authorization") }
	})
	t.Run("multiple-clone-lineages", func(t *testing.T) {
		e, s := triggerModeUnitSetup(t)
		for _, q := range []string{
			"CREATE TABLE mode_bulk_tree(id integer,v integer) PARTITION BY RANGE(id)",
			"CREATE TABLE mode_bulk_branch PARTITION OF mode_bulk_tree FOR VALUES FROM (0) TO (10) PARTITION BY RANGE(id)",
			"CREATE TABLE mode_bulk_leaf PARTITION OF mode_bulk_branch FOR VALUES FROM (0) TO (10)",
			"CREATE TRIGGER a BEFORE INSERT ON mode_bulk_tree FOR EACH ROW EXECUTE FUNCTION mode_fn('a')",
			"CREATE TRIGGER b BEFORE INSERT ON mode_bulk_tree FOR EACH ROW EXECUTE FUNCTION mode_fn('b')",
			"CREATE TRIGGER local BEFORE INSERT ON mode_bulk_leaf FOR EACH ROW EXECUTE FUNCTION mode_fn('local')",
		} {
			sessionSQL(t, s, q)
		}
		read := e.CatalogRead()
		root := read.MustRelation(enginetest.RoleTableRef(t, e, "mode_bulk_tree").Address)
		candidates, err := table.TriggerModeCandidate(context.Background(), read, root, parser.AlterTableAction{Action: parser.DisableTrigger, TriggerSelector: parser.TriggerAll}, true, true)
		if err != nil || len(candidates) != 3 { t.Fatal("bulk clone selection", candidates, err) }
		for _, next := range candidates {
			before := read.MustRelation(next.Address())
			prior, users := stored.TriggersOf(before), stored.TriggersOf(next)
			if &prior[0] == &users[0] { t.Fatal("bulk clone payload was borrowed") }
			for i, trigger := range users {
				want := catalog.TriggerDisabled
				if trigger.Name() == "local" { want = catalog.TriggerOrigin }
				if trigger.Mode() != want || prior[i].Mode() != catalog.TriggerOrigin || !trigger.SameExecutable(prior[i]) ||
					trigger.Parts().Parent != prior[i].Parts().Parent || &trigger.Parts().Arguments[0] != &prior[i].Parts().Arguments[0] {
					t.Fatal("bulk clone lineage or executable changed", next.Name(), trigger.Name())
				}
			}
		}
	})
}

func TestTriggerModeComposition(t *testing.T) {
	e, s := triggerModeUnitSetup(t)
	for _, q := range []string{
		"CREATE TRIGGER b BEFORE INSERT OR UPDATE OF v ON mode_parent FOR EACH ROW WHEN(NEW.v>0) EXECUTE FUNCTION mode_fn('b')",
		"CREATE TRIGGER z BEFORE INSERT OR UPDATE OF v ON mode_parent FOR EACH ROW WHEN(NEW.v>0) EXECUTE FUNCTION mode_fn('z')",
		"ALTER TABLE mode_parent ADD CONSTRAINT mode_self_a FOREIGN KEY(id) REFERENCES mode_parent(id)",
		"ALTER TABLE mode_parent ADD CONSTRAINT mode_self_b FOREIGN KEY(id) REFERENCES mode_parent(id)",
	} { sessionSQL(t, s, q) }
	read := e.CatalogRead()
	original := read.MustRelation(enginetest.RoleTableRef(t, e, "mode_parent").Address)
	candidates, err := table.TriggerModeCandidate(context.Background(), read, original,
		parser.AlterTableAction{Action: parser.DisableTrigger, TriggerSelector: parser.TriggerAll}, false, true)
	if err != nil { t.Fatal(err) }
	var source *catalog.Relation
	for _, next := range candidates { if next.Address() == original.Address() { source = next } }
	if source == nil { t.Fatal("missing staged source") }
	// A structural successor has dropped the first user definition and FK.
	// Survivors retain identity but no longer have their source slice indices.
	ordinary := *original
	ordinary.Triggers = stored.TriggersPayload(stored.TriggersOf(original)[1:])
	ordinary.Constraints.ForeignKeys = original.Constraints.ForeignKeys[1:]
	composed := table.PreserveTriggerModes(source, &ordinary)
	if composed == &ordinary || composed == source || &composed.Constraints.ForeignKeys[0] == &ordinary.Constraints.ForeignKeys[0] ||
		&stored.TriggersOf(composed)[0] == &stored.TriggersOf(&ordinary)[0] { t.Fatal("composition borrowed changed backing") }
	for i, trigger := range stored.TriggersOf(composed) {
		prior := stored.TriggersOf(&ordinary)[i]
		if trigger.Mode() != catalog.TriggerDisabled || prior.Mode() != catalog.TriggerOrigin || !trigger.SameExecutable(prior) ||
			trigger.Parts().When != prior.Parts().When || trigger.Parts().Binding != prior.Parts().Binding ||
			&trigger.Parts().Arguments[0] != &prior.Parts().Arguments[0] { t.Fatal("composition lost surviving user identity") }
	}
	for i, key := range composed.Constraints.ForeignKeys {
		prior := ordinary.Constraints.ForeignKeys[i]
		for slot := range key.TriggerOIDs {
			if key.TriggerMode(slot) != catalog.TriggerDisabled || prior.TriggerMode(slot) != catalog.TriggerOrigin { t.Fatal("composition lost surviving FK mode") }
		}
		key.TriggerModes = prior.TriggerModes
		if !reflect.DeepEqual(key, prior) || &key.Attributes[0] != &prior.Attributes[0] { t.Fatal("composition changed provider identity/backing") }
	}
	if table.PreserveTriggerModes(source, composed) != composed { t.Fatal("unchanged composition replaced its owner") }
	for _, trigger := range stored.TriggersOf(source) { if trigger.Mode() != catalog.TriggerDisabled { t.Fatal("composition mutated staged source") } }
	if read.MustRelation(original.Address()) != original { t.Fatal("composition replaced borrowed catalog owner") }
}
