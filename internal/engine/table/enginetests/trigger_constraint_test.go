package enginetests_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestTriggerDeferredConstraintNames(t *testing.T) {
	domain := new(ids.Domain)
	address := func(class, oid ids.OID) ids.ObjectAddress { return ids.ObjectAddress{ClassID: class, ObjectID: oid} }
	owner := &catalog.Relation{ObjectAddress: address(ids.RelationClassID, 9100001), ObjectName: "t", ObjectRevision: 7, RelationKind: catalog.TableRelation}
	p := stored.TriggerParts{OID: 9100010, Name: "tr", Relation: domain.Ref(1, owner.Address()), Procedure: domain.Ref(1, address(ids.ProcedureClassID, 9100020)), Events: stored.TriggerInsert, Timing: stored.TriggerAfter, Level: stored.TriggerRow, Arguments: []string{"owned"}, References: []ids.ObjectAddress{address(ids.ProcedureClassID, 9100020)}, Constraint: catalog.TriggerConstraint{OID: 9100011, Name: "c", Deferrable: true, InitiallyDeferred: true}}
	definition, err := stored.NewTrigger(p)
	if err != nil {
		t.Fatal(err)
	}
	owner.Triggers = stored.TriggerList{definition}
	kind, backing, constraint, err := table.RenameConstraintKind(owner, "c")
	if err != nil || kind != 't' || backing != (ids.ObjectAddress{}) || constraint != address(2606, p.Constraint.OID) {
		t.Fatal("constraint lookup used tgname or trigger OID", kind, backing, constraint, err)
	}
	if _, _, _, err := table.RenameConstraintKind(owner, "tr"); err == nil {
		t.Fatal("tgname incorrectly selects constraint")
	}
	next, err := table.RenameLocalConstraint(owner, "c", "renamed", 't')
	if err != nil {
		t.Fatal(err)
	}
	before, after := stored.TriggersOf(owner)[0].Parts(), stored.TriggersOf(next)[0].Parts()
	if before.Constraint.Name != "c" || after.Constraint.Name != "renamed" || after.Name != "tr" || after.OID != before.OID || after.Constraint.OID != before.Constraint.OID || next.Revision() != owner.Revision()+1 || &after.Arguments[0] != &before.Arguments[0] {
		t.Fatal("rename lost independent names or immutable backing", before, after)
	}
	if !catalog.RelationConstraintNameExists(next, "renamed") || catalog.RelationConstraintNameExists(next, "tr") {
		t.Fatal("shared constraint name inventory omitted contype t")
	}
	collision := *owner
	collision.Constraints.Check = []catalog.CheckConstraint{{OID: 9100030, Name: "occupied"}}
	if _, err := table.RenameLocalConstraint(&collision, "c", "occupied", 't'); err == nil {
		t.Fatal("renamed into table constraint")
	}
	for _, mode := range []parser.DropMode{parser.DropRestrict, parser.DropCascade} {
		_, err := table.NotNullCandidate(nil, catalog.Read{}, owner, parser.AlterTableAction{Action: parser.DropConstraint, Constraint: parser.Identifier{Text: "c"}, Mode: mode}, table.NotNullDirect)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" || diagnostic.Message != "cannot drop constraint c on table t because trigger tr on table t requires it" || diagnostic.Hint != "You can drop trigger tr on table t instead." {
			t.Fatal("lost INTERNAL ownership diagnostic", err)
		}
	}
	if stored.TriggersOf(owner)[0].Parts().Constraint.Name != "c" {
		t.Fatal("failed drop mutated definition")
	}
}
