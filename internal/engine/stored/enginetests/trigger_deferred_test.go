package enginetests_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// Construct real immutable catalog facts without issuing SQL. Public syntax and
// observable trigger behavior are covered by the external reference family.
func TestTriggerDeferredDefinition(t *testing.T) {
	e := engine.New()
	seed := e.CatalogRead()
	var function *catalog.Procedure
	for _, d := range seed.Descriptors() {
		if p, ok := d.(*catalog.Procedure); ok && p.Name() == "suppress_redundant_updates_trigger" {
			function = p
			break
		}
	}
	if function == nil {
		t.Fatal("missing genuine builtin trigger capability")
	}
	address := func(class, oid ids.OID) ids.ObjectAddress { return ids.ObjectAddress{ClassID: class, ObjectID: oid} }
	owner := &catalog.Relation{ObjectAddress: address(ids.RelationClassID, 9100001), ObjectNamespace: function.Namespace(), ObjectName: "deferred_owner", ObjectRevision: 1, RelationKind: catalog.PartitionedRelation, Partition: &catalog.PartitionDefinition{}}
	owner.Owner = e.RoleStore.Domain.Principal(authorization.BootstrapPrincipalID)
	from := &catalog.Relation{ObjectAddress: address(ids.RelationClassID, 9100002), ObjectNamespace: function.Namespace(), ObjectName: "deferred_from", ObjectRevision: 1, RelationKind: catalog.TableRelation}
	state := catalog.CloneState(seed.State())
	state.SetDescriptor(owner.Address(), owner)
	state.SetDescriptor(from.Address(), from)
	read := seed.WithState(state)
	ref := func(a ids.ObjectAddress) ids.ObjectRef { return read.Owner().Domain.Ref(read.Epoch(), a) }
	p := stored.TriggerParts{OID: 9100010, Name: "tgname", Relation: ref(owner.Address()), Procedure: ref(function.Address()), Timing: stored.TriggerAfter, Level: stored.TriggerRow, Events: stored.TriggerInsert | stored.TriggerUpdate | stored.TriggerDelete, Arguments: []string{"owned"}, References: []ids.ObjectAddress{function.Address()}, Constraint: catalog.TriggerConstraint{OID: 9100011, Name: "conname", Deferrable: true, InitiallyDeferred: true, From: ref(from.Address())}}
	definition, err := stored.NewTrigger(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Arguments[0] = "borrowed mutation"
	p.References[0] = owner.Address()
	if definition.Parts().Arguments[0] != "owned" || definition.Parts().References[0] != function.Address() {
		t.Fatal("constructor retained mutable input backing")
	}
	p = definition.Parts()
	list := stored.TriggerList{definition}
	owner.Triggers = list
	if err := list.Validate(read, owner); err != nil {
		t.Fatal(err)
	}
	if list.OID(0) != p.OID || list.ConstraintOID(0) != p.Constraint.OID {
		t.Fatal("identity inventory omitted constraint")
	}
	var fact catalog.TriggerFact
	if err := list.Visit(func(f catalog.TriggerFact) error { fact = f; return nil }); err != nil || fact.Constraint != p.Constraint || fact.Name != "tgname" {
		t.Fatal("fact projection lost separate constraint metadata", fact, err)
	}
	var identities []ids.OID
	if err := catalog.VisitDefinitionOIDs(owner, func(id ids.OID) error { identities = append(identities, id); return nil }); err != nil || !slices.Contains(identities, p.OID) || !slices.Contains(identities, p.Constraint.OID) {
		t.Fatal("definition reservation lost identity", identities, err)
	}
	if !slices.Contains(catalog.RelationPayloadAddresses(owner), address(2606, p.Constraint.OID)) {
		t.Fatal("drop inventory lost owned constraint")
	}
	typ, ok := read.LookupType(sqltype.PgNodeTree)
	if !ok {
		t.Fatal("missing source-owned tree type")
	}
	var projected stored.TriggerCatalogDefinition
	if err := stored.VisitTriggerCatalog(t.Context(), read, owner, typ, func(f stored.TriggerCatalogDefinition) error { projected = f; return nil }); err != nil || projected.Constraint != p.Constraint || projected.OID != p.OID || projected.Type != 29 || !projected.Qual.IsNull() || projected.Qual.Type() != typ {
		t.Fatal("checked trigger catalog projection", projected, err)
	}
	for name, change := range map[string]func(*stored.TriggerParts){
		"partial name":           func(p *stored.TriggerParts) { p.Constraint.OID = 0 },
		"missing name":           func(p *stored.TriggerParts) { p.Constraint.Name = "" },
		"same identity":          func(p *stored.TriggerParts) { p.Constraint.OID = p.OID },
		"before":                 func(p *stored.TriggerParts) { p.Timing = stored.TriggerBefore },
		"statement":              func(p *stored.TriggerParts) { p.Level = stored.TriggerStatement },
		"truncate":               func(p *stored.TriggerParts) { p.Events = stored.TriggerTruncate },
		"nondeferrable deferred": func(p *stored.TriggerParts) { p.Constraint.Deferrable = false },
		"malformed FROM":         func(p *stored.TriggerParts) { p.Constraint.From.Address.ClassID = ids.ProcedureClassID },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := p
			change(&invalid)
			if _, err := stored.NewTrigger(invalid); err == nil {
				t.Fatal("accepted malformed metadata")
			}
		})
	}
	for _, change := range []func(*stored.TriggerParts){
		func(p *stored.TriggerParts) { p.Constraint.Name = "renamed" },
		func(p *stored.TriggerParts) { p.Constraint.InitiallyDeferred = false },
		func(p *stored.TriggerParts) { p.Constraint.From = ids.ObjectRef{} },
	} {
		changed := p
		change(&changed)
		next, err := stored.NewTrigger(changed)
		if err != nil || definition.SameExecutable(next) {
			t.Fatal("definition equality ignored metadata", err)
		}
	}
	for _, kind := range []catalog.RelationKind{catalog.ViewRelation, catalog.ForeignRelation} {
		wrong := *owner
		wrong.RelationKind = kind
		if err := list.Validate(read, &wrong); err == nil {
			t.Fatal("constraint trigger admitted on wrong relation kind", kind)
		}
	}
	foreign := engine.New().CatalogRead()
	if err := list.Validate(foreign, owner); err == nil {
		t.Fatal("foreign generation accepted")
	}
	invalid := p
	invalid.Constraint.From = foreign.Owner().Domain.Ref(foreign.Epoch(), from.Address())
	invalidDefinition, err := stored.NewTrigger(invalid)
	if err != nil {
		t.Fatal(err)
	}
	if err := (stored.TriggerList{invalidDefinition}).Validate(read, owner); err == nil {
		t.Fatal("foreign FROM authority accepted")
	}
	ordinary := p
	ordinary.Constraint = catalog.TriggerConstraint{}
	ordinaryDefinition, err := stored.NewTrigger(ordinary)
	if err != nil || (stored.TriggerList{ordinaryDefinition}).ConstraintOID(0) != 0 {
		t.Fatal("ordinary constraint fields changed", err)
	}

	child := &catalog.Relation{ObjectAddress: address(ids.RelationClassID, 9100003), ObjectNamespace: owner.Namespace(), ObjectName: "deferred_child", ObjectRevision: 1, RelationKind: catalog.TableRelation, Membership: &catalog.PartitionMembership{Parent: owner.Address()}}
	state.SetDescriptor(child.Address(), child)
	clone, err := stored.CloneTrigger(read, definition, owner, child, 9100020, 9100021, nil)
	if err != nil {
		t.Fatal(err)
	}
	cp := clone.Parts()
	if cp.Constraint.OID == p.Constraint.OID || cp.OID == p.OID || cp.Constraint.Name != p.Name || cp.Constraint.From != p.Constraint.From || cp.Parent != address(2620, p.OID) || !cp.Constraint.Deferrable || !cp.Constraint.InitiallyDeferred {
		t.Fatal("invalid clone identities/declarations", cp)
	}
	child.Triggers = stored.TriggerList{clone}
	if err := child.Triggers.Validate(read, child); err != nil {
		t.Fatal(err)
	}
	if _, err := stored.CloneTrigger(read, definition, owner, child, 9100030, 0, nil); err == nil {
		t.Fatal("constraint clone accepted missing identity")
	}
	if _, err := stored.CloneTrigger(read, definition, owner, child, 9100030, p.Constraint.OID, nil); err == nil {
		t.Fatal("constraint clone reused parent constraint identity")
	}
	if _, err := stored.CloneTrigger(read, definition, owner, child, p.OID, 9100031, nil); err == nil {
		t.Fatal("constraint clone reused parent trigger identity")
	}
	cp.Constraint.Name = "child_constraint"
	clone, err = stored.NewTrigger(cp)
	if err != nil {
		t.Fatal(err)
	}
	child.Owner, child.Triggers = owner.Owner, stored.TriggerList{clone}
	owner.Constraints.Check = []catalog.CheckConstraint{{OID: 9100040, Name: p.Name}}
	secondParts := p
	secondParts.OID, secondParts.Name = 9100050, "z_second"
	secondParts.Constraint.OID, secondParts.Constraint.Name = 9100051, "second_constraint"
	second, err := stored.NewTrigger(secondParts)
	if err != nil {
		t.Fatal(err)
	}
	owner.Triggers = stored.TriggerList{definition, second}
	secondClone, err := stored.CloneTrigger(read, second, owner, child, 9100052, 9100053, nil)
	if err != nil {
		t.Fatal(err)
	}
	secondClone, err = secondClone.WithConstraintName("child_second")
	if err != nil {
		t.Fatal(err)
	}
	child.Triggers = stored.TriggerList{clone, secondClone}
	grandchild := &catalog.Relation{ObjectAddress: address(ids.RelationClassID, 9100060), ObjectNamespace: owner.Namespace(), ObjectName: "deferred_grandchild", ObjectRevision: 1, RelationKind: catalog.TableRelation, Owner: owner.Owner, Membership: &catalog.PartitionMembership{Parent: child.Address()}}
	owner.Partition.Children = []ids.ObjectAddress{child.Address()}
	child.RelationKind, child.Partition = catalog.PartitionedRelation, &catalog.PartitionDefinition{Children: []ids.ObjectAddress{grandchild.Address()}}
	state.SetDescriptor(grandchild.Address(), grandchild)
	grandFirst, err := stored.CloneTrigger(read, clone, child, grandchild, 9100061, 9100062, nil)
	if err != nil {
		t.Fatal(err)
	}
	grandFirst, err = grandFirst.WithConstraintName("grand_first")
	if err != nil {
		t.Fatal(err)
	}
	grandSecond, err := stored.CloneTrigger(read, secondClone, child, grandchild, 9100063, 9100064, nil)
	if err != nil {
		t.Fatal(err)
	}
	grandSecond, err = grandSecond.WithConstraintName("grand_second")
	if err != nil {
		t.Fatal(err)
	}
	grandchild.Triggers = stored.TriggerList{grandFirst, grandSecond}
	groups, err := stored.TriggerDumpGroups(t.Context(), read, e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}))
	if err != nil {
		t.Fatal(err)
	}
	creates, constraintRenames, triggerRenames := 0, 0, 0
	phase := 0
	families := make(map[string]string)
	for _, group := range groups {
		switch {
		case strings.HasPrefix(group.SQL, "CREATE CONSTRAINT TRIGGER "):
			if phase != 0 || strings.Contains(group.SQL, "TRIGGER "+catalog.QuoteIdentifier(p.Name)+" ") {
				t.Fatal("replay ignored temporary construction identity", group.SQL)
			}
			creates++
		case strings.Contains(group.SQL, " RENAME CONSTRAINT "):
			if phase > 1 || !strings.Contains(group.SQL, "stuntgres_trigger_restore") {
				t.Fatal("replay lost independent constraint phase", group.SQL)
			}
			rename := strings.SplitN(strings.SplitN(group.SQL, " RENAME CONSTRAINT ", 2)[1], " TO ", 2)
			families[rename[1]] = rename[0]
			phase = 1
			constraintRenames++
		case strings.HasPrefix(group.SQL, "ALTER TRIGGER "):
			if phase > 2 || !strings.Contains(group.SQL, " RENAME TO "+catalog.QuoteIdentifier(p.Name)+";") && !strings.Contains(group.SQL, " RENAME TO "+catalog.QuoteIdentifier(second.Name())+";") {
				t.Fatal("replay lost recursive trigger rename phase", group.SQL)
			}
			phase = 2
			triggerRenames++
		}
	}
	if creates != 2 || constraintRenames != 6 || triggerRenames != 2 {
		t.Fatal("replay duplicated clone CREATE or lost independent names", creates, constraintRenames, triggerRenames)
	}
	name := func(s string) string { return families[catalog.QuoteIdentifier(s)+";\n"] }
	if name("conname") == "" || name("second_constraint") == "" || name("conname") == name("second_constraint") || name("conname") != name("child_constraint") || name("conname") != name("grand_first") || name("second_constraint") != name("child_second") || name("second_constraint") != name("grand_second") {
		t.Fatal("replay confused multilevel source families", families)
	}

	destination := engine.New().CatalogRead()
	destState := catalog.CloneState(destination.State())
	destOwner, destFrom := *owner, *from
	destOwner.ObjectAddress = address(ids.RelationClassID, 9200001)
	destFrom.ObjectAddress = address(ids.RelationClassID, 9200002)
	destState.SetDescriptor(destOwner.Address(), &destOwner)
	destState.SetDescriptor(destFrom.Address(), &destFrom)
	destination = destination.WithState(destState)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{owner.Address(): destOwner.Address(), from.Address(): destFrom.Address(), address(2606, p.Constraint.OID): address(2606, 9200011)}
	remapRef := func(old ids.ObjectRef) ids.ObjectRef {
		a := old.Address
		if next, ok := mapping[a]; ok {
			a = next
		}
		return destination.Owner().Domain.Ref(destination.Epoch(), a)
	}
	mapped, err := stored.RemapTriggers(destination, &destOwner, mapping, remapRef, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	mp := stored.TriggersOf(mapped)[0].Parts()
	if mp.Constraint.OID != 9200011 || mp.Constraint.Name != p.Constraint.Name || mp.Constraint.From.Address != destFrom.Address() || mp.Constraint.From.Domain() != destination.Owner().Domain || mp.Relation.Address != destOwner.Address() || mp.Relation.Domain() != destination.Owner().Domain {
		t.Fatal("remap lost destination identity or provenance", mp)
	}
	if !reflect.DeepEqual(definition.Parts(), p) {
		t.Fatal("clone/remap mutated source")
	}
	if err := mapped.Triggers.Validate(destination, mapped); err != nil {
		t.Fatal(err)
	}
}
