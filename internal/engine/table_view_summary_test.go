package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestTableSummaryImmutablePublication(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE summary_parent(n INT)")
	address := roleTableRef(t, e, "summary_parent").Address
	before := e.CatalogRead()
	predecessor := before.state.descriptors[address].(*relationDescriptor)
	if predecessor.hasIndexes {
		t.Fatal("new table has history")
	}
	run(t, e, "CREATE INDEX summary_index ON summary_parent(n)")
	successor := e.CatalogRead().state.descriptors[address].(*relationDescriptor)
	if successor == predecessor || !successor.hasIndexes || predecessor.hasIndexes || successor.revision == predecessor.revision {
		t.Fatal("history was not immutable same-identity publication")
	}
	run(t, e, "DROP INDEX summary_index")
	if !e.CatalogRead().state.descriptors[address].(*relationDescriptor).hasIndexes || predecessor.hasIndexes {
		t.Fatal("DROP cleared history or changed predecessor")
	}
	run(t, e, "ALTER TABLE summary_parent RENAME COLUMN n TO renamed")
	if !e.CatalogRead().state.descriptors[address].(*relationDescriptor).hasIndexes {
		t.Fatal("unrelated clone lost history")
	}
	run(t, e, "CREATE TABLE summary_unique(n INT UNIQUE)")
	if !e.CatalogRead().state.descriptors[roleTableRef(t, e, "summary_unique").Address].(*relationDescriptor).hasIndexes {
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
		if _, err := target.ReplaceCatalog(context.Background(), before); err != nil {
			t.Fatal(err)
		}
		parent := target.CatalogRead().state.descriptors[roleTableRef(t, target, "summary_former").Address].(*relationDescriptor)
		if !parent.hasIndexes {
			t.Fatal("real descriptor replacement normalized history")
		}
		if !before.state.descriptors[roleTableRef(t, source, "summary_former").Address].(*relationDescriptor).hasIndexes {
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
			state := cloneCatalog(base.state)
			address := roleTableRef(t, e, "summary_active").Address
			historyAddress := roleTableRef(t, e, "summary_history").Address
			parent := *state.descriptors[address].(*relationDescriptor)
			parent.hasIndexes = false
			state.descriptors[address] = &parent
			stage := e.BeginCatalogStage(base)
			if err := stage.Replace(&parent); err == nil {
				t.Fatal("same-identity replacement cleared sticky history")
			}
			target := New()
			if same {
				target = e
			}
			if _, err := target.ReplaceCatalog(context.Background(), CatalogRead{owner: e, state: state}); err != nil {
				t.Fatal(err)
			}
			current := target.CatalogRead()
			active := current.state.descriptors[roleTableRef(t, target, "summary_active").Address].(*relationDescriptor)
			history := current.state.descriptors[roleTableRef(t, target, "summary_history").Address].(*relationDescriptor)
			if !active.hasIndexes || !history.hasIndexes {
				t.Fatal("replacement failed to normalize active index or cleared history-only true")
			}
			if parent.hasIndexes || state.descriptors[address] != &parent || !base.state.descriptors[address].(*relationDescriptor).hasIndexes || !base.state.descriptors[historyAddress].(*relationDescriptor).hasIndexes {
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
	parent := base.state.descriptors[address].(*relationDescriptor)
	stage := e.BeginCatalogStage(base)
	next := *parent
	next.name = "summary_renamed"
	next.revision++
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	// Genuine private initial-invalid index metadata; history depends on
	// publication, not readiness or a successful physical build.
	e.mu.Lock()
	id, err := e.allocateOIDLocked()
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	index := &relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: id}, namespace: parent.namespace, name: "summary_pending", revision: 1, owner: parent.owner, relationKind: indexRelation, sqlIndex: &indexDefinition{parent: address, live: true}}
	if err := stage.Create(index); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	err = e.markStageIndexHistoryLocked(stage)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	staged := stage.descriptors[address].(*relationDescriptor)
	if staged.name != "summary_renamed" || !staged.hasIndexes || staged == &next || next.hasIndexes || parent.hasIndexes {
		t.Fatal("staged parent changes lost or mutated")
	}
	if e.CatalogRead().state != base.state {
		t.Fatal("private history prematurely published")
	}
	// The invalid index fails ordinary candidate validation; nothing is installed.
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil || e.CatalogRead().state != base.state {
		t.Fatal("invalid candidate published", err)
	}
	e.DiscardCatalogStage(stage)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	empty := e.BeginCatalogStage(base)
	if _, err := e.ValidateCatalogStage(canceled, empty); !errors.Is(err, context.Canceled) || e.CatalogRead().state != base.state {
		t.Fatal("cancellation changed catalog", err)
	}
}

func TestTableViewSummaryBuiltinIdentity(t *testing.T) {
	e := New()
	catalog := e.CatalogRead()
	source := catalog.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: 12003}].(*relationDescriptor)
	if source.rowType != sqltype.ID(12035) || len(source.columns) != 8 {
		t.Fatal("pg_tables rowtype/schema", source.rowType, source.columns)
	}
	for _, expected := range []struct {
		oid   OID
		name  string
		table bool
	}{
		{1249, "pg_attribute", true},
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
		relation, ok := catalog.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: expected.oid}].(*relationDescriptor)
		if !ok || relation.name != expected.name || relation.relationKind != catalogRelation {
			t.Fatalf("missing represented builtin %s (%d)", expected.name, expected.oid)
		}
		if representedSummaryTable(expected.oid) != expected.table || (representedSummaryViewDefinition(expected.oid) != "") == expected.table {
			t.Fatalf("incorrect table/view classification for %s", expected.name)
		}
	}
	for _, d := range catalog.state.descriptors {
		if relation, ok := d.(*relationDescriptor); ok && relation.relationKind == catalogRelation {
			table := representedSummaryTable(relation.address.ObjectID)
			view := representedSummaryViewDefinition(relation.address.ObjectID) != ""
			if table && view {
				t.Fatal("builtin classified twice")
			}
		}
	}
	e.mu.Lock()
	captured, err := e.captureTableViewSummary(context.Background(), catalog, e.roleRead(catalog, e.roles), ObjectRef{}, source, viewPresentation{})
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range captured.anonymous {
		for i, value := range row {
			if value.Type() != source.columns[i].Usage.Type {
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
		source := catalog.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: 12003}].(*relationDescriptor)
		e.mu.Lock()
		captured, err := e.captureTableViewSummary(context.Background(), catalog, e.roleRead(catalog, e.roles), ObjectRef{}, source, viewPresentation{})
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
	previous := base.state.descriptors[address].(*relationDescriptor)
	capture := func(catalog CatalogRead) []sqltype.Value {
		source := catalog.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: 12003}].(*relationDescriptor)
		e.mu.Lock()
		captured, err := e.captureTableViewSummary(context.Background(), catalog, e.roleRead(catalog, e.roles), ObjectRef{}, source, viewPresentation{})
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
	next.owner = newOwner.Ref
	next.revision++
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
	if err != nil || retainedOwner != "postgres" || previous.owner == newOwner.Ref || !previous.hasIndexes {
		t.Fatal("retained owner/history mutated", retainedOwner, err)
	}
	parent := current.state.descriptors[address].(*relationDescriptor)
	index := current.state.descriptors[roleTableRef(t, e, "summary_owned_index").Address].(*relationDescriptor)
	if !parent.hasIndexes || parent.owner != newOwner.Ref || index.owner != newOwner.Ref {
		t.Fatal("owner replacement lost history or dependent owner")
	}
}
