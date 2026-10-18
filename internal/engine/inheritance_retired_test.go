package engine

import (
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// These are package fixtures: SQL constructs physical descriptors through the
// real providers; assertions exercise the issuer and catalog validator directly.
func retiredUnitTable(t *testing.T, e *Engine, name string) *relationDescriptor {
	t.Helper()
	for _, d := range e.CatalogRead().Descriptors() {
		if r, ok := d.(*relationDescriptor); ok && r.Name() == name {
			return r
		}
	}
	t.Fatal("missing physical relation", name)
	return nil
}

func retiredRuntimeFixture(t *testing.T) (*Engine, *Operation) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	for _, q := range []string{
		"CREATE TABLE p(a INT CONSTRAINT nn NOT NULL,b INT,CONSTRAINT ck CHECK(a>0))",
		"CREATE TABLE q(a INT CONSTRAINT nn NOT NULL,CONSTRAINT ck CHECK(a>0))",
		"CREATE TABLE c() INHERITS(p,q)",
		"CREATE TABLE leaf() INHERITS(c)",
	} {
		roleExecute(t, e, op, q)
	}
	return e, op
}

func TestInheritanceConstraintHistoricalOrigins(t *testing.T) {
	e, op := retiredRuntimeFixture(t)
	p := retiredUnitTable(t, e, "p")
	c := retiredUnitTable(t, e, "c")
	leaf := retiredUnitTable(t, e, "leaf")
	before := e.CatalogRead()
	nn := c.NotNullConstraints[0]
	ck := c.CheckConstraints[0]
	parentNN := p.NotNullConstraints[0].OID
	parentCK := p.CheckConstraints[0].OID
	roleExecute(t, e, op, "ALTER TABLE p DROP COLUMN a")
	current := retiredUnitTable(t, e, "c")
	parent := retiredUnitTable(t, e, "p")
	bottom := retiredUnitTable(t, e, "leaf")
	if !reflect.DeepEqual(current.NotNullConstraints, c.NotNullConstraints) || !reflect.DeepEqual(current.CheckConstraints, c.CheckConstraints) {
		t.Fatal("DROP changed retained physical OID, recipe, validity or NO INHERIT")
	}
	for _, pair := range []struct {
		p           inheritedConstraint
		own, source ids.OID
		check       bool
	}{
		{current.Inheritance.NotNull[nn.Attribute], nn.OID, parentNN, false},
		{current.Inheritance.Checks[ck.Name], ck.OID, parentCK, true},
	} {
		if pair.p.Local || pair.p.Count() != 2 || len(pair.p.Origins) != 1 || len(pair.p.Retired) != 1 {
			t.Fatal("DROP did not move exactly one live contribution to history", pair.p)
		}
		h := pair.p.Retired[0]
		if h.Parent.Address != p.Address() || h.Parent.Epoch != before.Epoch() || h.Archive != 0 || h.Replayed || h.Attribute != 1 || h.Column != "a" || h.Constraint != pair.source || h.Own != pair.own || h.Check != pair.check || h.Lifetime != uint64(before.Epoch()) {
			t.Fatal("wrong actual removed provider certificate", h)
		}
		if err := validateRetiredInheritance(e.CatalogRead(), current, pair.p, pair.own, pair.check); err != nil {
			t.Fatal(err)
		}
	}
	if parent.HasAttribute(1) || parent.Index["b"] != 0 || parent.Attributes[0] != 2 {
		t.Fatal("DROP lost actual source tombstone or surviving stable rank")
	}
	if err := validateInheritance(e.CatalogRead(), current); err != nil {
		t.Fatal(err)
	}
	for _, pair := range []struct {
		p      inheritedConstraint
		source ids.OID
	}{
		{bottom.Inheritance.NotNull[bottom.NotNullConstraints[0].Attribute], nn.OID},
		{bottom.Inheritance.Checks[ck.Name], ck.OID},
	} {
		if pair.p.Count() != 1 || len(pair.p.Retired) != 0 || len(pair.p.Origins) != 1 || pair.p.Origins[0].Parent != c.Address() || pair.p.Origins[0].Constraint != pair.source {
			t.Fatal("leaf copied ancestor historical multiplicity instead of actual live child fact", pair.p)
		}
	}
	if !reflect.DeepEqual(leaf.Inheritance, bottom.Inheritance) || len(c.Inheritance.NotNull[nn.Attribute].Retired) != 0 {
		t.Fatal("published capture mutated in place")
	}
	// A re-added same-name column has a new stable rank and new provider facts.
	// Its second removal contributes another lifetime, not another copy of the old.
	roleExecute(t, e, op, "ALTER TABLE p ADD COLUMN a INT,ADD CONSTRAINT nn NOT NULL a,ADD CONSTRAINT ck CHECK(a>0)")
	replacement := retiredUnitTable(t, e, "p")
	newNN := replacement.NotNullConstraints[0].OID
	newCK := replacement.CheckConstraints[0].OID
	if newNN == parentNN || newCK == parentCK || replacement.Attributes[replacement.Index["a"]] == 1 {
		t.Fatal("re-add reused retired source identity")
	}
	live := retiredUnitTable(t, e, "c")
	if live.Inheritance.NotNull[nn.Attribute].Count() != 3 || live.Inheritance.Checks[ck.Name].Count() != 3 {
		t.Fatal("re-add omitted live plus archived support")
	}
	roleExecute(t, e, op, "ALTER TABLE p DROP COLUMN a")
	final := retiredUnitTable(t, e, "c")
	for _, pair := range []struct {
		p      inheritedConstraint
		newOID ids.OID
	}{
		{final.Inheritance.NotNull[nn.Attribute], newNN}, {final.Inheritance.Checks[ck.Name], newCK},
	} {
		if pair.p.Count() != 3 || len(pair.p.Retired) != 2 || len(pair.p.Origins) != 1 || pair.p.Retired[0].Constraint == pair.p.Retired[1].Constraint || pair.p.Retired[1].Constraint != pair.newOID {
			t.Fatal("distinct DROP lifetimes collapsed or inflated", pair.p)
		}
	}
	if err := validateInheritance(e.CatalogRead(), final); err != nil {
		t.Fatal(err)
	}
}

