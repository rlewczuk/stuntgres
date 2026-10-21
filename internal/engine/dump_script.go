package engine

import (
	"context"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/typedef"
)

// dumpScript is a captured Dump made ready to emit: the ordered events and
// the statements each section and phase writes. buildDumpScript completes
// the capture's graph and its deferral decision; nothing reads the engine.
type dumpScript struct {
	*dumpCapture

	policyGroups, ruleGroups, securityGroups  []dump.Group
	domainNodes                               map[ids.OID]*catalog.Type
	collationStatements, conversionStatements []dump.Statement
	seedStatements, conversionSeeds           []string
	conversionSchemas                         []string
	sequenceCreation, sequenceCompletion      []string
	invalidReplay                             map[ids.OID]partitionInvalidReplay
	eventOrder                                []dump.Event
	declarations                              map[ids.OID]dump.Statement
	operatorLinks                             map[ids.OID]string
}

// buildDumpScript renders the captured groups and statements and orders
// the event graph.
func buildDumpScript(ctx context.Context, capture *dumpCapture) (*dumpScript, error) {
	s := &dumpScript{dumpCapture: capture}
	var err error
	if s.policyGroups, err = policyDumpGroups(ctx, s.originalCatalog, s.originalRoles); err != nil {
		return nil, err
	}
	if s.ruleGroups, err = ruleDumpGroups(ctx, s.originalCatalog, s.originalRoles); err != nil {
		return nil, err
	}
	if s.securityGroups, err = rowSecurityDumpGroups(ctx, s.catalogView, s.domainRoles); err != nil {
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
	s.sequenceCreation, s.sequenceCompletion = sequence.DumpStatements(s.sequences)
	return s, nil
}

// addDeclarationEdges adds the domain, operator and SQL routine declarations
// to the graph and rewrites relation predecessors to their column sources.
func (s *dumpScript) addDeclarationEdges() error {
	s.domainNodes = make(map[ids.OID]*catalog.Type, len(s.domains))
	for _, d := range s.domains {
		id := d.Address().ObjectID
		s.domainNodes[id] = d
		s.graph[dump.Event{OID: id}] = nil
		if d.SQLType.IsDomain() {
			predecessor, err := dumpPredecessor(s.catalogView, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.SQLType.DomainBase().Type.ID())}, d.Address())
			if err != nil {
				return err
			}
			if predecessor != 0 {
				s.graph[dump.Event{OID: id}] = []dump.Event{{OID: predecessor}}
			}
			if d.DomainDefault != nil {
				for _, dependency := range d.DomainDefault.Dependencies {
					predecessor, err := dumpPredecessor(s.catalogView, dependency.Address, d.Address())
					if err != nil {
						return err
					}
					event, dependencyEvent := dump.Event{OID: id}, dump.Event{OID: predecessor}
					if predecessor != 0 && !slices.Contains(s.graph[event], dependencyEvent) {
						s.graph[event] = append(s.graph[event], dependencyEvent)
					}
				}
			}
			for _, check := range d.DomainChecks {
				if check.NotValid {
					continue
				}
				for _, dependency := range check.Dependencies {
					predecessor, err := dumpPredecessor(s.catalogView, dependency.Ref.Address, d.Address())
					if err != nil {
						return err
					}
					event := dump.Event{OID: id}
					dependencyEvent := dump.Event{OID: predecessor}
					if predecessor != 0 && !slices.Contains(s.graph[event], dependencyEvent) {
						s.graph[event] = append(s.graph[event], dependencyEvent)
					}
				}
			}
		}
	}
	if err := operatorDumpDependencies(dumpResolver{}, s.catalogView, s.operators, s.graph); err != nil {
		return err
	}
	// Reconstruct final links, not allocation history: asymmetric links remain
	// in CREATE, reciprocal defined links finish in endpoint-local ALTER events.
	operatorDumpEvents(s.catalogView, s.operators, s.graph)
	if err := sqlRoutineDumpEvents(dumpResolver{}, s.catalogView, s.sqlRoutines, s.graph); err != nil {
		return err
	}
	for event, predecessors := range s.graph {
		id := event.OID
		relation, ok := s.catalogView.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id})
		if !ok {
			continue
		}
		columns, err := dumpColumnPredecessors(s.catalogView, relation, dump.DeclarationIDs(predecessors))
		if err != nil {
			return err
		}
		s.graph[event] = dump.DeclarationEvents(columns)
		if inline, ok := s.inlinePredecessors[id]; ok {
			inline, err = dumpColumnPredecessors(s.catalogView, relation, inline)
			if err != nil {
				return err
			}
			s.inlinePredecessors[id] = inline
		}
	}
	return nil
}

