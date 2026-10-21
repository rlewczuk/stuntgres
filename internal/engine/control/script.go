package control

import (
	"context"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
)

// dumpScript is a captured Dump made ready to emit: the ordered events and
// the statements each section and phase writes. buildDumpScript completes
// the capture's graph and its deferral decision; nothing reads the engine.
type dumpScript struct {
	*Capture

	policyGroups, ruleGroups, securityGroups  []dump.Group
	domainNodes                               map[ids.OID]*catalog.Type
	collationStatements, conversionStatements []dump.Statement
	seedStatements, conversionSeeds           []string
	conversionSchemas                         []string
	sequenceCreation, sequenceCompletion      []string
	invalidReplay                             map[ids.OID]partition.InvalidReplay
	eventOrder                                []dump.Event
	declarations                              map[ids.OID]dump.Statement
	operatorLinks                             map[ids.OID]string
}

// buildDumpScript renders the captured groups and statements and orders
// the event graph.
func buildDumpScript(ctx context.Context, capture *Capture) (*dumpScript, error) {
	s := &dumpScript{Capture: capture}
	var err error
	if s.policyGroups, err = stored.PolicyDumpGroups(ctx, s.OriginalCatalog, s.OriginalRoles); err != nil {
		return nil, err
	}
	if s.ruleGroups, err = stored.RuleDumpGroups(ctx, s.OriginalCatalog, s.OriginalRoles); err != nil {
		return nil, err
	}
	if s.securityGroups, err = stored.RowSecurityDumpGroups(ctx, s.CatalogView, s.DomainRoles); err != nil {
		return nil, err
	}
	if err := s.addDeclarationEdges(); err != nil {
		return nil, err
	}
	if err := s.renderTypeStatements(ctx); err != nil {
		return nil, err
	}
	viewOrder, err := s.order(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.renderDeclarations(ctx, viewOrder); err != nil {
		return nil, err
	}
	s.sequenceCreation, s.sequenceCompletion = sequence.DumpStatements(s.Sequences)
	return s, nil
}

// addDeclarationEdges adds the domain, operator and SQL routine declarations
// to the graph and rewrites relation predecessors to their column sources.
func (s *dumpScript) addDeclarationEdges() error {
	s.domainNodes = make(map[ids.OID]*catalog.Type, len(s.Domains))
	for _, d := range s.Domains {
		id := d.Address().ObjectID
		s.domainNodes[id] = d
		s.Graph[dump.Event{OID: id}] = nil
		if d.SQLType.IsDomain() {
			predecessor, err := Resolver{}.Predecessor(s.CatalogView, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.SQLType.DomainBase().Type.ID())}, d.Address())
			if err != nil {
				return err
			}
			if predecessor != 0 {
				s.Graph[dump.Event{OID: id}] = []dump.Event{{OID: predecessor}}
			}
			if d.DomainDefault != nil {
				for _, dependency := range d.DomainDefault.Dependencies {
					predecessor, err := Resolver{}.Predecessor(s.CatalogView, dependency.Address, d.Address())
					if err != nil {
						return err
					}
					event, dependencyEvent := dump.Event{OID: id}, dump.Event{OID: predecessor}
					if predecessor != 0 && !slices.Contains(s.Graph[event], dependencyEvent) {
						s.Graph[event] = append(s.Graph[event], dependencyEvent)
					}
				}
			}
			for _, check := range d.DomainChecks {
				if check.NotValid {
					continue
				}
				for _, dependency := range check.Dependencies {
					predecessor, err := Resolver{}.Predecessor(s.CatalogView, dependency.Ref.Address, d.Address())
					if err != nil {
						return err
					}
					event := dump.Event{OID: id}
					dependencyEvent := dump.Event{OID: predecessor}
					if predecessor != 0 && !slices.Contains(s.Graph[event], dependencyEvent) {
						s.Graph[event] = append(s.Graph[event], dependencyEvent)
					}
				}
			}
		}
	}
	if err := routine.OperatorDumpDependencies(Resolver{}, s.CatalogView, s.Operators, s.Graph); err != nil {
		return err
	}
	// Reconstruct final links, not allocation history: asymmetric links remain
	// in CREATE, reciprocal defined links finish in endpoint-local ALTER events.
	routine.OperatorDumpEvents(s.CatalogView, s.Operators, s.Graph)
	if err := routine.SQLRoutineDumpEvents(Resolver{}, s.CatalogView, s.SQLRoutines, s.Graph); err != nil {
		return err
	}
	for event, predecessors := range s.Graph {
		id := event.OID
		relation, ok := s.CatalogView.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id})
		if !ok {
			continue
		}
		columns, err := ColumnPredecessors(s.CatalogView, relation, dump.DeclarationIDs(predecessors))
		if err != nil {
			return err
		}
		s.Graph[event] = dump.DeclarationEvents(columns)
		if inline, ok := s.InlinePredecessors[id]; ok {
			inline, err = ColumnPredecessors(s.CatalogView, relation, inline)
			if err != nil {
				return err
			}
			s.InlinePredecessors[id] = inline
		}
	}
	return nil
}

