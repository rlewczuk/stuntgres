package control

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

// Prerequisites select cohorts through genuine source dependency identities.
// Names alone cannot bind detached old/new installations of the same provider.
func (s *dumpScript) installationDependencies(items []replayInstallation) ([][]int, [][]string, error) {
	dependencies := make([][]int, len(items))
	missing := make([][]string, len(items))
	cohorts := map[ids.ObjectAddress]int{}
	members := map[ids.ObjectAddress]int{}
	installed := map[string]int{}
	for i, item := range items {
		cohorts[item.recipe.Cohort] = i
		if item.ext != nil {
			installed[item.recipe.Provider] = i
		}
		for _, a := range item.recipe.Members {
			members[a] = i
		}
	}
	for i, item := range items {
		selected := map[string]int{}
		if item.ext != nil {
			required, err := catalog.ExtensionPrerequisites(s.CatalogView, item.ext)
			if err != nil {
				return nil, nil, err
			}
			for _, a := range required {
				index, ok := cohorts[a]
				if !ok {
					return nil, nil, fmt.Errorf("stuntgres: missing installation prerequisite cohort")
				}
				selected[items[index].recipe.Provider] = index
			}
		}
		selectType := func(a ids.ObjectAddress) {
			index, ok := members[a]
			if !ok {
				return
			}
			provider := items[index].recipe.Provider
			if provider != item.recipe.Provider {
				if _, exists := selected[provider]; !exists {
					selected[provider] = index
				}
			}
		}
		for _, d := range item.recipe.Declarations {
			switch d := d.(type) {
			case *catalog.Type:
				if d.SQLType.IsDomain() {
					selectType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.SQLType.DomainBase().Type.ID())})
				}
			case *catalog.Operator:
				selectType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: d.Left})
				selectType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: d.Right})
			}
		}
		for _, name := range item.recipe.Implementation.Control().Requires {
			index, ok := selected[name]
			if !ok {
				index, ok = installed[name]
			}
			if ok {
				dependencies[i] = append(dependencies[i], index)
			} else {
				missing[i] = append(missing[i], name)
			}
		}
	}
	return dependencies, missing, nil
}

type temporaryInstallation struct{ name, schema string }
type installationSchedule struct {
	script                         *dumpScript
	items                          []replayInstallation
	dependencies, dependents       [][]int
	missing                        [][]string
	installed, completed, visiting map[int]bool
	active                         map[string]int
	names                          map[string]bool
	temporary                      map[string]bool
	temporaries                    []temporaryInstallation
	currentSchema                  []string
	cubePlacement                  []string
	cubeDependency                 []int
	createdSchemas                 []string
	created                        map[string]bool
}