// renderTypeStatements renders the collation and conversion declarations and
// the seeded ones a changed catalog must recreate.
func (s *dumpScript) renderTypeStatements(ctx context.Context) error {
	var err error
	if s.collationStatements, err = typedef.CollationDumpStatements(ctx, s.catalogView, s.domainRoles, s.collations); err != nil {
		return err
	}
	if s.seedStatements, err = typedef.CollationSeedDumpStatements(ctx, s.catalogView, s.domainRoles); err != nil {
		return err
	}
	if s.conversionStatements, s.conversionSchemas, err = typedef.ConversionDumpStatements(ctx, s.catalogView, s.domainRoles, s.conversions); err != nil {
		return err
	}
	s.conversionSeeds, err = typedef.ConversionSeedDumpStatements(ctx, s.catalogView, s.domainRoles)
	return err
}

// order sorts the declarations, decides default deferral, adds the later
// phases and sorts every event. It returns the declaration order.
func (s *dumpScript) order(ctx context.Context) ([]ids.OID, error) {
	if s.deferDefaults {
		for oid, predecessors := range s.inlinePredecessors {
			s.graph[dump.Event{OID: oid}] = dump.DeclarationEvents(predecessors)
		}
	}
	// Capture collision-remap provenance from the same immutable snapshot as
	// the graph, before any dump-private naming substitutions.
	var priorities map[ids.OID]ids.OID
	for address, priority := range s.originalCatalog.State().DumpPriorities() {
		if _, emitted := s.graph[dump.Event{OID: address.ObjectID}]; emitted {
			if priorities == nil {
				priorities = make(map[ids.OID]ids.OID)
			}
			priorities[address.ObjectID] = priority
		}
	}
	if err := physicalNotNullDumpDependencies(s.originalCatalog, s.graph); err != nil {
		return nil, err
	}
	normalizeOperatorDumpPredecessors(s.catalogView, s.graph)
	declarationOrder, err := dump.Order(ctx, s.graph, priorities)
	if err != nil && !s.deferDefaults && ctx.Err() == nil && len(s.inlinePredecessors) != 0 {
		for oid, predecessors := range s.inlinePredecessors {
			s.graph[dump.Event{OID: oid}] = dump.DeclarationEvents(predecessors)
		}
		if err := physicalNotNullDumpDependencies(s.originalCatalog, s.graph); err != nil {
			return nil, err
		}
		normalizeOperatorDumpPredecessors(s.catalogView, s.graph)
		declarationOrder, err = dump.Order(ctx, s.graph, priorities)
		s.deferDefaults = err == nil
	}
	if err != nil {
		return nil, err
	}
	for i := range s.tables {
		if err := s.tables[i].prepareInheritedRecipes(s.originalCatalog); err != nil {
			return nil, err
		}
	}
	viewOrder := dump.DeclarationIDs(declarationOrder)
	if err := partitionDumpEvents(ctx, s.originalCatalog, s.graph, dump.DeclarationEvents(viewOrder), s.tables, s.indexIndices, s.constraintReplay, s.deferDefaults); err != nil {
		return nil, err
	}
	s.invalidReplay = partitionInvalidDumpEvents(s.originalCatalog, s.graph, s.indexIndices)
	if err := domainInvalidDumpEvents(ctx, s.catalogView, s.graph, s.domainNodes); err != nil {
		return nil, err
	}
	normalizeOperatorDumpPredecessors(s.catalogView, s.graph)
	if s.eventOrder, err = dump.Order(ctx, s.graph, priorities); err != nil {
		return nil, err
	}
	return viewOrder, nil
}

// renderDeclarations renders the domain, composite, SQL routine and operator
// declarations and the operators' deferred link statements.
func (s *dumpScript) renderDeclarations(ctx context.Context, viewOrder []ids.OID) error {
	var err error
	if s.declarations, err = typedef.MixedDumpStatements(ctx, dumpPresenter{}, s.catalogView, s.domainRoles, viewOrder, s.domainNodes, s.composites); err != nil {
		return err
	}
	for _, d := range s.sqlRoutines {
		declaration, err := sqlRoutineDumpStatement(dumpPresenter{}, s.catalogView, s.domainRoles, d)
		if err != nil {
			return err
		}
		s.declarations[d.Address().ObjectID] = declaration
	}
	s.operatorLinks, err = operatorDumpStatements(ctx, dumpPresenter{}, s.catalogView, s.domainRoles, s.operators, s.declarations)
	return err
}