// renderTypeStatements renders the collation and conversion declarations and
// the seeded ones a changed catalog must recreate.
func (s *dumpScript) renderTypeStatements(ctx context.Context) error {
	var err error
	if s.collationStatements, err = typedef.CollationDumpStatements(ctx, s.CatalogView, s.DomainRoles, s.Collations); err != nil {
		return err
	}
	if s.seedStatements, err = typedef.CollationSeedDumpStatements(ctx, s.CatalogView, s.DomainRoles); err != nil {
		return err
	}
	if s.conversionStatements, s.conversionSchemas, err = typedef.ConversionDumpStatements(ctx, s.CatalogView, s.DomainRoles, s.Conversions); err != nil {
		return err
	}
	s.conversionSeeds, err = typedef.ConversionSeedDumpStatements(ctx, s.CatalogView, s.DomainRoles)
	return err
}

// order sorts the declarations, decides default deferral, adds the later
// phases and sorts every event. It returns the declaration order.
func (s *dumpScript) order(ctx context.Context) ([]ids.OID, error) {
	if s.DeferDefaults {
		for oid, predecessors := range s.InlinePredecessors {
			s.Graph[dump.Event{OID: oid}] = dump.DeclarationEvents(predecessors)
		}
	}
	// Capture collision-remap provenance from the same immutable snapshot as
	// the graph, before any dump-private naming substitutions.
	var priorities map[ids.OID]ids.OID
	for address, priority := range s.OriginalCatalog.State().DumpPriorities() {
		if _, emitted := s.Graph[dump.Event{OID: address.ObjectID}]; emitted {
			if priorities == nil {
				priorities = make(map[ids.OID]ids.OID)
			}
			priorities[address.ObjectID] = priority
		}
	}
	if err := table.PhysicalNotNullDumpDependencies(Resolver{}, s.OriginalCatalog, s.Graph); err != nil {
		return nil, err
	}
	routine.NormalizeOperatorDumpPredecessors(s.CatalogView, s.Graph)
	declarationOrder, err := dump.Order(ctx, s.Graph, priorities)
	if err != nil && !s.DeferDefaults && ctx.Err() == nil && len(s.InlinePredecessors) != 0 {
		for oid, predecessors := range s.InlinePredecessors {
			s.Graph[dump.Event{OID: oid}] = dump.DeclarationEvents(predecessors)
		}
		if err := table.PhysicalNotNullDumpDependencies(Resolver{}, s.OriginalCatalog, s.Graph); err != nil {
			return nil, err
		}
		routine.NormalizeOperatorDumpPredecessors(s.CatalogView, s.Graph)
		declarationOrder, err = dump.Order(ctx, s.Graph, priorities)
		s.DeferDefaults = err == nil
	}
	if err != nil {
		return nil, err
	}
	for i := range s.Tables {
		if err := s.Tables[i].PrepareInheritedRecipes(s.OriginalCatalog); err != nil {
			return nil, err
		}
	}
	viewOrder := dump.DeclarationIDs(declarationOrder)
	if err := partition.DumpEvents(ctx, Resolver{}, s.OriginalCatalog, s.Graph, dump.DeclarationEvents(viewOrder), s.Tables, s.IndexIndices, s.ConstraintReplay, s.DeferDefaults); err != nil {
		return nil, err
	}
	s.invalidReplay = partition.InvalidDumpEvents(s.OriginalCatalog, s.Graph, s.IndexIndices)
	if err := regobj.DomainInvalidDumpEvents(ctx, Resolver{}, s.CatalogView, s.Graph, s.domainNodes); err != nil {
		return nil, err
	}
	routine.NormalizeOperatorDumpPredecessors(s.CatalogView, s.Graph)
	if s.eventOrder, err = dump.Order(ctx, s.Graph, priorities); err != nil {
		return nil, err
	}
	return viewOrder, nil
}

// renderDeclarations renders the domain, composite, SQL routine and operator
// declarations and the operators' deferred link statements.
func (s *dumpScript) renderDeclarations(ctx context.Context, viewOrder []ids.OID) error {
	var err error
	if s.declarations, err = typedef.MixedDumpStatements(ctx, Presenter{}, s.CatalogView, s.DomainRoles, viewOrder, s.domainNodes, s.Composites); err != nil {
		return err
	}
	for _, d := range s.SQLRoutines {
		declaration, err := routine.SQLRoutineDumpStatement(Presenter{}, s.CatalogView, s.DomainRoles, d)
		if err != nil {
			return err
		}
		s.declarations[d.Address().ObjectID] = declaration
	}
	s.operatorLinks, err = routine.OperatorDumpStatements(ctx, Presenter{}, s.CatalogView, s.DomainRoles, s.Operators, s.declarations)
	return err
}
