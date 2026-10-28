package core_test

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestTriggerModeRestoreReplay(t *testing.T) {
	e,s := triggerModeUnitSetup(t)
	sessionSQL(t,s,"ALTER TABLE mode_parent DISABLE TRIGGER ALL")
	for fact := range catalog.ForeignKeyTriggerFacts(e.CatalogRead()) {
		if fact.Slot >= 2 {
			sessionSQL(t,s,"ALTER TABLE mode_child ENABLE REPLICA TRIGGER "+catalog.QuoteIdentifier(fact.Name))
		}
	}
	oldRead := e.CatalogRead()
	childRef := enginetest.RoleTableRef(t,e,"mode_child")
	parentRef := enginetest.RoleTableRef(t,e,"mode_parent")
	oldChild := oldRead.MustRelation(childRef.Address)
	oldFK := oldChild.Constraints.ForeignKeys[0]
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(),e,&dump); err != nil { t.Fatal(err) }
	var records []stored.PositionedForeignKeyMode
	_,err := parser.ParseWithOptions(dump.String(),parser.Options{StandardConformingStrings:true,LineComment:func(comment string,position parser.Position) error {
		record,recognized,err := stored.DecodeForeignKeyMode(comment)
		if recognized && err == nil {
			records = append(records,stored.PositionedForeignKeyMode{Record:record,Start:position.Offset-2,End:position.Offset+len(comment)})
		}
		return err
	}})
	if err != nil || len(records) != 4 { t.Fatal(records,err) }
	if matched,err := stored.ValidateForeignKeyModeFooter(dump.String(),records); err != nil || !matched { t.Fatal("actual writer seal",matched,err) }
	for slot,entry := range records {
		if entry.Record.Slot != slot || entry.Record.OwnerTable != "mode_child" || entry.Record.Constraint != oldFK.Name { t.Fatal("unstable logical footer ordering",records) }
	}
	if err := engine.Restore(context.Background(),e,bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	current := e.CatalogRead()
	nextChild := current.MustRelation(enginetest.RoleTableRef(t,e,"mode_child").Address)
	nextFK := nextChild.Constraints.ForeignKeys[0]
	if current.Epoch() == oldRead.Epoch() || nextChild == oldChild || nextFK.TriggerOIDs == oldFK.TriggerOIDs || nextFK.TriggerMode(0) != catalog.TriggerDisabled || nextFK.TriggerMode(1) != catalog.TriggerDisabled || nextFK.TriggerMode(2) != catalog.TriggerReplica || nextFK.TriggerMode(3) != catalog.TriggerReplica { t.Fatal("restore retained stale RI identity or lost modes",nextFK,oldFK) }
	seen := make(map[ids.OID]bool)
	for fact := range catalog.ForeignKeyTriggerFacts(current) {
		if fact.Owner != nextChild.Address() || fact.Constraint.OID != nextFK.OID || fact.OID != nextFK.TriggerOIDs[fact.Slot] || seen[fact.OID] { t.Fatal("foreign replay owner",fact) }
		seen[fact.OID] = true
	}
	if len(seen) != 4 || oldRead.MustRelation(childRef.Address) != oldChild || oldChild.Constraints.ForeignKeys[0].TriggerOIDs != oldFK.TriggerOIDs || stored.TriggersOf(oldRead.MustRelation(parentRef.Address))[0].Mode() != catalog.TriggerDisabled { t.Fatal("restore mutated retained source ownership") }
	var redump bytes.Buffer
	if err := engine.Dump(context.Background(),e,&redump); err != nil || !bytes.Equal(dump.Bytes(),redump.Bytes()) { t.Fatal("reminted identities changed native canonical dump",err) }
	base := e.CatalogRead()
	storage := e.Storage.Relations[nextChild.Address()]
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	first := records[0]
	end := first.End
	if end < dump.Len() && dump.String()[end] == '\n' { end++ }
	broken := dump.String()[:first.Start]+dump.String()[end:]
	if err := engine.Restore(context.Background(),e,strings.NewReader(broken)); err == nil { t.Fatal("partial snapshot accepted") }
	if e.CatalogRead().State() != base.State() || e.Storage.Relations[nextChild.Address()] != storage { t.Fatal("rejected replay changed target owners") }
	appended := dump.String()+"INSERT INTO public.mode_child VALUES(9,999);\n"
	if matched,err := stored.ValidateForeignKeyModeFooter(appended,records); err != nil || matched { t.Fatal("appended SQL retained waiver",matched,err) }
	err = engine.Restore(context.Background(),e,strings.NewReader(appended))
	nullCountUnitDiagnostic(t, err, sqlstate.Error{
		Code: "23503", Message: `insert or update on table "mode_child" violates foreign key constraint "mode_fk"`,
		Detail: `Key (pid)=(999) is not present in table "mode_parent".`,
		Schema: "public", Table: "mode_child", Constraint: "mode_fk",
	})
	if e.CatalogRead().State() != base.State() || e.Storage.Relations[nextChild.Address()] != storage { t.Fatal("failed ordinary validation replaced target") }
	if _,err := e.ValidateCatalogStage(context.Background(),stage); err != nil { t.Fatal("rejected replay invalidated prior stage",err) }
	t.Run("bulk-installer", triggerModeRestoreBulkOwnership)
}

