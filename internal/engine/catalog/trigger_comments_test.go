package catalog

import (
	"testing"

	"stuntgres/internal/engine/ids"
)

// triggerFacts is a minimal TriggersPayload: the catalog layer only needs the
// ordered OID/name facts to decide liveness.
type triggerFacts struct {
	oids  []ids.OID
	names []string
}

func (p triggerFacts) Validate(Read, *Relation) error { return nil }
func (p triggerFacts) Len() int                       { return len(p.oids) }
func (p triggerFacts) OID(i int) ids.OID              { return p.oids[i] }
func (triggerFacts) ConstraintOID(int) ids.OID        { return 0 }
func (triggerFacts) ConstraintName(int) string        { return "" }
func (p triggerFacts) Visit(visit func(TriggerFact) error) error {
	for i := range p.oids {
		if err := visit(TriggerFact{OID: p.oids[i], Name: p.names[i]}); err != nil {
			return err
		}
	}
	return nil
}

func TestTriggerComments(t *testing.T) {
	owner := &Relation{
		ObjectAddress:   ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 500},
		ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200},
		ObjectName:      "child",
		RelationKind:    TableRelation,
		Triggers:        triggerFacts{oids: []ids.OID{700, 701}, names: []string{"trg", "other"}},
		TriggerComments: map[ids.OID]string{
			700: "payload live",
			801: "fk owner",
			999: "stale",
			702: "",
		},
	}
	parent := &Relation{
		ObjectAddress:   ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 600},
		ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200},
		ObjectName:      "parent",
		RelationKind:    TableRelation,
		TriggerComments: map[ids.OID]string{
			800: "fk target",
			801: "misplaced",
		},
	}
	// Slots 0/1 are the DELETE/UPDATE action triggers on the referenced
	// relation; slots 2/3 are the check triggers on the owning relation.
	owner.Constraints.ForeignKeys = []ForeignKeyConstraint{{
		OID:         900,
		Target:      parent.ObjectAddress,
		TriggerOIDs: [4]ids.OID{800, 0, 801, 0},
	}}
	state := NewState(1)
	state.SetDescriptor(owner.Address(), owner)
	state.SetDescriptor(parent.Address(), parent)
	view := NewRead(nil, state)

	for _, tc := range []struct {
		name     string
		relation *Relation
		trigger  string
		want     ids.OID
		ok       bool
	}{
		{"payload trigger", owner, "trg", 700, true},
		{"second payload trigger", owner, "other", 701, true},
		{"unknown payload name", owner, "missing", 0, false},
		{"fk action trigger on target", parent, "RI_ConstraintTrigger_a_800", 800, true},
		{"fk check trigger on owner", owner, "RI_ConstraintTrigger_c_801", 801, true},
		{"action trigger is not on owner", owner, "RI_ConstraintTrigger_a_800", 0, false},
		{"check trigger is not on target", parent, "RI_ConstraintTrigger_c_801", 0, false},
	} {
		oid, ok := TriggerOIDByName(view, tc.relation, tc.trigger)
		if oid != tc.want || ok != tc.ok {
			t.Fatalf("%s: TriggerOIDByName(%q) = %d, %v; want %d, %v", tc.name, tc.trigger, oid, ok, tc.want, tc.ok)
		}
	}
	for _, tc := range []struct {
		name string
		oid  ids.OID
		want string
		ok   bool
	}{
		{"payload live", 700, "payload live", true},
		{"fk target live", 800, "fk target", true},
		{"fk owner live", 801, "fk owner", true},
		{"stale payload oid", 999, "", false},
		{"empty text", 702, "", false},
	} {
		text, ok := TriggerCommentText(view, tc.oid)
		if text != tc.want || ok != tc.ok {
			t.Fatalf("%s: TriggerCommentText(%d) = %q, %v; want %q, %v", tc.name, tc.oid, text, ok, tc.want, tc.ok)
		}
	}

	visited := make(map[ids.OID]string)
	if err := VisitTriggerComments(view, func(oid ids.OID, text string) error {
		visited[oid] = text
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := map[ids.OID]string{700: "payload live", 800: "fk target", 801: "fk owner"}
	if len(visited) != len(want) {
		t.Fatalf("visited %v; want %v", visited, want)
	}
	for oid, text := range want {
		if visited[oid] != text {
			t.Fatalf("visited %v; want %v", visited, want)
		}
	}

	uncommented := NewState(1)
	uncommented.SetDescriptor(owner.Address(), &Relation{
		ObjectAddress: owner.ObjectAddress,
		RelationKind:  TableRelation,
		Triggers:      owner.Triggers,
	})
	called := false
	if err := VisitTriggerComments(NewRead(nil, uncommented), func(ids.OID, string) error {
		called = true
		return nil
	}); err != nil || called {
		t.Fatalf("uncommented catalog visited a comment: %v %v", called, err)
	}
}