package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func triggerDDLExtensionUnit(t *testing.T) (*Engine, *StatementScope, *catalog.Relation, *catalog.Procedure, *catalog.Extension) {
	t.Helper()
	e, s, _, relation, function := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	stage := extensionUnitStage(t, e, s.op)
	extensionUnitPublish(t, e, s.op, stage)
	address := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"})
	return e, s, relation, function, e.CatalogRead().MustExtension(address)
}

func triggerDDLPartsUnit(t *testing.T, p stored.TriggerParts) stored.Trigger {
	t.Helper()
	definition, err := stored.NewTrigger(p)
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func TestTriggerDDLRenameIdentity(t *testing.T) {
	e, s, relation, function, ext := triggerDDLExtensionUnit(t)
	definition := triggerUnitDefinition(t, e.CatalogRead(), relation, function, "z", stored.TriggerUpdate)
	p := definition.Parts()
	p.UpdateColumns = []int32{2}
	p.Arguments = []string{"7", "kept"}
	p.AutoExtensions = []ids.ObjectAddress{ext.Address()}
	column := relation.Address()
	column.SubID = 2
	p.References = append(p.References, column)
	definition = triggerDDLPartsUnit(t, p)
	other := triggerUnitDefinition(t, e.CatalogRead(), relation, function, "a", stored.TriggerInsert)
	q := other.Parts()
	q.OID++
	other = triggerDDLPartsUnit(t, q)
	relation = triggerUnitInstall(t, e, relation, other, definition)
	store := e.storage.Relations[relation.Address()]
	if err := s.Finish(nil); err != nil {
		t.Fatal(err)
	}
	stmt := &parser.AlterTrigger{Name: parser.Identifier{Text: "z"}, Table: parser.ObjectName{Parts: []parser.Identifier{{Text: relation.Name()}}}, Action: "rename", Target: parser.Identifier{Text: "0new"}}
	if err := extensionUnitExecute(t, e, s.op, stmt); err != nil {
		t.Fatal(err)
	}
	next := e.CatalogRead().MustRelation(relation.Address())
	got := stored.TriggersOf(next)
	if len(got) != 2 || got[0].Name() != "0new" || got[1].Name() != "a" {
		t.Fatal("unsorted renamed payload", got)
	}
	parts := got[0].Parts()
	parts.Name = p.Name
	if !reflect.DeepEqual(parts, p) || stored.TriggersOf(relation)[1].Name() != "z" || next.Revision() == relation.Revision() || e.storage.Relations[relation.Address()] != store {
		t.Fatal("rename changed definition, source or storage identity")
	}
	stmt.Target.Text = "a"
	stmt.Name.Text = "0new"
	before := e.catalog.state
	if err := extensionUnitExecute(t, e, s.op, stmt); err == nil {
		t.Fatal("collision accepted")
	}
	if e.catalog.state != before {
		t.Fatal("collision published")
	}
}

func triggerDDLTreeUnit(t *testing.T) (*Engine, *StatementScope, *catalog.Relation, *catalog.Relation, *catalog.Relation, *catalog.Extension) {
	t.Helper()
	e, s, model, function, ext := triggerDDLExtensionUnit(t)
	root, low, high := triggerPartitionUnitTree(t, e, model)
	definition := triggerUnitDefinition(t, e.CatalogRead(), root, function, "old", stored.TriggerInsert)
	root = triggerUnitInstall(t, e, root, definition)
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	copy := *root
	copy.ObjectRevision++
	if err := stage.Replace(&copy); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, stage)
	return e, s, e.CatalogRead().MustRelation(root.Address()), e.CatalogRead().MustRelation(low.Address()), e.CatalogRead().MustRelation(high.Address()), ext
}