func triggerModeRestoreBulkOwnership(t *testing.T) {
	e, s := triggerModeUnitSetup(t)
	for _, q := range []string{
		"ALTER TABLE mode_child ADD CONSTRAINT mode_second_fk FOREIGN KEY(id) REFERENCES mode_parent(id)",
		"CREATE TABLE mode_other_parent(id integer PRIMARY KEY)",
		"ALTER TABLE mode_child ADD CONSTRAINT mode_not_valid_fk FOREIGN KEY(pid) REFERENCES mode_other_parent(id) NOT VALID",
		"CREATE FUNCTION mode_keep() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
		"CREATE TRIGGER keep BEFORE INSERT ON mode_child FOR EACH ROW EXECUTE FUNCTION mode_keep()",
	} { sessionSQL(t, s, q) }
	read := e.CatalogRead()
	child := read.MustRelation(enginetest.RoleTableRef(t, e, "mode_child").Address)
	before := append([]catalog.ForeignKeyConstraint(nil), child.Constraints.ForeignKeys...)
	records, err := stored.ForeignKeyModeRecords(context.Background(), read)
	if err != nil || len(records) != 12 { t.Fatal("complete multi-FK snapshot", len(records), err) }
	for i := range records { records[i].Seal = strings.Repeat("0", 64) }
	install := func(records []stored.ForeignKeyModeReplay) error {
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		// Exercise the actual installed/nonpending binder without granting a
		// historical-data waiver from this installer-only metadata fixture.
		return tablecmds.BindRestoredForeignKeys(e, context.Background(), op, nil, records, false)
	}
	if err := install(records); err != nil { t.Fatal(err) }
	if e.CatalogRead().MustRelation(child.Address()) != child { t.Fatal("all-default replay replaced an unchanged owner") }
	expected := map[string][4]catalog.TriggerMode{
		"mode_fk": {catalog.TriggerDisabled, catalog.TriggerReplica, catalog.TriggerAlways, catalog.TriggerOrigin},
		"mode_second_fk": {catalog.TriggerAlways, catalog.TriggerOrigin, catalog.TriggerReplica, catalog.TriggerDisabled},
		"mode_not_valid_fk": {catalog.TriggerOrigin, catalog.TriggerOrigin, catalog.TriggerOrigin, catalog.TriggerOrigin},
	}
	for i := range records { records[i].Mode = expected[records[i].Constraint][records[i].Slot].String() }
	// Input order is not authority: constraint resolution must work for
	// complete groups even when records are not in canonical dump order.
	for left, right := 0, len(records)-1; left < right; left, right = left+1, right-1 { records[left], records[right] = records[right], records[left] }
	base := e.CatalogRead().State()
	for _, invalid := range [][]stored.ForeignKeyModeReplay{
		records[:len(records)-1],
		append(append([]stored.ForeignKeyModeReplay(nil), records...), records[0]),
	} {
		if err := install(invalid); err == nil { t.Fatal("invalid multi-FK group installed") }
		if e.CatalogRead().State() != base || e.CatalogRead().MustRelation(child.Address()) != child || !reflect.DeepEqual(child.Constraints.ForeignKeys, before) {
			t.Fatal("failed replay mutated borrowed owner")
		}
	}
	if err := install(records); err != nil { t.Fatal(err) }
	next := e.CatalogRead().MustRelation(child.Address())
	if next == child || &next.Constraints.ForeignKeys[0] == &child.Constraints.ForeignKeys[0] ||
		&stored.TriggersOf(next)[0] != &stored.TriggersOf(child)[0] { t.Fatal("replay payload detachment") }
	for i, key := range next.Constraints.ForeignKeys {
		prior := before[i]
		for slot := range key.TriggerOIDs {
			if key.TriggerMode(slot) != expected[key.Name][slot] { t.Fatal("multi-FK mode lost", key.Name, slot) }
		}
		if &key.Attributes[0] != &prior.Attributes[0] || &key.Referenced[0] != &prior.Referenced[0] { t.Fatal("replay rebuilt provider backing") }
		key.TriggerModes = prior.TriggerModes
		if !reflect.DeepEqual(key, prior) { t.Fatal("replay changed FK identity, provider or validity", key.Name) }
	}
	if !reflect.DeepEqual(child.Constraints.ForeignKeys, before) { t.Fatal("replay mutated retained FK payload") }
	if err := install(records); err != nil { t.Fatal(err) }
	if e.CatalogRead().MustRelation(child.Address()) != next { t.Fatal("nondefault no-op replay replaced an unchanged owner") }
}
