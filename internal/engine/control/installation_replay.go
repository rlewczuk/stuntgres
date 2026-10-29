package control

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

type replayInstallation struct {
	recipe         *catalog.Installation
	ext            *catalog.Extension
	schema         string
	retainedSchema bool // Existing creation coordinates are neither created nor retired.
}

func (s *dumpScript) installations() ([]replayInstallation, error) {
	cohorts := map[ids.ObjectAddress]replayInstallation{}
	for _, d := range s.CatalogView.Descriptors() {
		if ext, ok := d.(*catalog.Extension); ok {
			if !ext.Installation.Valid() {
				return nil, extension.InvalidProviderDescriptor()
			}
			cohorts[ext.Address()] = replayInstallation{recipe: ext.Installation, ext: ext}
		}
	}
	for _, d := range s.CatalogView.Descriptors() {
		if temporaryDumpObject(s.CatalogView.State(), d) {
			continue
		}
		origin := catalog.DescriptorOrigin(d)
		if origin == nil {
			continue
		}
		if !origin.Valid(d.Address()) {
			return nil, extension.InvalidProviderDescriptor()
		}
		cohort := origin.Installation.Cohort
		previous, ok := cohorts[cohort]
		if !ok || previous.ext == nil && len(origin.Installation.Members) > len(previous.recipe.Members) {
			cohorts[cohort] = replayInstallation{recipe: origin.Installation}
		}
	}
	pending := make([]replayInstallation, 0, len(cohorts))
	for _, installation := range cohorts {
		pending = append(pending, installation)
	}
	slices.SortFunc(pending, func(a, b replayInstallation) int {
		// Global languages still retire through a genuine prerequisite-free
		// receiver when their origin header survives.
		if (a.recipe.Provider == "uuid-ossp") != (b.recipe.Provider == "uuid-ossp") {
			if a.recipe.Provider == "uuid-ossp" {
				return 1
			}
			return -1
		}
		if n := strings.Compare(a.recipe.Provider, b.recipe.Provider); n != 0 {
			return n
		}
		if (a.ext == nil) != (b.ext == nil) {
			if a.ext == nil {
				return -1
			}
			return 1
		}
		if a.recipe.Cohort.ObjectID < b.recipe.Cohort.ObjectID {
			return -1
		}
		if a.recipe.Cohort.ObjectID > b.recipe.Cohort.ObjectID {
			return 1
		}
		return 0
	})
	return pending, nil
}

// canonicalInstallationView is a rendering-only projection of the script-sealed
// declaration facts. It neither installs providers nor publishes descriptors.
func (s *dumpScript) canonicalInstallationView(item replayInstallation) (catalog.Read, error) {
	if len(item.recipe.Declarations) != len(item.recipe.Members) {
		return catalog.Read{}, fmt.Errorf("stuntgres: incomplete installation replay recipe")
	}
	state := catalog.CloneState(s.CatalogView.State())
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ^ids.OID(0)}
	for state.Get(namespace) != nil {
		namespace.ObjectID--
	}
	state.SetDescriptor(namespace, &catalog.Namespace{ObjectAddress: namespace, ObjectName: item.schema, ObjectRevision: 1})
	for i, d := range item.recipe.Declarations {
		if d == nil || d.Address() != item.recipe.Members[i] {
			return catalog.Read{}, extension.InvalidProviderDescriptor()
		}
		if ns := d.Namespace(); ns != (ids.ObjectAddress{}) {
			d = extension.RemapDescriptor(d, map[ids.ObjectAddress]ids.ObjectAddress{ns: namespace})
		}
		if typ, ok := d.(*catalog.Type); ok {
			n := *typ
			n.ObjectNamespace = namespace
			d = &n
		}
		state.SetDescriptor(d.Address(), d)
	}
	return s.CatalogView.WithState(state), nil
}

func replayAlter(sql string) string { return "ALTER " + sql }
func replayDrop(sql string) string  { return "DROP " + sql + " CASCADE;\n" }