func TestTriggerDDLPartitionIdentity(t *testing.T) {
	e, _, root, low, high, ext := triggerDDLTreeUnit(t)
	local := stored.TriggersOf(low)[0].Parts()
	local.AutoExtensions = []ids.ObjectAddress{ext.Address()}
	low = triggerUnitInstall(t, e, low, triggerDDLPartsUnit(t, local))
	oldRoot, oldLow, oldHigh := root, low, high
	p := stored.TriggersOf(root)[0].Parts()
	p.Name = "renamed"
	p.AutoExtensions = []ids.ObjectAddress{ext.Address()}
	copy := *root
	copy.ObjectRevision++
	copy.Triggers = stored.TriggersPayload([]stored.Trigger{triggerDDLPartsUnit(t, p)})
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&copy); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, stage)
	for _, old := range []*catalog.Relation{oldRoot, oldLow, oldHigh} {
		next := e.CatalogRead().MustRelation(old.Address())
		before, after := stored.TriggersOf(old)[0].Parts(), stored.TriggersOf(next)[0].Parts()
		if before.OID != after.OID || before.Parent != after.Parent || after.Name != "renamed" || before.Name != "old" {
			t.Fatal("clone identity or source changed", before, after)
		}
		want := 0
		if old == oldRoot || old == oldLow {
			want = 1
		}
		if len(after.AutoExtensions) != want {
			t.Fatal("parent edges copied or local edges lost", after.AutoExtensions)
		}
	}
	// A local occupant blocks a second parent rename without publishing any sibling.
	root = e.CatalogRead().MustRelation(root.Address())
	high = e.CatalogRead().MustRelation(high.Address())
	occupant := stored.TriggersOf(high)[0].Parts()
	occupant.OID += 100
	occupant.Name = "taken"
	occupant.Parent = ids.ObjectAddress{}
	occupant.ParentRevision = 0
	triggerUnitInstall(t, e, high, stored.TriggersOf(high)[0], triggerDDLPartsUnit(t, occupant))
	before := e.catalog.state
	p = stored.TriggersOf(root)[0].Parts()
	p.Name = "taken"
	copy = *root
	copy.ObjectRevision++
	copy.Triggers = stored.TriggersPayload([]stored.Trigger{triggerDDLPartsUnit(t, p)})
	failed := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(failed)
	if err := failed.Replace(&copy); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ValidateCatalogStage(context.Background(), failed); err == nil {
		t.Fatal("child collision accepted")
	}
	if e.catalog.state != before {
		t.Fatal("collision changed roots")
	}
}

func TestTriggerDDLStageAtomicity(t *testing.T) {
	for _, reason := range []string{"source", "cancel", "authorization", "role", "epoch"} {
		t.Run(reason, func(t *testing.T) {
			e, s, root, low, high, ext := triggerDDLTreeUnit(t)
			p := stored.TriggersOf(root)[0].Parts()
			p.Name = "next"
			p.AutoExtensions = []ids.ObjectAddress{ext.Address()}
			copy := *root
			copy.ObjectRevision++
			copy.Triggers = stored.TriggersPayload([]stored.Trigger{triggerDDLPartsUnit(t, p)})
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			stage.querySources = []catalog.Descriptor{ext}
			stage.authSession = s.op.Session
			stage.authRevision = s.op.Session.Authorization.Revision
			stage.roleRevision = e.roles.published.Revision
			if err := stage.Replace(&copy); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			prepared, err := e.ValidateCatalogStage(ctx, stage)
			if err != nil {
				t.Fatal(err)
			}
			switch reason {
			case "source":
				next := *ext
				next.ObjectRevision++
				e.catalog.state.SetDescriptor(ext.Address(), &next)
			case "cancel":
				cancel()
			case "authorization":
				s.op.Session.Authorization.Revision++
			case "role":
				e.roles.published.Revision++
			}
			token := PublicationToken{Epoch: stage.Base.Epoch()}
			if reason == "epoch" {
				token.Epoch++
			}
			_, err = e.PublishCatalog(prepared, token)
			if err == nil {
				t.Fatal("unfenced publication")
			}
			if reason == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			for _, old := range []*catalog.Relation{root, low, high} {
				if e.catalog.state.Get(old.Address()) != old {
					t.Fatal("failed stage changed relation", old.Name())
				}
			}
		})
	}
}

