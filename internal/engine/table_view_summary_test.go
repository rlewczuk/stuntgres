package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestTableSummaryImmutablePublication(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE summary_parent(n INT)")
	address := roleTableRef(t, e, "summary_parent").Address
	before := e.CatalogRead()
	predecessor := before.MustRelation(address)
	if predecessor.HasIndexes {
		t.Fatal("new table has history")
	}
	run(t, e, "CREATE INDEX summary_index ON summary_parent(n)")
	successor := e.CatalogRead().MustRelation(address)
	if successor == predecessor || !successor.HasIndexes || predecessor.HasIndexes || successor.Revision() == predecessor.Revision() {
		t.Fatal("history was not immutable same-identity publication")
	}
	run(t, e, "DROP INDEX summary_index")
	if !e.CatalogRead().MustRelation(address).HasIndexes || predecessor.HasIndexes {
		t.Fatal("DROP cleared history or changed predecessor")
	}
	run(t, e, "ALTER TABLE summary_parent RENAME COLUMN n TO renamed")
	if !e.CatalogRead().MustRelation(address).HasIndexes {
		t.Fatal("unrelated clone lost history")
	}
	run(t, e, "CREATE TABLE summary_unique(n INT UNIQUE)")
	if !e.CatalogRead().MustRelation(roleTableRef(t, e, "summary_unique").Address).HasIndexes {
		t.Fatal("same-stage UNIQUE parent lost history")
	}
}

func TestTableSummaryReplacementHistory(t *testing.T) {
	source := New()
	run(t, source, "CREATE TABLE summary_former(n INT)")
	run(t, source, "CREATE INDEX summary_old ON summary_former(n)")
	run(t, source, "DROP INDEX summary_old")
	for _, target := range []*Engine{source, New()} {
		before := source.CatalogRead()
		if _, err := target.ReplaceCatalog(context.Background(), source, before); err != nil {
			t.Fatal(err)
		}
		parent := target.CatalogRead().MustRelation(roleTableRef(t, target, "summary_former").Address)
		if !parent.HasIndexes {
			t.Fatal("real descriptor replacement normalized history")
		}
		if !before.MustRelation(roleTableRef(t, source, "summary_former").Address).HasIndexes {
			t.Fatal("retained source mutated")
		}
	}
}

func TestTableSummaryReplacementNormalizesIndexHistory(t *testing.T) {
	for _, same := range []bool{false, true} {
		t.Run(map[bool]string{false: "Foreign", true: "Same"}[same], func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE summary_active(n INT)")
			run(t, e, "CREATE INDEX summary_active_index ON summary_active(n)")
			run(t, e, "CREATE TABLE summary_history(n INT)")
			run(t, e, "CREATE INDEX summary_history_index ON summary_history(n)")
			run(t, e, "DROP INDEX summary_history_index")
			base := e.CatalogRead()
			state := cloneCatalog(base.State())
			address := roleTableRef(t, e, "summary_active").Address
			historyAddress := roleTableRef(t, e, "summary_history").Address
			parent := *state.MustRelation(address)
			parent.HasIndexes = false
			state.SetDescriptor(address, &parent)
			stage := e.BeginCatalogStage(base)
			if err := stage.Replace(&parent); err == nil {
				t.Fatal("same-identity replacement cleared sticky history")
			}
			target := New()
			if same {
				target = e
			}
			if _, err := target.ReplaceCatalog(context.Background(), e, e.catalog.read(state)); err != nil {
				t.Fatal(err)
			}
			current := target.CatalogRead()
			active := current.MustRelation(roleTableRef(t, target, "summary_active").Address)
			history := current.MustRelation(roleTableRef(t, target, "summary_history").Address)
			if !active.HasIndexes || !history.HasIndexes {
				t.Fatal("replacement failed to normalize active index or cleared history-only true")
			}
			if parent.HasIndexes || state.Get(address) != &parent || !base.MustRelation(address).HasIndexes || !base.MustRelation(historyAddress).HasIndexes {
				t.Fatal("normalization mutated supplied descriptors or retained roots")
			}
		})
	}
}