func (s *dumpScript) prepareInstallationReplay(ctx context.Context) error {
	items, err := s.installations()
	if err != nil {
		return err
	}
	s.extensionMetadata = make(map[ids.OID]string)
	s.installationTypeReplay = newInstallationTypeReplay(s.CatalogView, ctx)
	installedProviders := map[string]bool{"plpgsql": true}
	seededPresent := false
	for _, item := range items {
		if item.recipe.Provider == "plpgsql" {
			seededPresent = true
			break
		}
	}
	if !seededPresent && s.CatalogView.Owner().Extensions["plpgsql"] == extension.StockPLpgSQL {
		s.installationStatements = append(s.installationStatements, "DROP EXTENSION plpgsql;\n")
		installedProviders["plpgsql"] = false
	}
	var authority []string
	schedule, err := s.newInstallationSchedule(items)
	if err != nil {
		return err
	}
	dictionaries, err := s.dictionaryProviderReplays()
	if err != nil {
		return err
	}
	templates, err := s.templateProviderReplays()
	if err != nil {
		return err
	}
	var deferred []installationPlacement
	reservedNames := map[string]bool{}
	for _, d := range s.CatalogView.Descriptors() {
		reservedNames[d.Name()] = true
	}
	for _, item := range items {
		for _, d := range item.recipe.Declarations {
			reservedNames[d.Name()] = true
		}
	}
	var process func(int) error
	process = func(index int) error {
		if schedule.completed[index] {
			return nil
		}
		if err := schedule.install(index); err != nil {
			return err
		}
		// Dependents must run their genuine scripts while these prerequisite
		// declarations still have canonical names; finalize them before retirement.
		for _, dependent := range schedule.dependents[index] {
			if err := process(dependent); err != nil {
				return err
			}
		}
		item := items[index]
		item.schema = schedule.currentSchema[index]
		recipe := item.recipe
		seeded := recipe.Provider == "plpgsql"
		if seeded && s.unchangedSeedInstallation(item) {
			s.extensionMetadata[item.ext.Address().ObjectID] = ""
			if s.preservedSeedMembership == nil {
				s.preservedSeedMembership = make(map[ids.ObjectAddress]ids.ObjectAddress)
			}
			for _, address := range item.ext.Members {
				s.preservedSeedMembership[address] = item.ext.Address()
			}
			schedule.finish(index)
			return nil
		}
		baseline, err := s.canonicalInstallationView(item)
		if err != nil {
			return err
		}
		extName := catalog.QuoteIdentifier(recipe.Provider)
		detached := map[ids.ObjectAddress]bool{}
		receiverCleanup := false
		standaloneMissing := item.ext != nil
		// Detach real baseline declarations before identity replay. A type root's
		// SQL command also detaches its explicit array member recursively.
		for _, d := range recipe.Declarations {
			if detached[d.Address()] {
				continue
			}
			if !standaloneMissing && s.CatalogView.Get(d.Address()) == nil {
				continue
			}
			if _, ok := d.(*catalog.Language); ok && s.CatalogView.Get(d.Address()) == nil {
				receiverCleanup = true
			}
			sql, err := extension.MemberSQL(baseline, baseline.Get(d.Address()))
			if err != nil {
				return err
			}
			s.installationStatements = append(s.installationStatements, "ALTER EXTENSION "+extName+" DROP "+sql+";\n")
			detached[d.Address()] = true
			if typ, ok := d.(*catalog.Type); ok && typ.Array != 0 {
				detached[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.Array)}] = true
			}
		}
		deferPlacement := false
		if item.ext == nil && recipe.Implementation == extension.StockCube {
			for _, dependent := range schedule.dependents[index] {
				if schedule.cubeDependency[dependent] == index {
					deferPlacement = true
					break
				}
			}
		}
		var procedureNames map[ids.ObjectAddress]string
		if deferPlacement {
			procedureNames, err = s.parkCubeNames(item, baseline, reservedNames)
			if err != nil {
				return err
			}
		}
		// Types move first; their implicit arrays follow the ordinary type owner.
		// Re-render routine signatures after these actual type identity changes.
		typeNames := map[ids.ObjectAddress]string{}
		reserved := reservedNames
		serial := 0
		for _, d := range recipe.Declarations {
			typ, ok := d.(*catalog.Type)
			if !ok || typ.SQLType.IsArray() || s.CatalogView.Get(d.Address()) == nil {
				continue
			}
			name := ""
			for {
				serial++
				name = "__stuntgres_type_" + strconv.Itoa(serial)
				if !reserved[name] && !reserved["_"+name] {
					break
				}
			}
			reserved[name], reserved["_"+name] = true, true
			typeNames[d.Address()] = name
			s.installationStatements = append(s.installationStatements, "ALTER TYPE "+catalog.QuoteIdentifier(item.schema)+"."+catalog.QuoteIdentifier(d.Name())+" RENAME TO "+catalog.QuoteIdentifier(name)+";\n")
		}
		// A retired prerequisite releases canonical keys now. Its one final
		// descriptor placement runs only after all genuine producers finish.
		headerRetired := false
		if deferPlacement {
			s.installationStatements = append(s.installationStatements, "DROP EXTENSION "+extName+";\n")
			installedProviders[recipe.Provider] = false
			schedule.completed[index] = true
			delete(schedule.active, recipe.Provider)
			headerRetired = true
		}
		place := installationPlacement{
			index: index, item: item, baseline: baseline,
			typeNames: typeNames, procedureNames: procedureNames,
			headerRetired: headerRetired, standaloneMissing: standaloneMissing, receiverCleanup: receiverCleanup,
		}
		if deferPlacement {
			deferred = append(deferred, place)
			return nil
		}
		return s.placeInstallation(schedule, place, &dictionaries, &templates, installedProviders, &authority)
	}
	for i := range items {
		if err := process(i); err != nil {
			return err
		}
	}
	for _, place := range deferred {
		if err := s.placeInstallation(schedule, place, &dictionaries, &templates, installedProviders, &authority); err != nil {
			return err
		}
	}
	templateStatements, err := planProviderReplay(templates)
	if err != nil {
		return err
	}
	s.installationStatements = append(s.installationStatements, templateStatements...)
	statements, err := planProviderReplay(dictionaries)
	if err != nil {
		return err
	}
	s.installationStatements = append(s.installationStatements, statements...)
	schedule.retireTemporary()
	schedule.retireSchemas()
	s.installationStatements = append(s.installationStatements, authority...)
	s.membershipStatements, err = s.finalMembershipReplay()
	return err
}

