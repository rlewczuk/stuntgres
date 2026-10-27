package control

import (
	"context"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
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
	rawFloat4                   bool
	rawHelper, rawFinalDeferred bool
	castWindows                 map[castConsumer][]castReplay
	comparisonBindings          map[castConsumer]*dump.DefinitionBinding
	deferredDomainChecks        map[ids.OID]map[string]bool
	extensionTypeAfter          ids.OID
	extensionTypeSQL            string
	providerEventStatements     map[ids.OID][]string

	policyGroups, ruleGroups, securityGroups  []dump.Group
	triggerGroups                             []dump.Group
	eventTriggerReplay                        []eventTriggerReplay
	domainNodes                               map[ids.OID]*catalog.Type
	collationStatements, conversionStatements []dump.Statement
	seedStatements, conversionSeeds           []string
	familySeeds                               []string
	familySeedFinal                           map[ids.OID]dump.Statement
	conversionSchemas                         []string
	sequenceCreation, sequenceCompletion      []string
	invalidReplay                             map[ids.OID]partition.InvalidReplay
	eventOrder                                []dump.Event
	declarations                              map[ids.OID]dump.Statement
	operatorLinks                             map[ids.OID]string
	largeObjectRecords                        []largeObjectDump
	foreignDefaultStatements                  []foreignDefaultStatement
	lateForeignCasts                          map[ids.OID]bool
	providerStatements                        []string
	extensionMetadata                         map[ids.OID]string
	objectACLStatements                       []string
	initialACLStatements                      []string
	typeACLStatements                         map[ids.OID][]string
	attributeHistory                          *attributeHistory
	attributeSchedules                        map[ids.ObjectAddress]*attributeSchedule
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
	if s.triggerGroups, err = stored.TriggerDumpGroups(ctx, s.OriginalCatalog, s.OriginalRoles); err != nil {
		return nil, err
	}
	if s.securityGroups, err = stored.RowSecurityDumpGroups(ctx, s.CatalogView, s.DomainRoles); err != nil {
		return nil, err
	}
	if err := s.separateCastConsumers(); err != nil {
		return nil, err
	}
	if err := s.separateForeignCastConsumers(ctx); err != nil {
		return nil, err
	}
	if err := s.addDeclarationEdges(); err != nil {
		return nil, err
	}
	if err := s.renderTypeStatements(ctx); err != nil {
		return nil, err
	}
	if err := s.prepareAttributeHistory(); err != nil {
		return nil, err
	}
	if err := s.prepareAttributeSchedules(ctx); err != nil {
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
	if s.largeObjectRecords, err = prepareLargeObjectDump(ctx, s.Capture); err != nil {
		return nil, err
	}
	if err := s.renderForeignDefaults(ctx); err != nil {
		return nil, err
	}
	if err := s.prepareProviderReplay(); err != nil {
		return nil, err
	}
	if err = prepareObjectACLs(ctx, s); err != nil {
		return nil, err
	}
	if err := s.prepareEventTriggers(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Foreign defaults have no rows to evaluate before replay. Their final names
// and every referenced provider exist after the ordinary event/name phases.
// Render now so even a late default failure precedes the first output byte.
func (s *dumpScript) renderForeignDefaults(ctx context.Context) error {
	if len(s.ForeignDefaults) == 0 {
		return nil
	}
	for _, event := range s.eventOrder {
		selected := s.ForeignDefaults[event.OID]
		if event.Phase != dump.Declaration || len(selected) == 0 {
			continue
		}
		r := s.OriginalCatalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: event.OID})
		namespace := s.OriginalCatalog.Get(r.Namespace())
		qualified := catalog.QuoteIdentifier(namespace.Name()) + "." + catalog.QuoteIdentifier(r.Name())
		for i, column := range r.Columns {
			attribute := r.Attributes[i]
			if !selected[attribute] {
				continue
			}
			text, err := stored.DefaultSQL(ctx, s.OriginalCatalog, r.ColumnMeta.Defaults[attribute].Expression)
			if err != nil {
				return err
			}
			s.foreignDefaultStatements = append(s.foreignDefaultStatements, foreignDefaultStatement{
				Site: castConsumer{Event: dump.Event{OID: event.OID}, Attribute: attribute, ForeignDefault: true},
				SQL:  "ALTER FOREIGN TABLE " + qualified + " ALTER COLUMN " + catalog.QuoteIdentifier(column.Name) + " SET DEFAULT " + text + ";\n",
			})
		}
	}
	return nil
}

// addDeclarationEdges adds the domain, operator and SQL routine declarations
// to the graph and rewrites relation predecessors to their column sources.
func (s *dumpScript) addDeclarationEdges() error {
	s.domainNodes = make(map[ids.OID]*catalog.Type, len(s.Domains))
	for _, method := range s.AccessMethods {
		event := dump.Event{OID: method.Address().ObjectID}
		s.Graph[event] = nil
		for _, dependency := range catalog.AccessMethodDependencies(method) {
			id, err := (Resolver{}).Predecessor(s.CatalogView, dependency.Address, method.Address())
			if err != nil {
				return err
			}
			if id != 0 {
				s.Graph[event] = append(s.Graph[event], dump.Event{OID: id})
			}
		}
	}
	for _, d := range s.Domains {
		id := d.Address().ObjectID
		s.domainNodes[id] = d
		s.Graph[dump.Event{OID: id}] = nil
		for _, dependency := range catalog.RangeDependencies(d) {
			predecessor, err := (Resolver{}).Predecessor(s.CatalogView, dependency, d.Address())
			if err != nil {
				return err
			}
			if predecessor != 0 {
				s.Graph[dump.Event{OID: id}] = append(s.Graph[dump.Event{OID: id}], dump.Event{OID: predecessor})
			}
		}
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
				if check.NotValid || s.deferredDomainChecks[id][check.Name] {
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
	if err := index.OperatorFamilyDumpEvents(Resolver{}, s.CatalogView, s.OperatorFamilies, s.Graph); err != nil {
		return err
	}
	if err := routine.OperatorDumpDependencies(Resolver{}, s.CatalogView, s.Operators, s.Graph); err != nil {
		return err
	}
	// Reconstruct final links, not allocation history: asymmetric links remain
	// in CREATE, reciprocal defined links finish in endpoint-local ALTER events.
	routine.OperatorDumpEvents(s.CatalogView, s.Operators, s.Graph)
	if err := routine.RoutineDumpEvents(Resolver{}, s.CatalogView, s.OwnedRoutines, s.Graph); err != nil {
		return err
	}
	if err := s.castEvents(); err != nil {
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
	if s.conversionSeeds, err = typedef.ConversionSeedDumpStatements(ctx, s.CatalogView, s.DomainRoles); err != nil {
		return err
	}
	s.familySeeds, s.familySeedFinal, err = index.OperatorFamilySeedDumpStatements(ctx, s.CatalogView, s.DomainRoles, s.OperatorFamilies)
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
	if err := s.orderExtensionTypes(ctx); err != nil {
		return nil, err
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
	s.installAttributeScheduleGraph()
	declarationOrder, err = dump.Order(ctx, s.Graph, priorities)
	if err != nil {
		return nil, err
	}
	viewOrder := dump.DeclarationIDs(declarationOrder)
	// Cast rows are ordered by their real consumers, not the generic early
	// declaration chain. A historical recipe used by a deferred DEFAULT or
	// NOT VALID CHECK must finish before its conflicting final row exists.
	phaseDeclarations := make([]dump.Event, 0, len(viewOrder))
	for _, oid := range viewOrder {
		if _, statistics := s.CatalogView.Statistics(ids.ObjectAddress{ClassID: ids.StatisticExtClassID, ObjectID: oid}); statistics {
			continue
		}
		if _, cast := s.CatalogView.Descriptor(ids.ObjectAddress{ClassID: ids.CastClassID, ObjectID: oid}); !cast {
			phaseDeclarations = append(phaseDeclarations, dump.Event{OID: oid})
		}
	}
	if err := partition.DumpEvents(ctx, Resolver{}, s.OriginalCatalog, s.Graph, phaseDeclarations, s.Tables, s.IndexIndices, s.ConstraintReplay, s.DeferDefaults); err != nil {
		return nil, err
	}
	s.addAttributeRetirementEvents()
	if err := s.addSeparatedCheckEvents(); err != nil {
		return nil, err
	}
	s.invalidReplay = partition.InvalidDumpEvents(s.OriginalCatalog, s.Graph, s.IndexIndices)
	if err := regobj.DomainInvalidDumpEvents(ctx, Resolver{}, s.CatalogView, s.Graph, s.domainNodes); err != nil {
		return nil, err
	}
	if err := s.addSeparatedDomainCheckEvents(); err != nil {
		return nil, err
	}
	s.finishAttributeScheduleGraph()
	s.finishStatisticsGraph()
	// Deferral, inherited recipes and invalid-check phases are now final.
	// Attach windows to the events that actually emit each expression.
	if err := s.prepareCastWindows(); err != nil {
		return nil, err
	}
	if err := s.prepareComparisonBindings(ctx); err != nil {
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
	// Omit only checks with their own later real ALTER declaration. The
	// catalog and retained runtime domain metadata remain untouched.
	domains := make(map[ids.OID]*catalog.Type, len(s.domainNodes))
	for oid, domain := range s.domainNodes {
		domains[oid] = domain
		if len(s.deferredDomainChecks[oid]) == 0 {
			continue
		}
		copy := *domain
		copy.DomainChecks = nil
		for _, check := range domain.DomainChecks {
			if !s.deferredDomainChecks[oid][check.Name] {
				copy.DomainChecks = append(copy.DomainChecks, check)
			}
		}
		domains[oid] = &copy
	}
	replay, err := s.renderExtensionTypes(ctx, viewOrder)
	if err != nil {
		return err
	}
	if s.declarations, err = typedef.MixedDumpStatements(ctx, Presenter{}, s.CatalogView, s.DomainRoles, viewOrder, domains, s.Composites, replay); err != nil {
		return err
	}
	for oid, statement := range s.ForeignDeclarations {
		s.declarations[oid] = statement
	}
	for oid, statement := range s.familySeedFinal {
		s.declarations[oid] = statement
	}
	for _, dictionary := range s.Dictionaries {
		declaration, err := extension.DictionaryDumpStatement(s.CatalogView, s.DomainRoles, dictionary)
		if err != nil {
			return err
		}
		s.declarations[dictionary.Address().ObjectID] = declaration
	}
	for _, statistics := range s.Statistics {
		declaration, err := statisticsDumpStatement(s.CatalogView, s.DomainRoles, statistics)
		if err != nil {
			return err
		}
		s.declarations[statistics.Address().ObjectID] = declaration
	}
	for _, config := range s.Configurations {
		declaration, err := configurationDumpStatement(s.CatalogView, s.DomainRoles, config)
		if err != nil {
			return err
		}
		s.declarations[config.Address().ObjectID] = declaration
	}
	for _, d := range s.OwnedRoutines {
		declaration, err := routine.RoutineDumpStatement(Presenter{}, s.CatalogView, s.DomainRoles, d)
		if err != nil {
			return err
		}
		s.declarations[d.Address().ObjectID] = declaration
	}
	for _, method := range s.AccessMethods {
		text, err := index.AccessMethodSQL(s.CatalogView, method)
		if err != nil {
			return err
		}
		s.declarations[method.Address().ObjectID] = dump.Statement{Owner: "postgres", SQL: text + "\n"}
	}
	if err := s.castDeclarations(); err != nil {
		return err
	}
	if err := index.OperatorFamilyDumpStatements(ctx, Presenter{}, s.CatalogView, s.DomainRoles, s.OperatorFamilies, s.declarations); err != nil {
		return err
	}
	s.operatorLinks, err = routine.OperatorDumpStatements(ctx, Presenter{}, s.CatalogView, s.DomainRoles, s.Operators, s.declarations)
	return err
}
