package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestTableSummaryReplacementHistory(t *testing.T) {
	source := engine.New()
	enginetest.Run(t, source, "CREATE TABLE summary_former(n INT)")
	enginetest.Run(t, source, "CREATE INDEX summary_old ON summary_former(n)")
	enginetest.Run(t, source, "DROP INDEX summary_old")
	for _, target := range []*core.Engine{source, engine.New()} {
		before := source.CatalogRead()
		if _, err := target.ReplaceCatalog(context.Background(), source, before); err != nil {
			t.Fatal(err)
		}
		parent := target.CatalogRead().MustRelation(enginetest.RoleTableRef(t, target, "summary_former").Address)
		if !parent.History.HasIndexes {
			t.Fatal("real descriptor replacement normalized history")
		}
		if !before.MustRelation(enginetest.RoleTableRef(t, source, "summary_former").Address).History.HasIndexes {
			t.Fatal("retained source mutated")
		}
	}
}

func TestTableSummaryStagedParentAndCancellation(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE summary_stage(n INT)")
	address := enginetest.RoleTableRef(t, e, "summary_stage").Address
	base := e.CatalogRead()
	parent := base.MustRelation(address)
	stage := e.BeginCatalogStage(base)
	next := *parent
	next.ObjectName = "summary_renamed"
	next.ObjectRevision++
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	// Genuine private initial-invalid index metadata; history depends on
	// publication, not readiness or a successful physical build.
	e.Mu.Lock()
	id, err := e.Catalog.AllocateOIDLocked()
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	index := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}, ObjectNamespace: parent.Namespace(), ObjectName: "summary_pending", ObjectRevision: 1, Owner: parent.Owner, RelationKind: catalog.IndexRelation, SQLIndex: &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, Parent: address, Live: true}}
	if err := stage.Create(index); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	err = stage.MarkIndexHistoryLocked(e.Catalog.RevisionLocked, stage.Replace)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	staged := stage.Get(address).(*catalog.Relation)
	if staged.Name() != "summary_renamed" || !staged.History.HasIndexes || staged == &next || next.History.HasIndexes || parent.History.HasIndexes {
		t.Fatal("staged parent changes lost or mutated")
	}
	if e.CatalogRead().State() != base.State() {
		t.Fatal("private history prematurely published")
	}
	// The invalid index fails ordinary candidate validation; nothing is installed.
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil || e.CatalogRead().State() != base.State() {
		t.Fatal("invalid candidate published", err)
	}
	e.DiscardCatalogStage(stage)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	empty := e.BeginCatalogStage(base)
	if _, err := e.ValidateCatalogStage(canceled, empty); !errors.Is(err, context.Canceled) || e.CatalogRead().State() != base.State() {
		t.Fatal("cancellation changed catalog", err)
	}
}

func TestTableViewSummaryBuiltinIdentity(t *testing.T) {
	e := engine.New()
	catalogView := e.CatalogRead()
	source := catalogView.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12003})
	if source.RowType != sqltype.ID(12035) || len(source.Columns) != 8 {
		t.Fatal("pg_tables rowtype/schema", source.RowType, source.Columns)
	}
	for _, expected := range []struct {
		oid   ids.OID
		name  string
		table bool
	}{
		{1247, "pg_type", true},
		{1249, "pg_attribute", true},
		{2600, "pg_aggregate", true},
		{2605, "pg_cast", true},
		{1262, "pg_database", true},
		{3079, "pg_extension", true},
		{2612, "pg_language", true},
		{3541, "pg_range", true},
		{3576, "pg_transform", true},
		{3602, "pg_ts_config", true},
		{3601, "pg_ts_parser", true},
		{2224, "pg_sequence", true},
		{2608, "pg_depend", true},
		{2609, "pg_description", true},
		{2396, "pg_shdescription", true},
		{2606, "pg_constraint", true},
		{2604, "pg_attrdef", true},
		{1259, "pg_class", true},
		{2618, "pg_rewrite", true},
		{12000, "pg_views", false},
		{12001, "pg_prepared_xacts", false},
		{12002, "pg_indexes", false},
		{12003, "pg_tables", false},
		{12006, "pg_matviews", false},
		{pgcatalog.RulesOID, "pg_rules", false},
		{pgcatalog.StatAllIndexesOID, "pg_stat_all_indexes", false},
		{12014, "pg_user", false},
		{12007, "pg_roles", false},
		{12008, "pg_group", false},
	} {
		relation, ok := catalogView.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: expected.oid})
		if !ok || relation.Name() != expected.name || relation.RelationKind != catalog.CatalogRelation {
			t.Fatalf("missing represented builtin %s (%d)", expected.name, expected.oid)
		}
		if pgcatalog.SummaryTable(expected.oid) != expected.table || (pgcatalog.SummaryViewDefinition(expected.oid) != "") == expected.table {
			t.Fatalf("incorrect table/view classification for %s", expected.name)
		}
	}
	for _, d := range catalogView.Descriptors() {
		if relation, ok := d.(*catalog.Relation); ok && relation.RelationKind == catalog.CatalogRelation {
			table := pgcatalog.SummaryTable(relation.Address().ObjectID)
			view := pgcatalog.SummaryViewDefinition(relation.Address().ObjectID) != ""
			if table && view {
				t.Fatal("builtin classified twice")
			}
		}
	}
	e.Mu.Lock()
	captured, err := catalogrt.CaptureRows(pgcatalog.Input{Context: context.Background(), Catalog: catalogView, Roles: e.RoleStore.PublishedRead(&e.Catalog.Owner, catalogView), Ref: ids.ObjectRef{}, Source: source, Owner: &e.Catalog.Owner, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Presentation: stored.Presentation{}})}, captureTableViewSummary)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range captured.Anonymous {
		for i, value := range row {
			if value.Type() != source.Columns[i].Usage.Type {
				t.Fatal("summary type", i, value)
			}
		}
	}
}