func emitDumpInstallationCompletion(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	if len(s.installationMetadata) == 0 {
		return nil
	}
	if err := w.setOwner("postgres"); err != nil {
		return err
	}
	if err := emitDumpLines(w, s.membershipStatements); err != nil {
		return err
	}
	if err := emitDumpLines(w, s.installationMetadata); err != nil {
		return err
	}
	return ctx.Err()
}

// An unchanged bootstrap installation already exists in the restore stage.
// Omit only that exact current state, not a provider-name approximation: any
// membership, identity, planner metadata or owner/ACL delta takes real replay.
func (s *dumpScript) unchangedSeedInstallation(item replayInstallation) bool {
	ext, recipe := item.ext, item.recipe
	bootstrap := s.DomainRoles.LookupRef("postgres")
	if ext == nil || recipe.Provider != "plpgsql" || ext.Name() != recipe.Provider || ext.Version != recipe.Version || ext.Owner != bootstrap || ext.TargetNamespace.ObjectID != ids.PGCatalogNamespaceOID || len(ext.Members) != len(recipe.Declarations) {
		return false
	}
	for _, original := range recipe.Declarations {
		current := s.CatalogView.Get(original.Address())
		if current == nil || current.Name() != original.Name() || current.Namespace() != original.Namespace() || catalog.ExtensionMembership(current) != ext.Address() || !slices.Contains(ext.Members, original.Address()) {
			return false
		}
		if procedure, ok := current.(*catalog.Procedure); ok {
			released, ok := original.(*catalog.Procedure)
			if !ok || procedure.Parallel != released.Parallel || procedure.Cost != released.Cost || procedure.Rows != released.Rows {
				return false
			}
		}
		if language, ok := current.(*catalog.Language); ok {
			released, ok := original.(*catalog.Language)
			if !ok || language.Procedural != released.Procedural || language.Trusted != released.Trusted || language.Call != released.Call || language.Inline != released.Inline || language.Validator != released.Validator {
				return false
			}
		}
		owner, acl, _ := access.DescriptorACL(current)
		// Historical declaration owners are rendering facts in their original
		// role domain, not current authority. The actual bootstrap baseline is
		// the live captured role and absent raw ACL, even after role remapping.
		if owner != bootstrap || acl.Explicit || len(acl.Entries) != 0 {
			return false
		}
	}
	return true
}
