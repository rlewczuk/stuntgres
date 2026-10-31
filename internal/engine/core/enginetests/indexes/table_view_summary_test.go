package indexes_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestTableSummaryImmutablePublication(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE summary_parent(n INT)")
	address := enginetest.RoleTableRef(t, e, "summary_parent").Address
	before := e.CatalogRead()
	predecessor := before.MustRelation(address)
	if predecessor.History.HasIndexes {
		t.Fatal("new table has history")
	}
	enginetest.Run(t, e, "CREATE INDEX summary_index ON summary_parent(n)")
	successor := e.CatalogRead().MustRelation(address)
	if successor == predecessor || !successor.History.HasIndexes || predecessor.History.HasIndexes || successor.Revision() == predecessor.Revision() {
		t.Fatal("history was not immutable same-identity publication")
	}
	enginetest.Run(t, e, "DROP INDEX summary_index")
	if !e.CatalogRead().MustRelation(address).History.HasIndexes || predecessor.History.HasIndexes {
		t.Fatal("DROP cleared history or changed predecessor")
	}
	enginetest.Run(t, e, "ALTER TABLE summary_parent RENAME COLUMN n TO renamed")
	if !e.CatalogRead().MustRelation(address).History.HasIndexes {
		t.Fatal("unrelated clone lost history")
	}
	enginetest.Run(t, e, "CREATE TABLE summary_unique(n INT UNIQUE)")
	if !e.CatalogRead().MustRelation(enginetest.RoleTableRef(t, e, "summary_unique").Address).History.HasIndexes {
		t.Fatal("same-stage UNIQUE parent lost history")
	}
}

func TestTableSummaryReplacementNormalizesIndexHistory(t *testing.T) {
	for _, same := range []bool{false, true} {
		t.Run(map[bool]string{false: "Foreign", true: "Same"}[same], func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, "CREATE TABLE summary_active(n INT)")
			enginetest.Run(t, e, "CREATE INDEX summary_active_index ON summary_active(n)")
			enginetest.Run(t, e, "CREATE TABLE summary_history(n INT)")
			enginetest.Run(t, e, "CREATE INDEX summary_history_index ON summary_history(n)")
			enginetest.Run(t, e, "DROP INDEX summary_history_index")
			base := e.CatalogRead()
			state := catalog.CloneState(base.State())
			address := enginetest.RoleTableRef(t, e, "summary_active").Address
			historyAddress := enginetest.RoleTableRef(t, e, "summary_history").Address
			parent := *state.MustRelation(address)
			parent.History.HasIndexes = false
			state.SetDescriptor(address, &parent)
			stage := e.BeginCatalogStage(base)
			if err := stage.Replace(&parent); err == nil {
				t.Fatal("same-identity replacement cleared sticky history")
			}
			target := engine.New()
			if same {
				target = e
			}
			if _, err := target.ReplaceCatalog(context.Background(), e, e.Catalog.Read(state)); err != nil {
				t.Fatal(err)
			}
			current := target.CatalogRead()
			active := current.MustRelation(enginetest.RoleTableRef(t, target, "summary_active").Address)
			history := current.MustRelation(enginetest.RoleTableRef(t, target, "summary_history").Address)
			if !active.History.HasIndexes || !history.History.HasIndexes {
				t.Fatal("replacement failed to normalize active index or cleared history-only true")
			}
			if parent.History.HasIndexes || state.Get(address) != &parent || !base.MustRelation(address).History.HasIndexes || !base.MustRelation(historyAddress).History.HasIndexes {
				t.Fatal("normalization mutated supplied descriptors or retained roots")
			}
		})
	}
}

