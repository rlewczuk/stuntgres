package control

import (
	"fmt"
	"sort"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// These records are dump-local requests from retained executable expressions,
// not catalog objects or a second coercion registry. Each window is real SQL.
type castReplay struct {
	Cast      catalog.Cast
	SQL, Drop string
	Raw       bool
}

func sameReplayCast(a, b *catalog.Cast) bool {
	// Generic frozen recipes omit signature adapters. Authentic extension edges
	// check those fields separately before relying on their installation.
	return a.Source == b.Source && a.Target == b.Target && a.Method == b.Method && a.Context == b.Context && a.Function == b.Function && a.GeneratedRange == b.GeneratedRange
}
func castContext(context string, mode sqltype.CoercionContext) string {
	if context != "" {
		return context
	}
	if mode == sqltype.Implicit {
		return "i"
	}
	if mode == sqltype.Assignment {
		return "a"
	}
	return "e"
}
func elementCastRoutine(node expr.Expr) *catalog.Procedure {
	if node == nil {
		return nil
	}
	if p, ok := expr.InfoOf(node).RoutineReference.(*catalog.Procedure); ok {
		return p
	}
	var selected *catalog.Procedure
	plan.ExpressionChildren(node, func(child expr.Expr) {
		if selected == nil {
			selected = elementCastRoutine(child)
		}
	})
	return selected
}
func (s *dumpScript) prepareCastWindows() error {
	s.castWindows = make(map[castConsumer][]castReplay)
	requests := make(map[castConsumer][]catalog.Cast)
	var failure error
rows:
	for _, relations := range [][]table.DumpRelation{s.Tables, s.Materialized} {
		for _, relation := range relations {
			for _, version := range relation.Versions {
				if !visibility.LiveVersion(version, relation.Frontier, relation.Ends, nil) {
					continue
				}
				for i, value := range version.Values {
					if _, generated := relation.Descriptor.ColumnMeta.Generations[relation.Attributes[i]]; !generated && regobj.NeedsFloat4RawReplay(value) {
						s.rawFloat4 = true
						break rows
					}
				}
			}
		}
	}
	add := func(site castConsumer, d catalog.Cast) {
		if failure != nil {
			return
		}
		// Protected fixed edges already have their real provider on replay.
		for i := range sqltype.CastMetadataCount() {
			fixed, _ := sqltype.CastMetadataAt(i)
			if fixed.Source == d.Source && fixed.Target == d.Target {
				return
			}
		}
		for i := range requests[site] {
			old := &requests[site][i]
			if old.Source == d.Source && old.Target == d.Target {
				if !sameReplayCast(old, &d) {
					failure = fmt.Errorf("stuntgres: dump consumer %d phase %d attribute %d check %q requires conflicting frozen casts from %d to %d", site.Event.OID, site.Event.Phase, site.Attribute, site.Check, d.Source, d.Target)
				}
				return
			}
		}
		requests[site] = append(requests[site], d)
	}
	walkMember := func(site castConsumer, node expr.Expr) {
		s.rawFloat4 = replayCasts(node, func(d catalog.Cast) { add(site, d) }) || s.rawFloat4
	}
	walk := func(event dump.Event, node expr.Expr) {
		walkMember(castConsumer{Event: event}, node)
	}
	for _, descriptor := range s.CatalogView.Descriptors() {
		switch d := descriptor.(type) {
		case *catalog.Relation:
			if v := stored.ViewOf(d); v != nil {
				plan.VisitCTEQuery(v.Query().Root, func(plan.Node) {}, func(node expr.Expr) { walk(dump.Event{OID: d.Address().ObjectID}, node) }, true)
			}
			if d.Foreign != nil {
				event := dump.Event{OID: d.Address().ObjectID}
				for attribute, definition := range d.ColumnMeta.Defaults {
					site := castConsumer{Event: event}
					if s.ForeignDefaults[event.OID][attribute] {
						site.Attribute = attribute
						site.ForeignDefault = true
					}
					walkMember(site, definition.Expression)
				}
				for _, generation := range d.ColumnMeta.Generations {
					walk(event, generation.Expression)
				}
				for _, check := range d.Constraints.Check {
					walk(event, check.Expression)
				}
			}
			if at, ok := s.TableIndices[d.Address().ObjectID]; ok {
				t := &s.Tables[at]
				for attribute, definition := range t.Defaults {
					site := castConsumer{Event: dump.Event{OID: d.Address().ObjectID}}
					if s.DeferDefaults && t.DeferredDefinitions[attribute] {
						site.Event.Phase = dump.Defaults
						site.Attribute = attribute
					}
					site = s.attributeConsumer(site, attribute, "")
					walkMember(site, definition.Expression)
				}
				for attribute, generation := range d.ColumnMeta.Generations {
					site := castConsumer{Event: dump.Event{OID: d.Address().ObjectID}}
					if t.RecipePlaceholders {
						site.Event.Phase = dump.InheritanceRecipes
						site.Attribute = attribute
					}
					site = s.attributeConsumer(site, attribute, "")
					walkMember(site, generation.Expression)
				}
				for _, check := range d.Constraints.Check {
					if !table.DumpCheckDeclaration(s.OriginalCatalog, d, check) {
						continue
					}
					site := castConsumer{Event: dump.Event{OID: d.Address().ObjectID}}
					if check.NotValid {
						site.Event.Phase = dump.InvalidConstraints
						site.Check = check.Name
					} else if t.DeferredChecks[check.Name] {
						site.Event.Phase = dump.InheritanceChecks
						site.Check = check.Name
					}
					site = s.attributeConsumer(site, 0, check.Name)
					walkMember(site, check.Expression)
				}
			}
		case *catalog.Type:
			event := dump.Event{OID: d.Address().ObjectID}
			if d.DomainDefault != nil {
				walk(event, d.DomainDefault.Expression)
			}
			for _, check := range d.DomainChecks {
				site := castConsumer{Event: event}
				if check.NotValid || s.deferredDomainChecks[event.OID][check.Name] {
					site.Event.Phase = dump.DomainChecks
					site.Check = check.Name
				}
				walkMember(site, check.Expression)
			}
		case *catalog.Procedure:
			if d.Aggregate != nil && d.DynamicCallable() && d.Aggregate.BinaryRecipe != nil {
				for _, edge := range d.Aggregate.BinaryRecipe.Casts {
					add(castConsumer{Event: dump.Event{OID: d.Address().ObjectID}}, edge)
				}
			}
			if layout := d.RoutineDefinition(); layout != nil && d.SQLDefaults != nil {
				for slot := range layout.Inputs {
					if node, ok := d.SQLDefaults.Default(slot); ok {
						walk(dump.Event{OID: d.Address().ObjectID}, node)
					}
				}
			}
			if d.SQL != nil && d.SQL.Standard {
				program, err := stored.RoutineProgramOf(d)
				if err != nil {
					return err
				}
				for i := range program.Len() {
					binding := program.Action(i).Binding()
					visit := func(node expr.Expr) { walk(dump.Event{OID: d.Address().ObjectID}, node) }
					stored.VisitRuleActionExpressions(binding, visit)
					if binding.Query != nil {
						plan.VisitCTEQuery(binding.Query.Root, func(plan.Node) {}, visit, true)
					}
				}
			}
		}
	}
	if failure != nil {
		return failure
	}
	// If a historical consumer needs a different row, all consumers of that
	// pair use windows. Install the authentic final object once, at the end.
	type pair struct{ source, target sqltype.ID }
	postponed := make(map[pair]bool)
	for _, required := range requests {
		for _, d := range required {
			if final, exists := s.CatalogView.Cast(d.Source, d.Target); exists && !sameReplayCast(final, &d) {
				if final.Origin != nil {
					return fmt.Errorf("stuntgres: historical cast conflicts with installed extension")
				}
				postponed[pair{d.Source, d.Target}] = true
			}
		}
	}
	// Late foreign statements are actual post-rules consumers, not graph
	// DEFAULT events. Keep their authentic final rows absent until that
	// section finishes; every earlier consumer of the pair uses this same
	// window authority. Cast adapters follow the existing dependency graph.
	s.lateForeignCasts = make(map[ids.OID]bool)
	for site, required := range requests {
		if !site.ForeignDefault {
			continue
		}
		for _, d := range required {
			if postponed[pair{d.Source, d.Target}] {
				if final, ok := s.CatalogView.Cast(d.Source, d.Target); ok {
					s.lateForeignCasts[final.OID] = true
				}
			}
		}
	}
	for _, descriptor := range s.CatalogView.Descriptors() {
		final, ok := descriptor.(*catalog.Cast)
		if !ok || final.GeneratedRange != (ids.ObjectAddress{}) || final.Origin != nil || s.lateForeignCasts[final.OID] {
			continue
		}
		for oid := range s.lateForeignCasts {
			if s.Graph.DependsOn(dump.Event{OID: final.OID}, dump.Event{OID: oid}, make(map[dump.Event]bool)) {
				s.lateForeignCasts[final.OID] = true
				postponed[pair{final.Source, final.Target}] = true
				break
			}
		}
	}
	for site, required := range requests {
		event := site.Event
		// Signature adapters must exist before a temporary WITH FUNCTION row.
		sort.SliceStable(required, func(i, j int) bool { return required[i].Method != "f" && required[j].Method == "f" })
		for _, d := range required {
			if d.GeneratedRange != (ids.ObjectAddress{}) {
				actual, ok := s.CatalogView.Cast(d.Source, d.Target)
				if !ok || !sameReplayCast(actual, &d) {
					return fmt.Errorf("stuntgres: stale generated range cast")
				}
				if err := (catalog.CastProvider{}).Validate(s.CatalogView, actual); err != nil {
					return err
				}
				if !site.ForeignDefault {
					oid, err := predecessor(s.CatalogView, d.GeneratedRange, ids.ObjectAddress{})
					if err != nil {
						return err
					}
					s.Graph.Add(event, dump.Event{OID: oid})
				}
				continue
			}
			final, exists := s.CatalogView.Cast(d.Source, d.Target)
			if exists && final.Origin != nil {
				if !sameReplayCast(final, &d) || final.InputCast != d.InputCast || final.ResultCast != d.ResultCast {
					return fmt.Errorf("stuntgres: stale extension cast replay")
				}
				if err := (catalog.CastProvider{}).Validate(s.CatalogView, final); err != nil {
					return err
				}
				continue
			}
			if d.Origin != nil {
				return fmt.Errorf("stuntgres: unavailable installed cast replay")
			}
			raw := s.rawFloat4 && d.Source == sqltype.Xid && d.Target == sqltype.Float4
			if raw && d.Method == "b" && d.Context == "e" {
				continue
			}
			if exists && sameReplayCast(final, &d) && !postponed[pair{d.Source, d.Target}] && !(raw && d.Method != "b") {
				if !site.ForeignDefault {
					s.Graph.Add(event, dump.Event{OID: final.OID})
				}
				continue
			}
			declaration, err := castDumpStatement(s.CatalogView, &d)
			if err != nil {
				return err
			}
			source, _ := s.CatalogView.LookupType(d.Source)
			target, _ := s.CatalogView.LookupType(d.Target)
			x, err := (Presenter{}).ColumnDeclaration(s.CatalogView, catalog.Column{Usage: source.Usage()})
			if err != nil {
				return err
			}
			y, err := (Presenter{}).ColumnDeclaration(s.CatalogView, catalog.Column{Usage: target.Usage()})
			if err != nil {
				return err
			}
			s.castWindows[site] = append(s.castWindows[site], castReplay{Cast: d, SQL: declaration.SQL, Drop: "DROP CAST (" + x + " AS " + y + ");\n", Raw: raw})
			for _, dependency := range catalog.CastDependencies(&d) {
				predecessor, err := predecessor(s.CatalogView, dependency, ids.ObjectAddress{})
				if err != nil {
					return err
				}
				if predecessor != 0 && !site.ForeignDefault {
					s.Graph.Add(event, dump.Event{OID: predecessor})
				}
			}
			if exists {
				if !site.ForeignDefault {
					s.Graph.Add(dump.Event{OID: final.OID}, event)
				}
				if raw && final.Method == "b" {
					s.rawFinalDeferred = true
				}
			}
		}
	}
	if s.rawFloat4 && !s.rawFinalDeferred {
		if final, ok := s.CatalogView.Cast(sqltype.Xid, sqltype.Float4); ok && final.Method == "b" {
			for event := range s.Graph {
				// Installation barriers are emitted in the prefix, before raw
				// codec state. Making them wait for this ordinary CAST creates
				// a cycle through the CAST's own installation predecessor.
				if _, installation := s.CatalogView.Extension(ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: event.OID}); installation {
					continue
				}
				if event.OID != final.OID {
					s.Graph.Add(event, dump.Event{OID: final.OID})
				}
			}
		}
	}
	return nil
}
func (s *dumpScript) openCastWindow(w *dumpWriter, site castConsumer) error {
	for _, request := range s.castWindows[site] {
		if err := w.setOwner("postgres"); err != nil {
			return err
		}
		if request.Raw {
			if !s.rawHelper {
				return fmt.Errorf("stuntgres: frozen raw cast window has no bootstrap owner")
			}
			if err := w.emit("DROP CAST (pg_catalog.xid AS pg_catalog.float4);\n"); err != nil {
				return err
			}
		}
		if err := w.emit(request.SQL); err != nil {
			return err
		}
	}
	return nil
}
func (s *dumpScript) closeCastWindow(w *dumpWriter, site castConsumer) error {
	requests := s.castWindows[site]
	for i := len(requests) - 1; i >= 0; i-- {
		request := requests[i]
		if err := w.setOwner("postgres"); err != nil {
			return err
		}
		if err := w.emit(request.Drop); err != nil {
			return err
		}
		if request.Raw {
			if err := w.emit("CREATE CAST (pg_catalog.xid AS pg_catalog.float4) WITHOUT FUNCTION;\n"); err != nil {
				return err
			}
		}
	}
	return nil
}
