package ddl

import (
	"fmt"
	"sort"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// SchemaDropTarget preserves each original name occurrence. A zero Ref is a
// conditional absence, not permission to resolve a newly appeared namespace.
type SchemaDropTarget struct {
	Name string
	Ref  ids.ObjectRef
}

// SchemaDropManifest binds even an all-absent target list to its catalog domain
// and epoch. It survives lock waits without retaining stale authorization.
type SchemaDropManifest struct {
	Domain  *ids.Domain
	Epoch   ids.CatalogEpoch
	Targets []SchemaDropTarget
}

// CaptureSchemaDrop resolves targets and initial ownership without staging or
// emitting notices. The caller retains the entire ordered manifest across waits.
func CaptureSchemaDrop(h Host, auth Authority, s *parser.DropSchema) (SchemaDropManifest, error) {
	view := h.AccessView(auth)
	manifest := SchemaDropManifest{Domain: view.Catalog.Domain(), Epoch: view.Catalog.Epoch(), Targets: make([]SchemaDropTarget, 0, len(s.Names))}
	for _, name := range s.Names {
		target := SchemaDropTarget{Name: name}
		d, err := lookupSchema(view.Catalog, name)
		if err != nil {
			if !s.IfExists {
				return SchemaDropManifest{}, err
			}
		} else {
			if err := checkSchemaOwner(h, auth, d); err != nil {
				return SchemaDropManifest{}, err
			}
			target.Ref = ObjectRef(h, auth, d.Address())
		}
		manifest.Targets = append(manifest.Targets, target)
	}
	return manifest, nil
}

// StageDropSchema validates the complete captured binding set before notices or
// removals, then constructs the dependency closure from exact captured refs.
// The caller holds the same catalog/authorization critical section throughout.
func StageDropSchema(h SchemaHost, auth Authority, s *parser.DropSchema, manifest SchemaDropManifest) (bool, error) {
	view := h.AccessView(auth)
	if manifest.Domain != view.Catalog.Domain() || manifest.Epoch != view.Catalog.Epoch() {
		return false, ids.ErrCatalogReplaced
	}
	for _, target := range manifest.Targets {
		address := view.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: target.Name})
		if target.Ref == (ids.ObjectRef{}) {
			if address != (ids.ObjectAddress{}) {
				return false, ids.ErrCatalogReplaced
			}
		} else if target.Ref.Domain() != manifest.Domain || target.Ref.Epoch != manifest.Epoch || address != target.Ref.Address || view.Catalog.Get(target.Ref.Address) == nil {
			return false, ids.ErrCatalogReplaced
		}
	}
	seen := make(map[ids.ObjectAddress]bool)
	var schemas []*catalog.Namespace
	for _, target := range manifest.Targets {
		if target.Ref == (ids.ObjectRef{}) || seen[target.Ref.Address] {
			continue
		}
		d := view.Catalog.MustNamespace(target.Ref.Address)
		if err := checkSchemaOwner(h, auth, d); err != nil {
			return false, err
		}
		seen[d.Address()] = true
		schemas = append(schemas, d)
	}
	for _, target := range manifest.Targets {
		if target.Ref == (ids.ObjectRef{}) {
			Notice(h, "00000", fmt.Sprintf(`schema "%s" does not exist, skipping`, target.Name))
		}
	}
	children, err := catalog.NamespaceChildren(view.Catalog, seen)
	if err != nil {
		return false, err
	}
	dependent, domains, err := schemaDependents(view.Catalog, children)
	if err != nil {
		return false, err
	}
	// A schema is also a referent of scalar REGNAMESPACE constants in
	// definitions outside it. Feed its identity to the existing dependency
	// closure before publication; contained-object removal alone is not enough.
	for _, schema := range schemas {
		h.DropTarget(schema.Address())
	}
	if err = h.RecordDependencyPath(auth); err != nil {
		return false, err
	}
	if len(domains) != 0 {
		if err = h.StageSchemaDependents(auth, schemas, dependent, domains, s.Cascade); err != nil {
			return false, err
		}
		return true, nil
	}
	sort.Slice(dependent, func(i, j int) bool { return dependent[i].Address().ObjectID < dependent[j].Address().ObjectID })
	if len(dependent) > 0 && !s.Cascade {
		message := "cannot drop desired object(s) because other objects depend on them"
		if len(schemas) == 1 {
			message = "cannot drop schema " + catalog.DependencyIdentifier(schemas[0].Name()) + " because other objects depend on it"
		}
		details := make([]string, len(dependent))
		for i, r := range dependent {
			ns := view.Catalog.Get(r.Namespace()).Name()
			kind, name := h.RelationDiagnostic(auth, r)
			details[i] = kind + " " + name + " depends on schema " + catalog.DependencyIdentifier(ns)
		}
		return false, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: message, Detail: strings.Join(details, "\n"), Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
	}
	if s.Cascade {
		for _, r := range dependent {
			kind, name := h.RelationDiagnostic(auth, r)
			h.AddCascadeNotice("drop cascades to " + kind + " " + name)
		}
	}
	for _, r := range dependent {
		if err = h.RemoveObject(ObjectRef(h, auth, r.Address()), false); err != nil {
			return false, err
		}
	}
	for _, d := range schemas {
		if err = h.RemoveObject(ObjectRef(h, auth, d.Address()), true); err != nil {
			return false, err
		}
	}
	return len(schemas) != 0, nil
}

