package engine

import (
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/parser"
)

// These are package fixtures: SQL constructs physical descriptors through the
// real providers; assertions exercise the issuer and catalog validator directly.
func retiredUnitTable(t *testing.T, e *Engine, name string) *relationDescriptor {
	t.Helper()
	for _, d := range e.CatalogRead().state.descriptors {
		if r, ok := d.(*relationDescriptor); ok && r.name == name {
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
	nn := c.notNullConstraints[0]
	ck := c.checkConstraints[0]
	parentNN := p.notNullConstraints[0].OID
	parentCK := p.checkConstraints[0].OID
	roleExecute(t, e, op, "ALTER TABLE p DROP COLUMN a")
	current := retiredUnitTable(t, e, "c")
	parent := retiredUnitTable(t, e, "p")
	bottom := retiredUnitTable(t, e, "leaf")
	if !reflect.DeepEqual(current.notNullConstraints, c.notNullConstraints) || !reflect.DeepEqual(current.checkConstraints, c.checkConstraints) {
		t.Fatal("DROP changed retained physical OID, recipe, validity or NO INHERIT")
	}
	for _, pair := range []struct {
		p           inheritedConstraint
		own, source OID
		check       bool
	}{
		{current.inheritance.notNull[nn.Attribute], nn.OID, parentNN, false},
		{current.inheritance.checks[ck.Name], ck.OID, parentCK, true},
	} {
		if pair.p.local || pair.p.count() != 2 || len(pair.p.origins) != 1 || len(pair.p.retired) != 1 {
			t.Fatal("DROP did not move exactly one live contribution to history", pair.p)
		}
		h := pair.p.retired[0]
		if h.parent.Address != p.address || h.parent.Epoch != before.Epoch() || h.archive != 0 || h.replayed || h.attribute != 1 || h.column != "a" || h.constraint != pair.source || h.own != pair.own || h.check != pair.check || h.lifetime != uint64(before.Epoch()) {
			t.Fatal("wrong actual removed provider certificate", h)
		}
		if err := validateRetiredInheritance(e.CatalogRead(), current, pair.p, pair.own, pair.check); err != nil {
			t.Fatal(err)
		}
	}
	if parent.hasAttribute(1) || parent.index["b"] != 0 || parent.attributes[0] != 2 {
		t.Fatal("DROP lost actual source tombstone or surviving stable rank")
	}
	if err := validateInheritance(e.CatalogRead(), current); err != nil {
		t.Fatal(err)
	}
	for _, pair := range []struct {
		p      inheritedConstraint
		source OID
	}{
		{bottom.inheritance.notNull[bottom.notNullConstraints[0].Attribute], nn.OID},
		{bottom.inheritance.checks[ck.Name], ck.OID},
	} {
		if pair.p.count() != 1 || len(pair.p.retired) != 0 || len(pair.p.origins) != 1 || pair.p.origins[0].parent != c.address || pair.p.origins[0].constraint != pair.source {
			t.Fatal("leaf copied ancestor historical multiplicity instead of actual live child fact", pair.p)
		}
	}
	if !reflect.DeepEqual(leaf.inheritance, bottom.inheritance) || len(c.inheritance.notNull[nn.Attribute].retired) != 0 {
		t.Fatal("published capture mutated in place")
	}
	// A re-added same-name column has a new stable rank and new provider facts.
	// Its second removal contributes another lifetime, not another copy of the old.
	roleExecute(t, e, op, "ALTER TABLE p ADD COLUMN a INT,ADD CONSTRAINT nn NOT NULL a,ADD CONSTRAINT ck CHECK(a>0)")
	replacement := retiredUnitTable(t, e, "p")
	newNN := replacement.notNullConstraints[0].OID
	newCK := replacement.checkConstraints[0].OID
	if newNN == parentNN || newCK == parentCK || replacement.attributes[replacement.index["a"]] == 1 {
		t.Fatal("re-add reused retired source identity")
	}
	live := retiredUnitTable(t, e, "c")
	if live.inheritance.notNull[nn.Attribute].count() != 3 || live.inheritance.checks[ck.Name].count() != 3 {
		t.Fatal("re-add omitted live plus archived support")
	}
	roleExecute(t, e, op, "ALTER TABLE p DROP COLUMN a")
	final := retiredUnitTable(t, e, "c")
	for _, pair := range []struct {
		p      inheritedConstraint
		newOID OID
	}{
		{final.inheritance.notNull[nn.Attribute], newNN}, {final.inheritance.checks[ck.Name], newCK},
	} {
		if pair.p.count() != 3 || len(pair.p.retired) != 2 || len(pair.p.origins) != 1 || pair.p.retired[0].constraint == pair.p.retired[1].constraint || pair.p.retired[1].constraint != pair.newOID {
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
	state := &inheritanceAlterState{stage: stage, events: map[ObjectAddress][]inheritanceAlterEvent{}}
	scope := &StatementScope{inheritanceAlter: state}
	next := *p
	next.columns = slices.Clone(p.columns[1:])
	next.attributes = slices.Clone(p.attributes[1:])
	next.notNullConstraints = nil
	next.checkConstraints = nil
	action := parser.AlterTableAction{Action: parser.DropColumn, Column: parser.Identifier{Text: "a"}}
	scope.recordInheritanceAlterAction(p, &next, action)
	event := state.events[p.address]
	if len(event) != 1 || len(event[0].retiredNotNull) != 1 || len(event[0].retiredChecks) != 1 {
		t.Fatal("actual DROP callback failed to capture removed facts", event)
	}
	graph := cloneInheritance(c.inheritance)
	inheritRetiredColumnConstraints(state, c, graph, c.attributes[c.index["a"]])
	if len(graph.notNull[1].retired) != 1 || len(graph.checks["ck"].retired) != 1 {
		t.Fatal("recorded DROP failed to issue historical support")
	}
	// A caller flag, a DROP CONSTRAINT, or an unrelated source OID is not a
	// certificate of an actual removed contributing column provider.
	for _, bad := range []inheritanceAlterEvent{
		{action: parser.AlterTableAction{Action: parser.DropConstraint}, retiredNotNull: p.notNullConstraints, retiredChecks: p.checkConstraints},
		{action: action, attribute: 1, retiredNotNull: []notNullConstraint{{OID: p.notNullConstraints[0].OID + 100000, Name: "nn", Attribute: 1}}},
	} {
		state.events[p.address] = []inheritanceAlterEvent{bad}
		g := cloneInheritance(c.inheritance)
		inheritRetiredColumnConstraints(state, c, g, 1)
		if len(g.notNull[1].retired) != 0 || len(g.checks["ck"].retired) != 0 {
			t.Fatal("noncontributing event issued history", bad)
		}
	}
	roleExecute(t, e, op, "ALTER TABLE p DROP COLUMN a")
	current := retiredUnitTable(t, e, "c")
	view := e.CatalogRead()
	provenance := current.inheritance.notNull[1]
	for name, mutate := range map[string]func(*retiredInheritanceOrigin){
		"own": func(h *retiredInheritanceOrigin) { h.own++ }, "kind": func(h *retiredInheritanceOrigin) { h.check = true },
		"epoch": func(h *retiredInheritanceOrigin) { h.parent.Epoch++ }, "foreign_owner": func(h *retiredInheritanceOrigin) { h.parent.owner = New().owner },
		"rank": func(h *retiredInheritanceOrigin) { h.attribute = 2 }, "zero_lifetime": func(h *retiredInheritanceOrigin) { h.lifetime = 0 },
		"namespace": func(h *retiredInheritanceOrigin) { h.archive = 1 }, "column": func(h *retiredInheritanceOrigin) { h.column = "" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := provenance
			bad.retired = slices.Clone(provenance.retired)
			mutate(&bad.retired[0])
			if err := validateRetiredInheritance(view, current, bad, current.notNullConstraints[0].OID, false); err == nil {
				t.Fatal("forged runtime lineage validated", name)
			}
		})
	}
	duplicate := provenance
	duplicate.retired = append(slices.Clone(provenance.retired), provenance.retired[0])
	if err := validateRetiredInheritance(view, current, duplicate, current.notNullConstraints[0].OID, false); err == nil {
		t.Fatal("duplicate source lifetime validated")
	}
	// Retirement must not relax exact current provider OID/key validation.
	forged := *current
	forged.inheritance = cloneInheritance(current.inheritance)
	live := forged.inheritance.notNull[1]
	live.origins = slices.Clone(live.origins)
	live.origins[0].constraint++
	forged.inheritance.notNull[1] = live
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
	if !old.notNullConstraints[0].NotValid || !old.checkConstraints[0].NotValid {
		t.Fatal("invalid fixture did not reach actual child fact")
	}
	roleExecute(t, e, op, "ALTER TABLE p DROP COLUMN a")
	current := retiredUnitTable(t, e, "c")
	if !reflect.DeepEqual(current.notNullConstraints, old.notNullConstraints) || !reflect.DeepEqual(current.checkConstraints, old.checkConstraints) {
		t.Fatal("retirement silently validated or rebuilt invalid enforcement")
	}
	for _, dependency := range current.checkConstraints[0].dependencies {
		if dependency.ref.Address.ClassID == RelationClassID && dependency.ref.Address.ObjectID == retiredUnitTable(t, e, "p").address.ObjectID && dependency.ref.Address.SubID == 1 {
			t.Fatal("retained history pinned the removed live source column")
		}
	}
	if err := validateInheritance(e.CatalogRead(), current); err != nil {
		t.Fatal(err)
	}
}