func TestTriggerDDLAutoExtensions(t *testing.T) {
	e, _, root, low, _, ext := triggerDDLTreeUnit(t)
	before := e.CatalogRead()
	p := stored.TriggersOf(low)[0].Parts()
	addresses := []ids.ObjectAddress{ext.Address(), ext.Address()}
	p.AutoExtensions = addresses
	definition := triggerDDLPartsUnit(t, p)
	addresses[0] = ids.ObjectAddress{}
	if !reflect.DeepEqual(definition.Parts().AutoExtensions, []ids.ObjectAddress{ext.Address()}) {
		t.Fatal("constructor retained input or duplicate")
	}
	low = triggerUnitInstall(t, e, low, definition)
	read := e.CatalogRead()
	if err := low.Triggers.Validate(read, low); err != nil {
		t.Fatal(err)
	}
	if err := catalogDependencyPreflight(read.State(), read.State(), []session.DependencyObservation{{Base: before.State(), Targets: []ids.ObjectAddress{ext.Address()}}}, nil); err == nil {
		t.Fatal("late automatic dependency was not fenced")
	}
	missing := p
	missing.AutoExtensions = []ids.ObjectAddress{{ClassID: ids.ExtensionClassID, ObjectID: 9999999}}
	bad := *low
	bad.Triggers = stored.TriggersPayload([]stored.Trigger{triggerDDLPartsUnit(t, missing)})
	if err := bad.Triggers.Validate(read, &bad); err == nil {
		t.Fatal("missing extension accepted")
	}
	edges, err := pgcatalog.CollectDepend(context.Background(), read, engineCatalogRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	owner := ids.ObjectAddress{ClassID: 2620, ObjectID: definition.OID()}
	count := 0
	for _, edge := range edges {
		if edge.Dependent == owner && edge.Referenced == ext.Address() {
			if edge.DepType != 'x' {
				t.Fatal("extension projected as normal/member", edge)
			}
			count++
		}
	}
	if count != 1 {
		t.Fatal("automatic edge projection", count)
	}
	stage := e.BeginCatalogStage(read)
	defer e.DiscardCatalogStage(stage)
	_, err = domainDependencyClosure(context.Background(), stage, []ids.ObjectAddress{ext.Address()})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" {
		t.Fatal("clone-only extension retirement bypassed partition owner", err)
	}
	// Retiring the parent trigger grants the clone deletion through the same graph.
	parent := stored.TriggersOf(root)[0].Parts()
	parent.AutoExtensions = []ids.ObjectAddress{ext.Address()}
	root = triggerUnitInstall(t, e, root, triggerDDLPartsUnit(t, parent))
	allowed := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(allowed)
	closure, err := domainDependencyClosure(context.Background(), allowed, []ids.ObjectAddress{ext.Address()})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(closure, func(edge table.DomainDependency) bool { return edge.Address == owner && edge.Automatic }) {
		t.Fatal("clone missing automatic closure")
	}
	// Exercise application and validation, not just discovery: automatic x
	// edges have no visible RESTRICT label but must prune attached payloads.
	rootStore, lowStore := e.storage.Relations[root.Address()], e.storage.Relations[low.Address()]
	if err := allowed.Remove(allowed.Base.Owner().Domain.Ref(allowed.Base.Epoch(), ext.Address())); err != nil {
		t.Fatal(err)
	}
	if err := stageExpressionDependencies(context.Background(), allowed, []ids.ObjectAddress{ext.Address()}, parser.DropRestrict, nil, nil, nil, ext.Address()); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, allowed)
	retired := e.CatalogRead()
	if retired.Get(ext.Address()) != nil || retired.MustRelation(root.Address()).HasTriggers() {
		t.Fatal("automatic extension retirement retained provider or parent trigger")
	}
	for _, address := range root.Partition.Children {
		if retired.MustRelation(address).HasTriggers() {
			t.Fatal("automatic extension retirement retained clone", address)
		}
	}
	if !root.HasTriggers() || !low.HasTriggers() || e.storage.Relations[root.Address()] != rootStore || e.storage.Relations[low.Address()] != lowStore {
		t.Fatal("retirement mutated source payloads or storage")
	}
	for _, bad := range []ids.ObjectAddress{{ClassID: ids.ProcedureClassID, ObjectID: ext.Address().ObjectID}, {ClassID: ids.ExtensionClassID, ObjectID: ext.Address().ObjectID, SubID: 1}} {
		p.AutoExtensions = []ids.ObjectAddress{bad}
		if _, err := stored.NewTrigger(p); err == nil {
			t.Fatal("invalid edge accepted", bad)
		}
	}
}