// schemaDependents classifies the children of dropped schemas: relations
// dropped with them, and the other objects the dependency closure handles.
// Extension members, composite and index relations and autogenerated types
// go with their owner.
func schemaDependents(read catalog.Read, children []ids.ObjectAddress) ([]*catalog.Relation, []catalog.Descriptor, error) {
	var dependent []*catalog.Relation
	var domains []catalog.Descriptor
	seen := make(map[ids.ObjectAddress]bool)
	add := func(d catalog.Descriptor) {
		if !seen[d.Address()] {
			seen[d.Address()] = true
			domains = append(domains, d)
		}
	}
	for _, address := range children {
		d := read.Get(address)
		if catalog.ExtensionMembership(d) != (ids.ObjectAddress{}) {
			continue // the extension owns these members as one namespace dependent
		}
		if r, ok := d.(*catalog.Relation); ok {
			if r.RelationKind == catalog.CompositeRelation {
				continue
			}
			if r.SQLIndex != nil {
				continue
			}
			dependent = append(dependent, r)
		} else if t, ok := d.(*catalog.Type); ok && t.SQLType.IsUserDefined() && (t.OwningRelation == (ids.ObjectAddress{}) || t.SQLType.IsComposite() && read.MustRelation(t.OwningRelation).RelationKind == catalog.CompositeRelation) {
			if t.SQLType.IsMultirange() {
				owner, err := catalog.TypeUsageAuthority(read, t)
				if err != nil {
					return nil, nil, err
				}
				add(owner)
			} else {
				add(t)
			}
		} else if c, ok := d.(*catalog.Collation); ok {
			add(c)
		} else if c, ok := d.(*catalog.Conversion); ok {
			add(c)
		} else if dictionary, ok := d.(*catalog.TSDictionary); ok {
			add(dictionary)
		} else if configuration, ok := d.(*catalog.TSConfiguration); ok {
			add(configuration)
		} else if family, ok := d.(*catalog.OperatorFamily); ok {
			add(family)
		} else if class, ok := d.(*catalog.OperatorClass); ok {
			add(class)
		} else if d.Kind() == ids.OperatorFamilyOperatorObject || d.Kind() == ids.OperatorFamilySupportObject {
			continue // family retirement owns its separately addressed members
		} else if operator, ok := d.(*catalog.Operator); ok {
			add(operator)
		} else if extension, ok := d.(*catalog.Extension); ok {
			add(extension)
		} else if procedure, ok := d.(*catalog.Procedure); ok && procedure.RangeConstructor != nil {
			if err := catalog.ValidateRangeConstructor(read, procedure); err != nil {
				return nil, nil, err
			}
			continue // genuine INTERNAL children follow the owning family
		} else if procedure, ok := d.(*catalog.Procedure); ok && (procedure.Foreign != nil || procedure.RoutineKind != catalog.ProcedureRoutine && procedure.DynamicCallable()) {
			add(procedure)
		} else if t, ok := d.(*catalog.Type); ok && (t.OwningRelation != (ids.ObjectAddress{}) || t.SQLType.IsArray() && t.SQLType.Element().IsUserDefined()) {
			continue // removed automatically with the owning relation or element type
		} else {
			return nil, nil, sqlError("0A000", "dropping schemas with unsupported object dependencies is unsupported")
		}
	}
	return dependent, domains, nil
}
