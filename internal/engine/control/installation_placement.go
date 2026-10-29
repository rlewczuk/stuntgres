package control

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
)

// installationPlacement retains rendering facts after a real absent cube
// header is retired. It is neither a catalog nor a second declaration authority.
type installationPlacement struct {
	index                                             int
	item                                              replayInstallation
	baseline                                          catalog.Read
	typeNames, procedureNames                         map[ids.ObjectAddress]string
	headerRetired, standaloneMissing, receiverCleanup bool
}

func (s *dumpScript) placeInstallation(schedule *installationSchedule, placement installationPlacement, dictionaries, templates *[]providerReplay, installedProviders map[string]bool, authority *[]string) error {
	item, baseline := placement.item, placement.baseline
	recipe := item.recipe
	extName := catalog.QuoteIdentifier(recipe.Provider)
	for _, d := range recipe.Declarations {
		typ, ok := d.(*catalog.Type)
		if !ok {
			continue
		}
		current, ok := s.CatalogView.Type(d.Address())
		if !ok || typ.SQLType.IsArray() {
			continue
		}
		if err := s.replayInstalledType(schedule, item, current, placement.typeNames[d.Address()]); err != nil {
			return err
		}
	}
	// Type arguments now use final SQL identities, while object labels still
	// have their actual canonical or parked names in the recorded namespace.
	arguments := catalog.CloneState(baseline.State())
	for _, d := range recipe.Declarations {
		if _, ok := d.(*catalog.Type); ok {
			if current := s.CatalogView.Get(d.Address()); current != nil {
				arguments.SetDescriptor(d.Address(), current)
			}
		}
	}
	argumentView := s.CatalogView.WithState(arguments)
	groups := map[string][]providerReplay{}
	for _, d := range recipe.Declarations {
		if _, ok := d.(*catalog.Type); ok {
			continue
		}
		current := s.CatalogView.Get(d.Address())
		// Missing declarations still attached to a retiring origin need no
		// identity replay: DROP EXTENSION removes that genuine residual graph.
		// Its generated argument types may lawfully be absent from the source.
		if current == nil && (placement.headerRetired || !placement.standaloneMissing) {
			continue
		}
		selectedView := s.CatalogView
		selected := current
		if selected == nil {
			selectedView = argumentView
			selected = argumentView.Get(d.Address())
		}
		identity, err := extension.OwnerIdentity(selectedView, selected)
		if err != nil {
			return err
		}
		kind := ""
		family := routineProvider
		signature := catalog.ProcedureSignature("")
		switch selected := selected.(type) {
		case *catalog.Procedure:
			kind = "FUNCTION"
			signature = selected.Signature
			if selected.RoutineKind == catalog.ProcedureRoutine {
				kind = "PROCEDURE"
			} else if selected.RoutineKind == catalog.AggregateRoutine {
				kind = "AGGREGATE"
			}
		case *catalog.TSDictionary:
			kind = "dictionary"
			family = dictionaryProvider
		case *catalog.TSTemplate:
			kind = "template"
			family = templateProvider
		case *catalog.OperatorClass:
			kind = "OPERATOR CLASS"
			signature = catalog.ProcedureSignature(identity.Signature)
		case *catalog.OperatorFamily:
			kind = "OPERATOR FAMILY"
			signature = catalog.ProcedureSignature(identity.Signature)
		}
		if kind != "" {
			originalName := d.Name()
			if parked := placement.procedureNames[d.Address()]; parked != "" {
				originalName = parked
			}
			replay := providerReplay{original: providerKey{item.schema, originalName, signature}, current: providerKey{identity.Schema, identity.Name, signature}, arguments: identity.Signature, family: family, objectKind: kind}
			if procedure, ok := current.(*catalog.Procedure); ok {
				replay.parallel, replay.cost, replay.rows = procedure.Parallel, procedure.Cost, procedure.Rows
				if extension.StockInsertUsername.SupportsDMLTrigger(procedure) {
					replay.securityDefiner, replay.configuration = procedure.SecurityDefiner, procedure.Configuration
				}
			}
			groups[kind] = append(groups[kind], replay)
			continue
		}
		if current == nil {
			continue
		}
		sql, err := extension.MemberSQL(argumentView, argumentView.Get(d.Address()))
		if err != nil {
			return err
		}
		if _, ok := current.(*catalog.Language); ok {
			if name := s.languageReplayNames[d.Address()]; name != "" {
				s.installationStatements = append(s.installationStatements, replayAlter(sql)+" RENAME TO "+catalog.QuoteIdentifier(name)+";\n")
			}
		}
		if d.Namespace() != (ids.ObjectAddress{}) {
			ns := s.CatalogView.MustNamespace(current.Namespace()).Name()
			if d.Name() != current.Name() {
				return fmt.Errorf("stuntgres: declaration has no represented rename owner")
			}
			if ns != item.schema {
				s.installationStatements = append(s.installationStatements, replayAlter(sql)+" SET SCHEMA "+catalog.QuoteIdentifier(ns)+";\n")
			}
		}
	}
	if len(groups["dictionary"]) != 0 && len(*dictionaries) != 0 {
		// Initialized and installed dictionaries share the real collision keys.
		groups["dictionary"] = append(groups["dictionary"], (*dictionaries)...)
		*dictionaries = nil
	}
	if len(groups["template"]) != 0 && len(*templates) != 0 {
		groups["template"] = append(groups["template"], (*templates)...)
		*templates = nil
	}
	var kinds []string
	for kind := range groups {
		kinds = append(kinds, kind)
	}
	slices.SortFunc(kinds, func(a, b string) int {
		// Template identities precede dictionary placement and later consumers.
		if (a == "template") != (b == "template") {
			if a == "template" {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	})
	for _, kind := range kinds {
		statements, err := planProviderReplay(groups[kind])
		if err != nil {
			return err
		}
		s.installationStatements = append(s.installationStatements, statements...)
	}
	// Reconcile a surviving installed language before retiring any handler
	// referenced by its destination seed routes. Otherwise cascading retirement
	// could destroy its genuine installed identity before final name replay.
	for _, declaration := range recipe.Declarations {
		released, ok := declaration.(*catalog.Language)
		if !ok {
			continue
		}
		current, ok := s.CatalogView.Get(released.Address()).(*catalog.Language)
		if !ok {
			continue
		}
		missingHandler := false
		for _, route := range [...]ids.ObjectAddress{released.Call, released.Inline, released.Validator} {
			if route != (ids.ObjectAddress{}) && s.CatalogView.Get(route) == nil {
				missingHandler = true
				break
			}
		}
		if !missingHandler {
			continue
		}
		language := *current
		if parked := s.languageReplayNames[current.Address()]; parked != "" {
			language.ObjectName = parked
		}
		statement, err := routine.LanguageDumpStatement(s.CatalogView, &language, true)
		if err != nil {
			return err
		}
		s.installationStatements = append(s.installationStatements, statement)
	}
	retiredMissing := false
	if placement.receiverCleanup && item.ext != nil {
		// Classes without standalone DDL retire through a real extension owner.
		var choices []string
		for name, implementation := range s.CatalogView.Owner().Extensions {
			if name != recipe.Provider && !installedProviders[name] && len(implementation.Control().Requires) == 0 {
				if _, ok := implementation.(extension.Installer); ok {
					choices = append(choices, name)
				}
			}
		}
		slices.Sort(choices)
		if len(choices) == 0 {
			return fmt.Errorf("stuntgres: no registered cleanup installation available")
		}
		cleanup := choices[len(choices)-1]
		cleanupSchema := schedule.schema("__stuntgres_retire")
		s.installationStatements = append(s.installationStatements, "CREATE SCHEMA "+catalog.QuoteIdentifier(cleanupSchema)+";\n", "CREATE EXTENSION "+catalog.QuoteIdentifier(cleanup)+" WITH SCHEMA "+catalog.QuoteIdentifier(cleanupSchema)+";\n")
		assigned := map[ids.ObjectAddress]bool{}
		for _, d := range recipe.Declarations {
			if s.CatalogView.Get(d.Address()) != nil || assigned[d.Address()] {
				continue
			}
			sql, err := extension.MemberSQL(argumentView, argumentView.Get(d.Address()))
			if err != nil {
				return err
			}
			s.installationStatements = append(s.installationStatements, "ALTER EXTENSION "+catalog.QuoteIdentifier(cleanup)+" ADD "+sql+";\n")
			assigned[d.Address()] = true
			if typ, ok := d.(*catalog.Type); ok && typ.Array != 0 {
				assigned[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.Array)}] = true
			}
		}
		s.installationStatements = append(s.installationStatements, "DROP EXTENSION "+catalog.QuoteIdentifier(cleanup)+" CASCADE;\n", "DROP SCHEMA "+catalog.QuoteIdentifier(cleanupSchema)+";\n")
		retiredMissing = true
	}
	// The source already proved no surviving dependency on these deletions.
	for i := len(recipe.Declarations) - 1; i >= 0; i-- {
		d := recipe.Declarations[i]
		if !placement.standaloneMissing || retiredMissing || s.CatalogView.Get(d.Address()) != nil {
			continue
		}
		if typ, ok := d.(*catalog.Type); ok && typ.SQLType.IsArray() {
			continue
		}
		sql, err := extension.MemberSQL(argumentView, argumentView.Get(d.Address()))
		if err != nil {
			return err
		}
		s.installationStatements = append(s.installationStatements, replayDrop(sql))
	}
	if item.ext == nil && !placement.headerRetired {
		s.installationStatements = append(s.installationStatements, "DROP EXTENSION "+extName+";\n")
		installedProviders[recipe.Provider] = false
	} else if item.ext != nil {
		target := s.CatalogView.MustNamespace(item.ext.TargetNamespace).Name()
		if recipe.Provider != "plpgsql" && target != item.schema {
			s.installationStatements = append(s.installationStatements, "ALTER EXTENSION "+extName+" SET SCHEMA "+catalog.QuoteIdentifier(target)+";\n")
		}
		s.extensionMetadata[item.ext.Address().ObjectID] = ""
		installedProviders[recipe.Provider] = true
	}
	metadata, err := extension.InstallationOwnerMetadata(s.CatalogView, s.DomainRoles, recipe, item.ext)
	if err != nil {
		return err
	}
	s.installationMetadata = append(s.installationMetadata, metadata)
	if item.ext != nil {
		s.installationMetadata = append(s.installationMetadata, extensionCommentSQL(item.ext))
	}
	for _, d := range recipe.Declarations {
		current := s.CatalogView.Get(d.Address())
		if current == nil {
			continue
		}
		switch current := current.(type) {
		case *catalog.Language:
			continue
		case *catalog.Type:
			if current.SQLType.IsArray() {
				continue
			}
		}
		owner, _, _ := access.DescriptorACL(current)
		if owner.ID() == 0 {
			continue
		}
		role, err := s.DomainRoles.Descriptor(owner)
		if err != nil {
			return err
		}
		sql, err := extension.MemberSQL(s.CatalogView, current)
		if err != nil {
			return err
		}
		*authority = append(*authority, "ALTER "+sql+" OWNER TO "+catalog.QuoteIdentifier(role.Name)+";\n")
		if dictionary, ok := current.(*catalog.TSDictionary); ok && len(dictionary.Options) != 0 {
			*authority = append(*authority, "ALTER "+sql+" ("+catalog.DictionaryOptionsText(dictionary.Options)+");\n")
		}
	}
	schedule.finish(placement.index)
	return nil
}

// Every surviving header carries explicit state, including removal of a default.
func extensionCommentSQL(ext *catalog.Extension) string {
	literal := "NULL"
	if ext.Comment != "" {
		literal = dump.CommentLiteral(ext.Comment)
	}
	return "COMMENT ON EXTENSION " + catalog.QuoteIdentifier(ext.Name()) + " IS " + literal + ";\n"
}
