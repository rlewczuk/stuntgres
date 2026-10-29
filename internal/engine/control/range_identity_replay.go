package control

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
)

type rangeReplayCreation struct {
	root, companion *catalog.Type
	coordinates     typedef.TypeDumpCreation
	schema          string
}

func (p *installationTypeReplay) freshTypeLabel(prefix string) string {
	for {
		p.serial++
		name := prefix + strconv.FormatUint(p.serial, 10)
		if p.reservedNames[name] || p.reservedNames["_"+name] {
			continue
		}
		p.reservedNames[name], p.reservedNames["_"+name] = true, true
		return name
	}
}

// Only creation coordinates change. Subtype, diff, opclass and collation still
// resolve through the unmodified captured view. Unique real schemas prevent a
// constructor from colliding with its own already-created subtype-diff routine.
func (s *dumpScript) beforeTypeCreate(root, companion *catalog.Type) (typedef.TypeDumpCreation, error) {
	creation := typedef.TypeDumpCreation{Root: catalog.DescriptorNameKey(root)}
	if companion == nil {
		return creation, nil
	}
	creation.Companion = catalog.DescriptorNameKey(companion)
	p := s.installationTypeReplay
	namespace, err := p.namespace()
	if err != nil {
		return creation, err
	}
	schema := ""
	for {
		p.serial++
		schema = "__stuntgres_range_work_" + strconv.FormatUint(p.serial, 10)
		if s.CatalogView.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: schema}) == (ids.ObjectAddress{}) {
			break
		}
	}
	creation.Root.Namespace, creation.Companion.Namespace = namespace, namespace
	creation.Root.Name = p.freshTypeLabel("__stuntgres_range_")
	creation.Companion.Name = p.freshTypeLabel("__stuntgres_multirange_")
	creation.RootSQL, creation.CompanionSQL = replayTypeSQL(schema, creation.Root.Name), replayTypeSQL(schema, creation.Companion.Name)
	s.rangeCreations[root.Address().ObjectID] = &rangeReplayCreation{root: root, companion: companion, coordinates: creation, schema: schema}
	// Emit the private schema at this range's declaration, before its captured
	// owner directive. A global prefix would consume an OID before earlier
	// alias endpoints and could turn a dangling numeric bound into that object.
	return creation, nil
}

func (s *dumpScript) emitRangeCreationPrelude(w *dumpWriter, oid ids.OID) error {
	creation := s.rangeCreations[oid]
	if creation == nil {
		return nil
	}
	previous := w.currentOwner
	if err := w.setOwner("postgres"); err != nil {
		return err
	}
	if err := w.emit("CREATE SCHEMA " + catalog.QuoteIdentifier(creation.schema) + ";\n"); err != nil {
		return err
	}
	if previous != "" {
		return w.setOwner(previous)
	}
	if err := w.emit("SET SESSION AUTHORIZATION DEFAULT;\n"); err != nil {
		return err
	}
	w.currentOwner = ""
	return nil
}

func (s *dumpScript) completeRangeCreation(oid ids.OID, creation *rangeReplayCreation) error {
	model := s.installationTypeReplay.names
	var statements []string
	for _, parent := range []*catalog.Type{creation.root, creation.companion} {
		id := parent.SQLType.ID()
		original := model.Names[id]
		renamed := original
		renamed.Name = parent.Name()
		if original.Name != renamed.Name {
			if err := model.Relocate(id, renamed, false); err != nil {
				return err
			}
			statements = append(statements, "ALTER TYPE "+replayTypeSQL(creation.schema, original.Name)+" RENAME TO "+catalog.QuoteIdentifier(parent.Name())+";\n")
		}
		normalization, err := s.normalizeArrayInWork(parent, creation.schema)
		if err != nil {
			return err
		}
		if normalization != "" {
			statements = append(statements, normalization)
		}
		if err := model.Relocate(id, catalog.DescriptorNameKey(parent), true); err != nil {
			return err
		}
		statements = append(statements, "ALTER TYPE "+replayTypeSQL(creation.schema, parent.Name())+" SET SCHEMA "+catalog.QuoteIdentifier(s.CatalogView.MustNamespace(parent.Namespace()).Name())+";\n")
	}
	// All five constructors have genuine INTERNAL dependencies on the live
	// parents. Their public labels/namespace/owner are independent SQL state;
	// parent identity movement refreshes generations, never these labels.
	procedures := s.installationTypeReplay.constructors[creation.root.SQLType.ID()]
	if len(procedures) != 5 {
		return fmt.Errorf("stuntgres: incomplete generated range constructor family")
	}
	bootstrap, ok := s.DomainRoles.Lookup("postgres")
	if !ok {
		return fmt.Errorf("stuntgres: missing bootstrap routine owner")
	}
	var moves []providerReplay
	forms := map[catalog.RangeConstructorForm]bool{}
	for _, procedure := range procedures {
		if err := catalog.ValidateRangeConstructor(s.CatalogView, procedure); err != nil {
			return err
		}
		constructor := procedure.RangeConstructor
		if forms[constructor.Form] {
			return fmt.Errorf("stuntgres: duplicate generated range constructor form")
		}
		forms[constructor.Form] = true
		canonical := creation.coordinates.Root.Name
		if constructor.Type.IsMultirange() {
			canonical = creation.coordinates.Companion.Name
		}
		identity, err := extension.OwnerIdentity(s.CatalogView, procedure)
		if err != nil {
			return err
		}
		owner, err := s.DomainRoles.Descriptor(procedure.Owner)
		if err != nil {
			return err
		}
		moves = append(moves, providerReplay{original: providerKey{creation.schema, canonical, procedure.Signature}, current: providerKey{identity.Schema, identity.Name, procedure.Signature}, arguments: identity.Signature, objectKind: "FUNCTION", ownerChanged: procedure.Owner != bootstrap.Ref, owner: owner.Name})
	}
	final, err := planProviderReplay(moves)
	if err != nil {
		return err
	}
	statements = append(statements, final...)
	statements = append(statements, "DROP SCHEMA "+catalog.QuoteIdentifier(creation.schema)+";\n")
	s.typeReplayEpilogues[oid] += strings.Join(statements, "")
	return nil
}
