package control

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

// A phase can emit several independent SQL statements. Only the declaration
// itself is indivisible; deferred defaults and checks bind one member at a time.
type castConsumer struct {
	Event     dump.Event
	Attribute int32
	Check     string
	// ForeignDefault identifies the existing post-rules section, not a graph
	// Defaults event. Its attribute is one actual ALTER FOREIGN TABLE.
	ForeignDefault bool
}

func replayCasts(node expr.Expr, add func(catalog.Cast)) bool {
	if node == nil {
		return false
	}
	if _, virtual := node.(plan.ColumnExpr); virtual {
		return false
	}
	raw := false
	switch n := node.(type) {
	case plan.LiteralExpr:
		raw = regobj.NeedsFloat4RawReplay(n.Value)
	case plan.SQLRoutineExpr:
		// Omitted defaults bind in the routine's own declaration, not this
		// call's SQL. Walking expanded executable arguments invents conflicts.
		for _, arg := range n.Args[:n.WrittenCount] {
			raw = replayCasts(arg, add) || raw
		}
		return raw
	case plan.DatumCastExpr:
		add(catalog.Cast{Source: n.Child.ResultUsage().Type.ID(), Target: n.Usage.Type.ID(), Method: n.Method, Context: castContext(n.Context, n.Mode)})
	case plan.ArrayCastExpr:
		if p := elementCastRoutine(n.Element); p != nil {
			add(catalog.Cast{Source: n.Child.ResultUsage().Type.Element().BaseType().ID(), Target: n.Usage.Type.Element().BaseType().ID(), Method: "f", Context: castContext(n.Context, n.Mode), Function: p.Address()})
		}
	case plan.CaseExpr:
		if n.Test != nil {
			for _, arm := range n.Arms {
				if p := elementCastRoutine(arm.Test); p != nil {
					add(catalog.Cast{Source: n.Test.ResultUsage().Type.ID(), Target: arm.TestUsage.Type.ID(), Method: "f", Context: castContext(arm.TestContext, sqltype.Implicit), Function: p.Address()})
				}
			}
		}
	}
	plan.ExpressionChildren(node, func(child expr.Expr) { raw = replayCasts(child, add) || raw })
	return raw
}

// This is a declaration-layout decision, not window collection. Independently
// altered defaults/checks can retain different historical recipes for one pair.
// Move only affected members to the existing real ALTER statement phases.
func (s *dumpScript) separateCastConsumers() error {
	var moved, generationSeeds map[ids.ObjectAddress]bool
	for i := range s.Tables {
		t := &s.Tables[i]
		type pair struct{ source, target sqltype.ID }
		seen := make(map[pair]catalog.Cast)
		conflicts := make(map[pair]bool)
		collect := func(d catalog.Cast) {
			p := pair{d.Source, d.Target}
			if old, ok := seen[p]; ok && !sameReplayCast(&old, &d) {
				conflicts[p] = true
			}
			seen[p] = d
		}
		for _, d := range t.Defaults {
			replayCasts(d.Expression, collect)
		}
		for _, c := range t.Descriptor.Constraints.Check {
			replayCasts(c.Expression, collect)
		}
		for _, generation := range t.Descriptor.ColumnMeta.Generations {
			replayCasts(generation.Expression, collect)
		}
		if len(conflicts) == 0 {
			continue
		}
		affected := func(node expr.Expr) bool {
			found := false
			replayCasts(node, func(d catalog.Cast) { found = found || conflicts[pair{d.Source, d.Target}] })
			return found
		}
		if moved == nil {
			moved = make(map[ids.ObjectAddress]bool)
		}
		moved[t.Descriptor.Address()] = true
		for _, generation := range t.Descriptor.ColumnMeta.Generations {
			if affected(generation.Expression) {
				if generationSeeds == nil {
					generationSeeds = make(map[ids.ObjectAddress]bool)
				}
				generationSeeds[t.Descriptor.Address()] = true
			}
		}
		for attribute, d := range t.Defaults {
			if affected(d.Expression) {
				if t.DeferredDefinitions == nil {
					t.DeferredDefinitions = make(map[int32]bool)
				}
				t.DeferredDefinitions[attribute] = true
				s.DeferDefaults = true
			}
		}
		for _, c := range t.Descriptor.Constraints.Check {
			if !c.NotValid && affected(c.Expression) {
				if t.DeferredChecks == nil {
					t.DeferredChecks = make(map[string]bool)
				}
				t.DeferredChecks[c.Name] = true
			}
		}
	}
	if len(generationSeeds) != 0 {
		table.PrepareGenerationPlaceholders(s.OriginalCatalog, s.Tables, generationSeeds)
	}
	for i := range s.Tables {
		t := &s.Tables[i]
		if !moved[t.Descriptor.Address()] && !(len(generationSeeds) != 0 && t.RecipePlaceholders) {
			continue
		}
		if err := s.rebuildTableDeclarationEdges(t); err != nil {
			return err
		}
	}
	s.deferredDomainChecks = make(map[ids.OID]map[string]bool)
	for _, domain := range s.Domains {
		oid := domain.Address().ObjectID
		type pair struct{ source, target sqltype.ID }
		seen := make(map[pair]catalog.Cast)
		conflicts := make(map[pair]bool)
		collect := func(d catalog.Cast) {
			p := pair{d.Source, d.Target}
			if old, ok := seen[p]; ok && !sameReplayCast(&old, &d) {
				conflicts[p] = true
			}
			seen[p] = d
		}
		if domain.DomainDefault != nil {
			replayCasts(domain.DomainDefault.Expression, collect)
		}
		for _, check := range domain.DomainChecks {
			replayCasts(check.Expression, collect)
		}
		for _, check := range domain.DomainChecks {
			if check.NotValid {
				continue
			}
			affected := false
			replayCasts(check.Expression, func(d catalog.Cast) {
				affected = affected || conflicts[pair{d.Source, d.Target}]
			})
			if affected {
				if s.deferredDomainChecks[oid] == nil {
					s.deferredDomainChecks[oid] = make(map[string]bool)
				}
				s.deferredDomainChecks[oid][check.Name] = true
			}
		}
	}
	return nil
}