func TestTableSummaryStagedParentAndCancellation(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE summary_stage(n INT)")
	address := roleTableRef(t, e, "summary_stage").Address
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
	e.mu.Lock()
	id, err := e.catalog.allocateOIDLocked()
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	index := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}, ObjectNamespace: parent.Namespace(), ObjectName: "summary_pending", ObjectRevision: 1, Owner: parent.Owner, RelationKind: indexRelation, SQLIndex: &indexDefinition{Parent: address, Live: true}}
	if err := stage.Create(index); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	err = e.markStageIndexHistoryLocked(stage)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	staged := stage.Descriptors[address].(*relationDescriptor)
	if staged.Name() != "summary_renamed" || !staged.HasIndexes || staged == &next || next.HasIndexes || parent.HasIndexes {
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
	e := New()
	catalog := e.CatalogRead()
	source := catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12003})
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
		{12014, "pg_user", false},
		{12007, "pg_roles", false},
		{12008, "pg_group", false},
	} {
		relation, ok := catalog.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: expected.oid})
		if !ok || relation.Name() != expected.name || relation.RelationKind != catalogRelation {
			t.Fatalf("missing represented builtin %s (%d)", expected.name, expected.oid)
		}
		if representedSummaryTable(expected.oid) != expected.table || (representedSummaryViewDefinition(expected.oid) != "") == expected.table {
			t.Fatalf("incorrect table/view classification for %s", expected.name)
		}
	}
	for _, d := range catalog.Descriptors() {
		if relation, ok := d.(*relationDescriptor); ok && relation.RelationKind == catalogRelation {
			table := representedSummaryTable(relation.Address().ObjectID)
			view := representedSummaryViewDefinition(relation.Address().ObjectID) != ""
			if table && view {
				t.Fatal("builtin classified twice")
			}
		}
	}
	e.mu.Lock()
	captured, err := e.captureTableViewSummary(context.Background(), catalog, e.roleRead(catalog, e.roles), ids.ObjectRef{}, source, viewPresentation{})
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range captured.anonymous {
		for i, value := range row {
			if value.Type() != source.Columns[i].Usage.Type {
				t.Fatal("summary type", i, value)
			}
		}
	}
}

func TestTableSummaryColumnACLNeutrality(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER summary_reader")
	roleExecute(t, e, op, "CREATE TABLE summary_denied(a INT,b INT)")
	ref := roleTableRef(t, e, "summary_denied")
	if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	reader, _ := e.Roles().Lookup("summary_reader")
	column := ref
	column.Address.SubID = 1
	grant := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: op.RoleState().CurrentUser, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}
	for _, acl := range []authorization.ACLValue{grant, {Explicit: true}} {
		if err := e.SetObjectACL(context.Background(), column, acl); err != nil {
			t.Fatal(err)
		}
		catalog := e.CatalogRead()
		source := catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12003})
		e.mu.Lock()
		captured, err := e.captureTableViewSummary(context.Background(), catalog, e.roleRead(catalog, e.roles), ids.ObjectRef{}, source, viewPresentation{})
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, row := range captured.anonymous {
			name, err := row[1].Text()
			if err != nil {
				t.Fatal(err)
			}
			if name == "summary_denied" {
				count++
				owner, err := row[2].Text()
				if err != nil || owner != "postgres" || !row[3].IsNull() {
					t.Fatal("ACL affected logical summary", owner, err, row)
				}
			}
		}
		if count != 1 {
			t.Fatal("column ACL changed summary membership", count)
		}
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION summary_reader")
	roleError(t, e, op, "SELECT a FROM summary_denied", "42501", "permission denied for table summary_denied", "")
}

func TestTableSummaryOwnerReplacement(t *testing.T) {
	e := New()
	run(t, e, "CREATE ROLE summary_new_owner")
	run(t, e, "CREATE TABLE summary_owned(n INT)")
	run(t, e, "CREATE INDEX summary_owned_index ON summary_owned(n)")
	base := e.CatalogRead()
	address := roleTableRef(t, e, "summary_owned").Address
	previous := base.MustRelation(address)
	capture := func(catalog CatalogRead) []sqltype.Value {
		source := catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12003})
		e.mu.Lock()
		captured, err := e.captureTableViewSummary(context.Background(), catalog, e.roleRead(catalog, e.roles), ids.ObjectRef{}, source, viewPresentation{})
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range captured.anonymous {
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
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	current := e.CatalogRead()
	row := capture(current)
	owner, err := row[2].Text()
	if err != nil || owner != "summary_new_owner" {
		t.Fatal("current summary owner", owner, err)
	}
	retainedOwner, err := oldRow[2].Text()
	if err != nil || retainedOwner != "postgres" || previous.Owner == newOwner.Ref || !previous.HasIndexes {
		t.Fatal("retained owner/history mutated", retainedOwner, err)
	}
	parent := current.MustRelation(address)
	index := current.MustRelation(roleTableRef(t, e, "summary_owned_index").Address)
	if !parent.HasIndexes || parent.Owner != newOwner.Ref || index.Owner != newOwner.Ref {
		t.Fatal("owner replacement lost history or dependent owner")
	}
}