func (s *dumpScript) newInstallationSchedule(items []replayInstallation) (*installationSchedule, error) {
	dependencies, missing, err := s.installationDependencies(items)
	if err != nil {
		return nil, err
	}
	schedule := &installationSchedule{script: s, items: items, dependencies: dependencies, missing: missing, dependents: make([][]int, len(items)), installed: map[int]bool{}, completed: map[int]bool{}, visiting: map[int]bool{}, active: map[string]int{}, names: map[string]bool{}, temporary: map[string]bool{}}
	schedule.currentSchema = make([]string, len(items))
	schedule.cubePlacement = make([]string, len(items))
	schedule.cubeDependency = make([]int, len(items))
	schedule.created = map[string]bool{}
	for i := range items {
		schedule.cubeDependency[i] = -1
		if items[i].recipe.Implementation != extension.StockEarthdistance {
			continue
		}
		schema, cube, err := extension.EarthInstallationCubePlacement(items[i].recipe)
		if err != nil {
			return nil, err
		}
		schedule.names[schema] = true
		for _, dependency := range dependencies[i] {
			recipe := items[dependency].recipe
			if recipe.Implementation == extension.StockCube {
				for _, member := range recipe.Members {
					if member == cube {
						schedule.cubePlacement[i], schedule.cubeDependency[i] = schema, dependency
					}
				}
			}
		}
	}
	for i := range items {
		if items[i].recipe.Provider == "plpgsql" {
			items[i].schema = "pg_catalog"
		} else {
			// Public namespace movers cannot create objects in pg_catalog.
			// Reproduce genuine installation there instead; the initialized
			// namespace is neither created nor retired by this replay.
			if items[i].ext != nil && items[i].ext.TargetNamespace.ObjectID == ids.PGCatalogNamespaceOID {
				items[i].schema, items[i].retainedSchema = "pg_catalog", true
			}
			for _, address := range items[i].recipe.Members {
				if current := s.CatalogView.Get(address); current != nil && current.Namespace().ObjectID == ids.PGCatalogNamespaceOID {
					items[i].schema, items[i].retainedSchema = "pg_catalog", true
				}
			}
			for _, address := range items[i].recipe.Members {
				procedure, ok := s.CatalogView.Procedure(address)
				if !ok || procedure.Aggregate == nil || procedure.Aggregate.Helpers[0].ID == 0 {
					continue
				}
				helpers := procedure.Aggregate.Helpers
				if helpers[0].Namespace != helpers[1].Namespace {
					return nil, fmt.Errorf("stuntgres: aggregate replay helpers have different creation namespaces")
				}
				namespace, ok := s.CatalogView.Namespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: helpers[0].Namespace})
				if !ok || namespace.Temporary != (ids.SessionToken{}) {
					return nil, fmt.Errorf("stuntgres: aggregate replay helper namespace is not portable")
				}
				items[i].schema, items[i].retainedSchema = namespace.Name(), true
			}
			if !items[i].retainedSchema {
				items[i].schema = schedule.schema("__stuntgres_install_" + strconv.Itoa(i))
			}
		}
		for _, dependency := range dependencies[i] {
			schedule.dependents[dependency] = append(schedule.dependents[dependency], i)
		}
	}
	// Start a constrained prerequisite directly at its first real producer's
	// coordinates. pg_catalog must be first: ordinary SET SCHEMA cannot move in.
	for dependency := range items {
		dependents := schedule.dependents[dependency]
		for j, dependent := range dependents {
			if schedule.cubeDependency[dependent] == dependency && schedule.cubePlacement[dependent] == "pg_catalog" {
				dependents[0], dependents[j] = dependents[j], dependents[0]
				break
			}
		}
		for _, dependent := range dependents {
			if schedule.cubeDependency[dependent] != dependency {
				continue
			}
			schema := schedule.cubePlacement[dependent]
			items[dependency].schema = schema
			items[dependency].retainedSchema = s.CatalogView.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: schema}) != (ids.ObjectAddress{})
			break
		}
	}
	return schedule, nil
}
func (p *installationSchedule) schema(prefix string) string {
	name := prefix
	for serial := 0; p.names[name] || p.script.CatalogView.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name}) != (ids.ObjectAddress{}); serial++ {
		name = prefix + "_" + strconv.Itoa(serial)
	}
	p.names[name] = true
	return name
}
func (p *installationSchedule) install(index int) error {
	if p.installed[index] {
		return nil
	}
	if p.visiting[index] {
		return fmt.Errorf("stuntgres: cyclic installation recipe dependency")
	}
	p.visiting[index] = true
	item := p.items[index]
	for _, dependency := range p.dependencies[index] {
		if p.completed[dependency] {
			return fmt.Errorf("stuntgres: prerequisite identity finalized before script installation")
		}
		if err := p.install(dependency); err != nil {
			return err
		}
	}
	for _, name := range p.missing[index] {
		if err := p.installTemporary(name); err != nil {
			return err
		}
	}
	if previous, ok := p.active[item.recipe.Provider]; ok && previous != index {
		return fmt.Errorf("stuntgres: overlapping same-name installation replay")
	}
	if item.recipe.Provider != "plpgsql" {
		owner := "postgres"
		if item.ext != nil {
			role, err := p.script.DomainRoles.Descriptor(item.ext.Owner)
			if err != nil {
				return err
			}
			owner = role.Name
		}
		initial := item.recipe.Version
		// CREATE directly at the final release and CREATE+UPDATE can have different
		// immutable declaration origins. Reproduce the oldest surviving release.
		for _, version := range item.recipe.Implementation.Versions().InstallPath(item.recipe.Version) {
			found := false
			for _, d := range p.script.CatalogView.Descriptors() {
				origin := catalog.DescriptorOrigin(d)
				if origin.Valid(d.Address()) && origin.Installation.Cohort == item.recipe.Cohort && origin.Installation.Version == version {
					initial = version
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if dependency := p.cubeDependency[index]; dependency >= 0 {
			if err := p.placePrerequisite(dependency, p.cubePlacement[index]); err != nil {
				return err
			}
		}
		if !item.retainedSchema && !p.created[item.schema] {
			p.script.installationStatements = append(p.script.installationStatements, "CREATE SCHEMA "+catalog.QuoteIdentifier(item.schema)+" AUTHORIZATION "+catalog.QuoteIdentifier(owner)+";\n")
			p.created[item.schema] = true
			p.createdSchemas = append(p.createdSchemas, item.schema)
		}
		p.script.installationStatements = append(p.script.installationStatements, "SET SESSION AUTHORIZATION "+catalog.QuoteIdentifier(owner)+";\n", "CREATE EXTENSION "+catalog.QuoteIdentifier(item.recipe.Provider)+" WITH SCHEMA "+catalog.QuoteIdentifier(item.schema)+" VERSION '"+strings.ReplaceAll(initial, "'", "''")+"';\n", "SET SESSION AUTHORIZATION postgres;\n")
		if initial != item.recipe.Version {
			// Destination owner attributes need not match the source's. CREATE
			// retains the captured owner, but genuine script UPDATE runs as the
			// replay administrator; final owner metadata restores member owners.
			p.script.installationStatements = append(p.script.installationStatements, "ALTER EXTENSION "+catalog.QuoteIdentifier(item.recipe.Provider)+" UPDATE TO '"+strings.ReplaceAll(item.recipe.Version, "'", "''")+"';\n")
		}
	}
	p.currentSchema[index] = item.schema
	p.installed[index] = true
	p.active[item.recipe.Provider] = index
	delete(p.visiting, index)
	return nil
}

// placePrerequisite moves the same still-canonical cohort, never a same-named
// replacement selected by schema spelling. Dependents run before finalization.
func (p *installationSchedule) placePrerequisite(index int, schema string) error {
	if !p.installed[index] || p.completed[index] || p.items[index].recipe.Implementation != extension.StockCube || schema == "" {
		return fmt.Errorf("stuntgres: invalid declaration prerequisite placement")
	}
	current := p.currentSchema[index]
	if current == schema {
		return nil
	}
	if schema == "pg_catalog" {
		return fmt.Errorf("stuntgres: declaration prerequisite cannot move into pg_catalog")
	}
	if !p.created[schema] && p.script.CatalogView.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: schema}) == (ids.ObjectAddress{}) {
		p.script.installationStatements = append(p.script.installationStatements, "CREATE SCHEMA "+catalog.QuoteIdentifier(schema)+";\n")
		p.created[schema] = true
		p.createdSchemas = append(p.createdSchemas, schema)
	}
	p.script.installationStatements = append(p.script.installationStatements, "ALTER EXTENSION "+catalog.QuoteIdentifier(p.items[index].recipe.Provider)+" SET SCHEMA "+catalog.QuoteIdentifier(schema)+";\n")
	p.currentSchema[index] = schema
	return nil
}

func (p *installationSchedule) retireSchemas() {
	for _, schema := range p.createdSchemas {
		p.script.installationStatements = append(p.script.installationStatements, "DROP SCHEMA "+catalog.QuoteIdentifier(schema)+";\n")
	}
}
func (p *installationSchedule) installTemporary(name string) error {
	if p.temporary[name] {
		return nil
	}
	implementation, ok := p.script.CatalogView.Owner().Extensions[name]
	if !ok {
		return fmt.Errorf("stuntgres: missing registered replay prerequisite %q", name)
	}
	if _, ok := implementation.(extension.Installer); !ok {
		return extension.InvalidProviderDescriptor()
	}
	// No source installation or survivor needs this provider; only the real
	// dependent script does. Its whole baseline is retired by ordinary DROP.
	p.temporary[name] = true
	for _, required := range implementation.Control().Requires {
		if err := p.installTemporary(required); err != nil {
			return err
		}
	}
	schema := p.schema("__stuntgres_prerequisite_" + name)
	p.script.installationStatements = append(p.script.installationStatements, "CREATE SCHEMA "+catalog.QuoteIdentifier(schema)+";\n", "CREATE EXTENSION "+catalog.QuoteIdentifier(name)+" WITH SCHEMA "+catalog.QuoteIdentifier(schema)+";\n")
	p.temporaries = append(p.temporaries, temporaryInstallation{name, schema})
	return nil
}
func (p *installationSchedule) finish(index int) {
	p.completed[index] = true
	if p.items[index].ext == nil {
		delete(p.active, p.items[index].recipe.Provider)
		p.currentSchema[index] = ""
	} else {
		p.currentSchema[index] = p.script.CatalogView.MustNamespace(p.items[index].ext.TargetNamespace).Name()
	}
}
func (p *installationSchedule) retireTemporary() {
	for i := len(p.temporaries) - 1; i >= 0; i-- {
		item := p.temporaries[i]
		p.script.installationStatements = append(p.script.installationStatements, "DROP EXTENSION "+catalog.QuoteIdentifier(item.name)+";\n", "DROP SCHEMA "+catalog.QuoteIdentifier(item.schema)+";\n")
	}
}