func TestInheritanceConstraintHistoryIssuer(t *testing.T) {
	e, op := retiredRuntimeFixture(t)
	p := retiredUnitTable(t, e, "p")
	c := retiredUnitTable(t, e, "c")
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	state := &inheritanceAlterState{stage: stage, events: map[ids.ObjectAddress][]inheritanceAlterEvent{}}
	scope := &StatementScope{inheritanceAlter: state}
	next := *p
	next.Columns = slices.Clone(p.Columns[1:])
	next.Attributes = slices.Clone(p.Attributes[1:])
	next.NotNullConstraints = nil
	next.CheckConstraints = nil
	action := parser.AlterTableAction{Action: parser.DropColumn, Column: parser.Identifier{Text: "a"}}
	scope.recordInheritanceAlterAction(p, &next, action)
	event := state.events[p.Address()]
	if len(event) != 1 || len(event[0].retiredNotNull) != 1 || len(event[0].retiredChecks) != 1 {
		t.Fatal("actual DROP callback failed to capture removed facts", event)
	}
	graph := cloneInheritance(c.Inheritance)
	inheritRetiredColumnConstraints(state, c, graph, c.Attributes[c.Index["a"]])
	if len(graph.NotNull[1].Retired) != 1 || len(graph.Checks["ck"].Retired) != 1 {
		t.Fatal("recorded DROP failed to issue historical support")
	}
	// A caller flag, a DROP CONSTRAINT, or an unrelated source OID is not a
	// certificate of an actual removed contributing column provider.
	for _, bad := range []inheritanceAlterEvent{
		{action: parser.AlterTableAction{Action: parser.DropConstraint}, retiredNotNull: p.NotNullConstraints, retiredChecks: p.CheckConstraints},
		{action: action, attribute: 1, retiredNotNull: []notNullConstraint{{OID: p.NotNullConstraints[0].OID + 100000, Name: "nn", Attribute: 1}}},
	} {
		state.events[p.Address()] = []inheritanceAlterEvent{bad}
		g := cloneInheritance(c.Inheritance)
		inheritRetiredColumnConstraints(state, c, g, 1)
		if len(g.NotNull[1].Retired) != 0 || len(g.Checks["ck"].Retired) != 0 {
			t.Fatal("noncontributing event issued history", bad)
		}
	}
	roleExecute(t, e, op, "ALTER TABLE p DROP COLUMN a")
	current := retiredUnitTable(t, e, "c")
	view := e.CatalogRead()
	provenance := current.Inheritance.NotNull[1]
	for name, mutate := range map[string]func(*retiredInheritanceOrigin){
		"own": func(h *retiredInheritanceOrigin) { h.Own++ }, "kind": func(h *retiredInheritanceOrigin) { h.Check = true },
		"epoch": func(h *retiredInheritanceOrigin) { h.Parent.Epoch++ }, "foreign_owner": func(h *retiredInheritanceOrigin) {
			h.Parent = New().catalog.Domain.Ref(h.Parent.Epoch, h.Parent.Address)
		},
		"rank": func(h *retiredInheritanceOrigin) { h.Attribute = 2 }, "zero_lifetime": func(h *retiredInheritanceOrigin) { h.Lifetime = 0 },
		"namespace": func(h *retiredInheritanceOrigin) { h.Archive = 1 }, "column": func(h *retiredInheritanceOrigin) { h.Column = "" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := provenance
			bad.Retired = slices.Clone(provenance.Retired)
			mutate(&bad.Retired[0])
			if err := validateRetiredInheritance(view, current, bad, current.NotNullConstraints[0].OID, false); err == nil {
				t.Fatal("forged runtime lineage validated", name)
			}
		})
	}
	duplicate := provenance
	duplicate.Retired = append(slices.Clone(provenance.Retired), provenance.Retired[0])
	if err := validateRetiredInheritance(view, current, duplicate, current.NotNullConstraints[0].OID, false); err == nil {
		t.Fatal("duplicate source lifetime validated")
	}
	// Retirement must not relax exact current provider OID/key validation.
	forged := *current
	forged.Inheritance = cloneInheritance(current.Inheritance)
	live := forged.Inheritance.NotNull[1]
	live.Origins = slices.Clone(live.Origins)
	live.Origins[0].Constraint++
	forged.Inheritance.NotNull[1] = live
	if err := validateInheritance(view, &forged); err == nil {
		t.Fatal("historical support laundered a forged live provider")
	}
}

