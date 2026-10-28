package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func expectEventIndex(t *testing.T, read catalog.Read, want int) {
	t.Helper()
	count := 0
	for a, d := range read.EventTriggers() {
		if d.Address() != a || read.Get(a) != d {
			t.Fatal("event class index is not descriptor-derived")
		}
		count++
	}
	if count != want {
		t.Fatal("event index size", count, want)
	}
	if read.State() == nil {
		return
	}
	all := 0
	for _, d := range read.Descriptors() {
		if _, ok := d.(*catalog.EventTrigger); ok {
			all++
		}
	}
	if all != want {
		t.Fatal("descriptor/index class mismatch", all, want)
	}
}

func eventTriggerIndex(t *testing.T) {
	source := engine.New()
	actor := enginetest.StartSession(t, source, nil)
	empty := source.CatalogRead()
	expectEventIndex(t, empty, 0)
	expectEventIndex(t, catalog.Read{}, 0)
	enginetest.SessionSQL(t, actor, "CREATE FUNCTION et_index_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$")
	enginetest.SessionSQL(t, actor, "CREATE EVENT TRIGGER et_index ON ddl_command_start WHEN TAG IN ('CREATE TABLE') EXECUTE FUNCTION et_index_fn()")
	enabled := source.CatalogRead()
	address := enabled.NameAddress(ids.NameKey{ClassID: ids.EventTriggerClassID, Name: "et_index"})
	original := enabled.Get(address).(*catalog.EventTrigger)
	expectEventIndex(t, enabled, 1)
	expectEventIndex(t, empty, 0)
	enginetest.SessionSQL(t, actor, "ALTER EVENT TRIGGER et_index DISABLE")
	disabled := source.CatalogRead()
	current := disabled.Get(address).(*catalog.EventTrigger)
	if original.FiringMode != catalog.TriggerOrigin || current.FiringMode != catalog.TriggerDisabled || current == original {
		t.Fatal("derived index lost immutable replacement")
	}
	expectEventIndex(t, disabled, 1)
	expectEventIndex(t, enabled, 1)
	for _, tc := range []struct {
		action string
		mode   catalog.TriggerMode
	}{
		{"ENABLE REPLICA", catalog.TriggerReplica}, {"ENABLE ALWAYS", catalog.TriggerAlways}, {"ENABLE", catalog.TriggerOrigin}, {"DISABLE", catalog.TriggerDisabled},
	} {
		predecessor := source.CatalogRead()
		old := predecessor.Get(address).(*catalog.EventTrigger)
		enginetest.SessionSQL(t, actor, "ALTER EVENT TRIGGER et_index "+tc.action)
		live := source.CatalogRead()
		next := live.Get(address).(*catalog.EventTrigger)
		if next == old || next.FiringMode != tc.mode || predecessor.Get(address) != old || original.FiringMode != catalog.TriggerOrigin || current.FiringMode != catalog.TriggerDisabled {
			t.Fatal("mode replacement changed retained index authority")
		}
		expectEventIndex(t, live, 1)
	}
	enginetest.SessionSQL(t, actor, "CREATE ROLE et_index_owner SUPERUSER")
	enginetest.SessionSQL(t, actor, "BEGIN")
	enginetest.SessionSQL(t, actor, "ALTER EVENT TRIGGER et_index RENAME TO et_index_private")
	enginetest.SessionSQL(t, actor, "ALTER EVENT TRIGGER et_index_private OWNER TO et_index_owner")
	private := source.PrivateCatalogLocked(actor.Transactions.Catalog)
	successor := private.Get(address).(*catalog.EventTrigger)
	if successor.Name() != "et_index_private" || successor.Owner == original.Owner || successor.Function != original.Function || successor.FiringMode != catalog.TriggerDisabled || private.NameAddress(ids.NameKey{ClassID: ids.EventTriggerClassID, Name: "et_index"}) != (ids.ObjectAddress{}) {
		t.Fatal("private rename/owner lost descriptor identity")
	}
	expectEventIndex(t, private, 1)
	expectEventIndex(t, enabled, 1)
	enginetest.SessionSQL(t, actor, "ROLLBACK")
	if source.CatalogRead().Get(address).Name() != "et_index" || source.CatalogRead().Get(address).(*catalog.EventTrigger).Owner != original.Owner {
		t.Fatal("rollback retained private identity")
	}
	enginetest.SessionSQL(t, actor, "BEGIN")
	enginetest.SessionSQL(t, actor, "DROP EVENT TRIGGER et_index")
	expectEventIndex(t, source.PrivateCatalogLocked(actor.Transactions.Catalog), 0)
	expectEventIndex(t, source.CatalogRead(), 1)
	enginetest.SessionSQL(t, actor, "ROLLBACK")
	expectEventIndex(t, source.CatalogRead(), 1)
	copy := catalog.CloneState(disabled.State())
	next := *current
	next.ObjectRevision++
	next.FiringMode = catalog.TriggerOrigin
	copy.SetDescriptor(address, &next)
	if copy.Get(address) != &next {
		t.Fatal("setter did not publish its actual descriptor")
	}
	expectEventIndex(t, disabled.WithState(copy), 1)
	copy.RemoveDescriptor(address)
	copy.UnbindName(catalog.DescriptorNameKey(current))
	expectEventIndex(t, disabled.WithState(copy), 0)
	expectEventIndex(t, disabled, 1)
	copy = catalog.CloneState(disabled.State())
	copy.ResetDescriptors()
	expectEventIndex(t, disabled.WithState(copy), 0)
	copy = catalog.CloneState(disabled.State())
	copy.ResetMaps()
	expectEventIndex(t, disabled.WithState(copy), 0)
	destination := engine.New()
	receiver := enginetest.StartSession(t, destination, nil)
	enginetest.SessionSQL(t, receiver, "CREATE TABLE et_index_collision(id integer)")
	if _, err := destination.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	imported := destination.CatalogRead()
	expectEventIndex(t, imported, 1)
	for _, d := range imported.EventTriggers() {
		if d == current || d.Function.Domain() != imported.Domain() || d.Function.Epoch != imported.Epoch() || d.Owner != imported.Owner().Principals.Principal(d.Owner.ID()) {
			t.Fatal("index retained an unremapped foreign identity")
		}
		if err := (catalog.EventTriggerProvider{}).Validate(imported, d); err != nil {
			t.Fatal(err)
		}
	}
	enginetest.SessionSQL(t, actor, "ALTER EVENT TRIGGER et_index RENAME TO et_index_replay")
	enginetest.SessionSQL(t, actor, "ALTER EVENT TRIGGER et_index_replay OWNER TO et_index_owner")
	enginetest.SessionSQL(t, receiver, "CREATE ROLE et_index_owner SUPERUSER")
	for _, tc := range []struct {
		action string
		mode   catalog.TriggerMode
	}{
		{"ENABLE", catalog.TriggerOrigin}, {"ENABLE REPLICA", catalog.TriggerReplica}, {"ENABLE ALWAYS", catalog.TriggerAlways}, {"DISABLE", catalog.TriggerDisabled},
	} {
		enginetest.SessionSQL(t, actor, "ALTER EVENT TRIGGER et_index_replay "+tc.action)
		sourceRead := source.CatalogRead()
		sourceDescriptor := sourceRead.Get(address).(*catalog.EventTrigger)
		retained := destination.CatalogRead()
		if _, err := destination.ReplaceCatalog(context.Background(), source, sourceRead); err != nil {
			t.Fatal(err)
		}
		remapped := destination.CatalogRead()
		expectEventIndex(t, remapped, 1)
		expectEventIndex(t, retained, 1)
		for _, d := range remapped.EventTriggers() {
			if d == sourceDescriptor || d.Name() != "et_index_replay" || d.FiringMode != tc.mode || d.Owner != remapped.Owner().Principals.Principal(d.Owner.ID()) || d.Function.Domain() != remapped.Domain() || d.Function.Epoch != remapped.Epoch() || len(d.Tags) != 1 || d.Tags[0] != "CREATE TABLE" {
				t.Fatal("four-state replacement lost remapped definition")
			}
			if err := (catalog.EventTriggerProvider{}).Validate(remapped, d); err != nil {
				t.Fatal(err)
			}
		}
		if sourceRead.Get(address) != sourceDescriptor || sourceDescriptor.FiringMode != tc.mode || original.FiringMode != catalog.TriggerOrigin {
			t.Fatal("remap mutated source or retained mode")
		}
	}
	enginetest.SessionSQL(t, actor, "ALTER EVENT TRIGGER et_index_replay RENAME TO et_index")
	enginetest.SessionSQL(t, actor, "DROP EVENT TRIGGER et_index")
	expectEventIndex(t, source.CatalogRead(), 0)
	expectEventIndex(t, enabled, 1)
	expectEventIndex(t, imported, 1)
	fresh := engine.New()
	if _, err := destination.ReplaceCatalog(context.Background(), fresh, fresh.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	expectEventIndex(t, destination.CatalogRead(), 0)
	expectEventIndex(t, imported, 1)
}