func TestTableSummaryOwnerReplacement(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE ROLE summary_new_owner")
	enginetest.Run(t, e, "CREATE TABLE summary_owned(n INT)")
	enginetest.Run(t, e, "CREATE INDEX summary_owned_index ON summary_owned(n)")
	base := e.CatalogRead()
	address := enginetest.RoleTableRef(t, e, "summary_owned").Address
	previous := base.MustRelation(address)
	capture := func(catalog catalog.Read) []sqltype.Value {
		source := catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12003})
		e.Mu.Lock()
		captured, err := core.CaptureRows(pgcatalog.Input{Context: context.Background(), Catalog: catalog, Roles: e.RoleStore.PublishedRead(&e.Catalog.Owner, catalog), Ref: ids.ObjectRef{}, Source: source, Owner: &e.Catalog.Owner, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Presentation: stored.Presentation{}})}, captureTableViewSummary)
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range captured.Anonymous {
			name, err := row[1].Text()
			if err != nil {
				t.Fatal(err)
			}
			if name == "summary_owned" {
				return row
			}
		}
		t.Fatal("missing owned table summary")
		return nil
	}
	oldRow := capture(base)
	newOwner, _ := e.Roles().Lookup("summary_new_owner")
	next := *previous
	next.Owner = newOwner.Ref
	next.ObjectRevision++
	stage := e.BeginCatalogStage(base)
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	current := e.CatalogRead()
	row := capture(current)
	owner, err := row[2].Text()
	if err != nil || owner != "summary_new_owner" {
		t.Fatal("current summary owner", owner, err)
	}
	retainedOwner, err := oldRow[2].Text()
	if err != nil || retainedOwner != "postgres" || previous.Owner == newOwner.Ref || !previous.History.HasIndexes {
		t.Fatal("retained owner/history mutated", retainedOwner, err)
	}
	parent := current.MustRelation(address)
	index := current.MustRelation(enginetest.RoleTableRef(t, e, "summary_owned_index").Address)
	if !parent.History.HasIndexes || parent.Owner != newOwner.Ref || index.Owner != newOwner.Ref {
		t.Fatal("owner replacement lost history or dependent owner")
	}
}

// Unit tests name producers by their former engine identifiers. Each one is
// resolved through the real pgcatalog registry, so a missing registration
// fails at package initialization instead of silently testing another path.
var (
	captureAvailableExtensions = enginetest.RegisteredCatalogProducer(12081)
	captureDataTypePrivileges  = enginetest.RegisteredCatalogProducer(16201)
	captureExtensionCatalog    = enginetest.RegisteredCatalogProducer(1255)
	capturePGAM                = enginetest.RegisteredCatalogProducer(2601)
	capturePGAggregate         = enginetest.RegisteredCatalogProducer(2600)
	capturePGAttribute         = enginetest.RegisteredCatalogProducer(1249)
	capturePGCast              = enginetest.RegisteredCatalogProducer(2605)
	capturePGCollation         = enginetest.RegisteredCatalogProducer(3456)
	capturePGConstraint        = enginetest.RegisteredCatalogProducer(2606)
	capturePGCursors           = enginetest.RegisteredCatalogProducer(12077)
	capturePGDatabase          = enginetest.RegisteredCatalogProducer(1262)
	capturePGDefaultACL        = enginetest.RegisteredCatalogProducer(826)
	capturePGDepend            = enginetest.RegisteredCatalogProducer(2608)
	capturePGDescription       = enginetest.RegisteredCatalogProducer(2609)
	capturePGEnum              = enginetest.RegisteredCatalogProducer(3501)
	capturePGGroup             = enginetest.RegisteredCatalogProducer(12008)
	capturePGIndexes           = enginetest.RegisteredCatalogProducer(12002)
	capturePGNamespace         = enginetest.RegisteredCatalogProducer(2615)
	capturePGOpclass           = enginetest.RegisteredCatalogProducer(2616)
	capturePGOperator          = enginetest.RegisteredCatalogProducer(2617)
	capturePGParameterACL      = enginetest.RegisteredCatalogProducer(6243)
	capturePGRange             = enginetest.RegisteredCatalogProducer(3541)
	capturePGRoles             = enginetest.RegisteredCatalogProducer(12007)
	capturePGSequence          = enginetest.RegisteredCatalogProducer(2224)
	capturePGSettings          = enginetest.RegisteredCatalogProducer(16202)
	capturePGShDescription     = enginetest.RegisteredCatalogProducer(2396)
	capturePGTablespace        = enginetest.RegisteredCatalogProducer(1213)
	capturePGType              = enginetest.RegisteredCatalogProducer(1247)
	capturePGUser              = enginetest.RegisteredCatalogProducer(12014)
	capturePreparedSource      = enginetest.RegisteredCatalogProducer(12001)
	captureTablePrivileges     = enginetest.RegisteredCatalogProducer(16200)
	captureTableViewSummary    = enginetest.RegisteredCatalogProducer(12000)
	captureTimezoneSource      = enginetest.RegisteredCatalogProducer(12004)
)
