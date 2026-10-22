package table

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// fakePreparedDrop is a prepared drop with no definition effects.
type fakePreparedDrop struct{ next *catalog.Relation }

func (d *fakePreparedDrop) DefinitionDelta(ids.ObjectAddress) (catalog.Descriptor, bool, bool) {
	return nil, false, false
}
func (d *fakePreparedDrop) LayoutDelta(ids.ObjectAddress) (index.LayoutLineage, bool) {
	return index.LayoutLineage{}, false
}
func (d *fakePreparedDrop) Next() *catalog.Relation { return d.next }

// fakePrepareHost records each call and checks that the definition it is
// handed, and the operation's, read the relation being prepared.
type fakePrepareHost struct {
	t       *testing.T
	op      *session.Operation
	calls   []string
	drop    *fakePreparedDrop
	dropErr error
	added   *catalog.Relation
}

func (h *fakePrepareHost) record(call string, definition catalog.DefinitionReader, old *catalog.Relation) {
	h.calls = append(h.calls, call)
	if got, _ := definition.Descriptor(old.Address()); got != old {
		h.t.Errorf("%s: definition reads %v, want the prepared relation", call, got)
	}
	if _, ok := h.op.Definition.(InheritanceDefinitionReader); !ok {
		h.t.Errorf("%s: operation definition is %T", call, h.op.Definition)
	}
}

func (h *fakePrepareHost) PrepareUsingIndexMetadata(reader catalog.DefinitionReader, parent *catalog.Relation, captured index.UsingIndexAttachment) (*index.UsingIndexPreparation, error) {
	h.calls = append(h.calls, "using index")
	return nil, errors.New("unexpected USING INDEX")
}

func (h *fakePrepareHost) AddColumnCandidate(definition catalog.DefinitionReader, old *catalog.Relation, action parser.AlterTableAction) (*catalog.Relation, AddedColumn, error) {
	h.record("add "+action.Definition.Name, definition, old)
	return h.added, AddedColumn{Column: action.Definition}, nil
}

func (h *fakePrepareHost) PrepareColumnDropAfter(definition catalog.DefinitionReader, old *catalog.Relation, action parser.AlterTableAction, prior PreparedDrop) (PreparedDrop, error) {
	h.record("drop "+action.Column.Text, definition, old)
	if prior != nil {
		h.t.Errorf("first column drop got prior %v", prior)
	}
	h.op.Notices = append(h.op.Notices, sqlstate.Error{Message: "drop notice"})
	if h.dropErr != nil {
		return nil, h.dropErr
	}
	return h.drop, nil
}

func (h *fakePrepareHost) PrepareIdentityDropDefinition(definition catalog.DefinitionReader, old *catalog.Relation, action parser.AlterTableAction) (PreparedDrop, error) {
	h.record("drop identity", definition, old)
	return nil, errors.New("unexpected DROP IDENTITY")
}

func (h *fakePrepareHost) PrepareConstraintIndexDrop(definition catalog.DefinitionReader, old, next *catalog.Relation, actions []parser.AlterTableAction, prior PreparedDrop) (PreparedDrop, error) {
	h.record(fmt.Sprintf("constraint index drop after %v", prior == PreparedDrop(h.drop)), definition, next)
	return nil, nil
}

func (h *fakePrepareHost) PrepareAddedChecks(definition catalog.DefinitionReader, parent *catalog.Relation, raw []*parser.CheckConstraint) (PreparedAddedChecks, error) {
	h.record("checks", definition, parent)
	return PreparedAddedChecks{Old: parent, Next: parent}, nil
}

func prepareFixture(t *testing.T) (*fakePrepareHost, *InheritanceAlterPlan, *catalog.Relation) {
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 100}
	old := &catalog.Relation{ObjectAddress: address, RelationKind: catalog.TableRelation}
	state := catalog.NewState(1)
	state.SetDescriptor(address, old)
	dropped, added := *old, *old
	op := &session.Operation{Ctx: context.Background()}
	host := &fakePrepareHost{t: t, op: op, drop: &fakePreparedDrop{next: &dropped}, added: &added}
	plan := &InheritanceAlterPlan{Events: map[ids.ObjectAddress][]InheritanceAlterEvent{}, Definition: InheritanceDefinitionReader{Base: catalog.NewRead(nil, state)}}
	return host, plan, old
}

func TestPrepareInheritanceNodePhaseOrder(t *testing.T) {
	host, plan, old := prepareFixture(t)
	sentinel := InheritanceDefinitionReader{}
	host.op.Definition = &sentinel
	actions := []parser.AlterTableAction{
		{Action: parser.AddColumn, Definition: &parser.Column{Name: "x"}},
		{Action: parser.DropColumn, Column: parser.Identifier{Text: "y"}},
	}
	node, err := PrepareInheritanceNode(host, host.op, plan.Definition.Base, plan, old, actions)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"drop y", "constraint index drop after true", "add x", "checks"}; !slices.Equal(host.calls, want) {
		t.Fatalf("host calls = %q, want %q", host.calls, want)
	}
	drop, add := node.Steps[1], node.Steps[0]
	if drop.Old != old || drop.Next != host.drop.Next() || drop.Drop != PreparedDrop(host.drop) || len(drop.Notices) != 1 {
		t.Fatalf("drop step = %+v", drop)
	}
	if add.Old != host.drop.Next() || add.Next != host.added || add.Added.Column == nil || add.Added.Column.Name != "x" {
		t.Fatalf("add step = %+v", add)
	}
	if node.Checks.Old != host.added || node.AfterDropOld != host.drop.Next() || node.AfterDrop != nil {
		t.Fatalf("node phases = %+v", node)
	}
	if got, _ := plan.Definition.Descriptor(old.Address()); got != host.added {
		t.Fatalf("plan definition reads %v, want the last candidate", got)
	}
	if host.op.Definition != &sentinel || host.op.Notices != nil {
		t.Fatalf("operation definition or notices not restored")
	}
}

func TestPrepareInheritanceNodeStopsAtFailedStep(t *testing.T) {
	host, plan, old := prepareFixture(t)
	host.dropErr = errors.New("drop failed")
	host.op.Notices = []sqlstate.Error{{Message: "earlier"}}
	actions := []parser.AlterTableAction{
		{Action: parser.DropColumn, Column: parser.Identifier{Text: "y"}},
		{Action: parser.AddColumn, Definition: &parser.Column{Name: "x"}},
	}
	node, err := PrepareInheritanceNode(host, host.op, plan.Definition.Base, plan, old, actions)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"drop y"}; !slices.Equal(host.calls, want) {
		t.Fatalf("host calls = %q, want %q", host.calls, want)
	}
	step := node.Steps[0]
	if step.Err != host.dropErr || step.Old != old || step.Next != old || len(step.Notices) != 1 {
		t.Fatalf("failed step = %+v", step)
	}
	if _, exists := node.Steps[1]; exists {
		t.Fatal("a step after the failure was prepared")
	}
	if len(host.op.Notices) != 1 || host.op.Notices[0].Message != "earlier" {
		t.Fatalf("operation notices = %v", host.op.Notices)
	}
}