func TestTriggerDDLExtensionRemap(t *testing.T) {
	e, _, relation, function, ext := triggerDDLExtensionUnit(t)
	p := triggerUnitDefinition(t, e.CatalogRead(), relation, function, "a", stored.TriggerInsert).Parts()
	p.AutoExtensions = []ids.ObjectAddress{ext.Address()}
	relation = triggerUnitInstall(t, e, relation, triggerDDLPartsUnit(t, p))
	destination, s, _, target, targetFunction := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	// Displace the genuine installed extension rather than fabricating a provider.
	destination.mu.Lock()
	_, err := destination.catalog.allocateOIDLocked()
	destination.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	install := extensionUnitStage(t, destination, s.op)
	extensionUnitPublish(t, destination, s.op, install)
	read := destination.CatalogRead()
	next := read.MustExtension(read.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
	if next.Address() == ext.Address() {
		t.Fatal("extension identities were not displaced")
	}
	owner := *target
	owner.Triggers = relation.Triggers
	mapRef := func(ref ids.ObjectRef) ids.ObjectRef {
		switch ref.Address {
		case relation.Address():
			ref.Address = target.Address()
		case function.Address():
			ref.Address = targetFunction.Address()
		case ext.Address():
			ref.Address = next.Address()
		}
		return read.Owner().Domain.Ref(read.Epoch(), ref.Address)
	}
	mapped, err := stored.RemapTriggers(read, &owner, nil, mapRef, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stored.TriggersOf(mapped)[0].Parts().AutoExtensions[0] != next.Address() || stored.TriggersOf(relation)[0].Parts().AutoExtensions[0] != ext.Address() {
		t.Fatal("extension remap changed source or retained source authority")
	}
	if err := mapped.Triggers.Validate(read, mapped); err != nil {
		t.Fatal(err)
	}
	for _, missing := range []ids.ObjectAddress{ext.Address(), {ClassID: ids.ExtensionClassID, ObjectID: 9999999}} {
		_, err = stored.RemapTriggers(read, &owner, nil, func(ref ids.ObjectRef) ids.ObjectRef {
			if ref.Address == ext.Address() {
				return read.Owner().Domain.Ref(read.Epoch(), missing)
			}
			return mapRef(ref)
		}, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, nil, nil)
		if err == nil {
			t.Fatal("missing/stale destination extension accepted")
		}
	}
}

func TestTriggerDDLAdmission(t *testing.T) {
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "t"}}}
	for _, tc := range []struct {
		stmt        parser.Statement
		mode        lock.Mode
		transaction bool
	}{
		{&parser.CreateTrigger{Table: name}, lock.ShareRowExclusive, false},
		{&parser.AlterTrigger{Table: name, Action: "rename"}, lock.AccessExclusive, true},
		{&parser.AlterTrigger{Table: name, Action: "depends"}, lock.AccessShare, true},
		{&parser.DropTrigger{Table: name}, lock.AccessShare, true},
	} {
		target, ok, err := table.TargetAdmission(tc.stmt, true)
		if err != nil || !ok || target.Mode != tc.mode || transactionalSchemaUtility(tc.stmt) != tc.transaction {
			t.Fatal("lifecycle admission", target, err)
		}
	}
	for _, action := range []string{"rename", "no_depends"} {
		if err := control.CheckAlterTrigger(&parser.AlterTrigger{Table: parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: "t"}}}, Action: action}); err == nil {
			t.Fatal("restore widened lifecycle allowlist")
		}
	}
}