func TestInheritanceConstraintHistoryValidity(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		"CREATE TABLE p(a INT)",
		"CREATE TABLE q(a INT)",
		"CREATE TABLE c() INHERITS(p,q)",
		"CREATE TABLE leaf() INHERITS(c)",
		"ALTER TABLE p ADD CONSTRAINT nn NOT NULL a NOT VALID,ADD CONSTRAINT ck CHECK(a>0) NOT VALID",
	} {
		roleExecute(t, e, op, q)
	}
	old := retiredUnitTable(t, e, "c")
	if !old.NotNullConstraints[0].NotValid || !old.CheckConstraints[0].NotValid {
		t.Fatal("invalid fixture did not reach actual child fact")
	}
	roleExecute(t, e, op, "ALTER TABLE p DROP COLUMN a")
	current := retiredUnitTable(t, e, "c")
	if !reflect.DeepEqual(current.NotNullConstraints, old.NotNullConstraints) || !reflect.DeepEqual(current.CheckConstraints, old.CheckConstraints) {
		t.Fatal("retirement silently validated or rebuilt invalid enforcement")
	}
	for _, dependency := range current.CheckConstraints[0].Dependencies {
		if dependency.Ref.Address.ClassID == ids.RelationClassID && dependency.Ref.Address.ObjectID == retiredUnitTable(t, e, "p").Address().ObjectID && dependency.Ref.Address.SubID == 1 {
			t.Fatal("retained history pinned the removed live source column")
		}
	}
	if err := validateInheritance(e.CatalogRead(), current); err != nil {
		t.Fatal(err)
	}
}