func (s *dumpScript) addSeparatedCheckEvents() error {
	for i := range s.Tables {
		t := &s.Tables[i]
		event := dump.Event{OID: t.Descriptor.Address().ObjectID, Phase: dump.InheritanceChecks}
		recursive := false
		for _, check := range t.Descriptor.Constraints.Check {
			if check.NotValid || !t.DeferredChecks[check.Name] || !table.DumpCheckDeclaration(s.OriginalCatalog, t.Descriptor, check) {
				continue
			}
			s.Graph.Add(event, dump.Event{OID: event.OID})
			recursive = recursive || !t.RecipePlaceholders && !check.NoInherit
			add := func(address ids.ObjectAddress) error {
				oid, err := predecessor(s.CatalogView, address, t.Descriptor.Address())
				if err == nil && oid != 0 {
					s.Graph.Add(event, dump.Event{OID: oid})
				}
				return err
			}
			if err := regobj.ClassReplayReferences(s.CatalogView, check.Expression, add); err != nil {
				return err
			}
			for _, dependency := range check.Dependencies {
				if err := add(dependency.Ref.Address); err != nil {
					return err
				}
			}
			s.Graph.Add(dump.Event{OID: event.OID, Phase: dump.Rows}, event)
		}
		if recursive && catalog.InheritanceHasChildren(s.OriginalCatalog, t.Descriptor.Address()) {
			// Use the ordinary executor's inheritance propagation. All intended
			// descendants exist before ADD, and no affected row precedes CHECK.
			seen := make(map[ids.ObjectAddress]bool)
			var require func(*catalog.Relation)
			require = func(parent *catalog.Relation) {
				for _, child := range catalog.InheritanceChildren(s.OriginalCatalog, parent.Address()) {
					if seen[child.Address()] {
						continue
					}
					seen[child.Address()] = true
					s.Graph.Add(event, dump.Event{OID: child.Address().ObjectID})
					s.Graph.Add(dump.Event{OID: child.Address().ObjectID, Phase: dump.Rows}, event)
					require(child)
				}
			}
			require(t.Descriptor)
		}
	}
	return nil
}

func (s *dumpScript) emitCastConsumer(w *dumpWriter, site castConsumer, owner string, emit func() error) error {
	if err := s.openCastWindow(w, site); err != nil {
		return err
	}
	if err := w.setOwner(owner); err != nil {
		return err
	}
	if err := emit(); err != nil {
		return err
	}
	return s.closeCastWindow(w, site)
}

func (s *dumpScript) addSeparatedDomainCheckEvents() error {
	for oid, names := range s.deferredDomainChecks {
		event := dump.Event{OID: oid, Phase: dump.DomainChecks}
		s.Graph.Add(event, dump.Event{OID: oid})
		for other := range s.Graph {
			if other.Phase == dump.TableCompletion {
				s.Graph.Add(event, other)
			}
		}
		add := func(address ids.ObjectAddress) error {
			provider, err := predecessor(s.CatalogView, address, ids.ObjectAddress{})
			if err == nil && provider != 0 {
				s.Graph.Add(event, dump.Event{OID: provider})
			}
			return err
		}
		for _, check := range s.domainNodes[oid].DomainChecks {
			if !names[check.Name] {
				continue
			}
			if err := regobj.ClassReplayReferences(s.CatalogView, check.Expression, add); err != nil {
				return err
			}
			for _, dependency := range check.Dependencies {
				if err := add(dependency.Ref.Address); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Keep the collector as the single dependency owner after changing CREATE's
// members; moved CHECK/generation providers must not leave false CREATE cycles.
func (s *dumpScript) rebuildTableDeclarationEdges(t *table.DumpRelation) error {
	c := dumpCollection{Capture: s.Capture}
	edges, err := c.tableDumpPredecessors(t.Descriptor, t)
	if err != nil {
		return err
	}
	oid := t.Descriptor.Address().ObjectID
	predecessors := edges.inline
	if s.InlinePredecessors == nil {
		s.InlinePredecessors = make(map[ids.OID][]ids.OID)
	}
	s.InlinePredecessors[oid] = edges.inline
	if !s.DeferDefaults {
		predecessors = slices.Clone(predecessors)
		for _, predecessor := range edges.deferred {
			if !slices.Contains(predecessors, predecessor) {
				predecessors = append(predecessors, predecessor)
			}
		}
	}
	s.Graph[dump.Event{OID: oid}] = dump.DeclarationEvents(predecessors)
	return nil
}
