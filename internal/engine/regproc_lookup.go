package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

type routineKey struct {
	name      string
	signature catalog.ProcedureSignature
}

var routineByOID, routinesByName, routineBySignature = indexBuiltinRoutines()

// Default counts absent from the native provider declaration inventory remain
// lookup-only metadata, never executable registrations. PostgreSQL REL_18_6:
// src/backend/catalog/system_functions.sql, pg_terminate_backend(pid integer,
// timeout int8 DEFAULT 0).
var builtinRoutineIdentityDefaults = map[ids.OID]uint8{2096: 1}

func routineIdentityDefaults(id ids.OID) uint8 {
	if declaration := BuiltinRoutine(id); declaration != nil {
		return declaration.Defaults
	}
	return builtinRoutineIdentityDefaults[id]
}

func indexBuiltinRoutines() (map[ids.OID]catalog.RoutineIdentity, map[string][]catalog.RoutineIdentity, map[routineKey]catalog.RoutineIdentity) {
	byOID := make(map[ids.OID]catalog.RoutineIdentity, len(builtinRoutineIdentities))
	byName := make(map[string][]catalog.RoutineIdentity)
	bySignature := make(map[routineKey]catalog.RoutineIdentity, len(builtinRoutineIdentities))
	for _, p := range builtinRoutineIdentities {
		byOID[p.ID] = p
		byName[p.Name] = append(byName[p.Name], p)
		bySignature[routineKey{p.Name, p.Signature}] = p
	}
	return byOID, byName, bySignature
}

func routineIdentityByOID(view catalog.Read, id ids.OID) (catalog.RoutineIdentity, bool) {
	if p, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}); ok {
		return catalog.RoutineIdentity{p.Address().ObjectID, p.Namespace().ObjectID, p.Name(), p.Signature}, true
	}
	if p, ok := routineByOID[id]; ok {
		return p, true
	}
	if owner := routineDescriptorOwner(view, id); owner != nil {
		for _, helper := range owner.Aggregate.Helpers {
			if helper.ID == id {
				return helper, true
			}
		}
	}
	return catalog.RoutineIdentity{}, false
}

func routineNamespaceVisible(view catalog.Read, namespace ids.ObjectAddress) bool {
	ns, ok := view.Namespace(namespace)
	return ok && ns.Temporary == (ids.SessionToken{})
}

// Exact signatures use both existing catalog NameKeys and the immutable release
// signature index. The first candidate masks all later identical signatures.
func selectExactRoutine(view catalog.Read, path namespaceSelection, name string, signature catalog.ProcedureSignature) (catalog.RoutineIdentity, int) {
	for i := range path.count() {
		namespace := path.at(i)
		if !routineNamespaceVisible(view, namespace) {
			continue
		}
		if p := procedureInNamespaces(view, namespaceSelection{qualified: namespace}, name, signature); p != nil {
			return catalog.RoutineIdentity{p.Address().ObjectID, p.Namespace().ObjectID, p.Name(), p.Signature}, 1
		}
		for _, d := range view.Descriptors() {
			if owner, ok := d.(*catalog.Procedure); ok && owner.Aggregate != nil && owner.Namespace() == namespace {
				for _, helper := range owner.Aggregate.Helpers {
					if helper.Name == name && helper.Signature == signature {
						return helper, 1
					}
				}
			}
		}
		if namespace.ObjectID == ids.PGCatalogNamespaceOID {
			if p, ok := routineBySignature[routineKey{name, signature}]; ok {
				return p, 1
			}
		}
	}
	return catalog.RoutineIdentity{}, 0
}

// Earlier schemas mask only identical signatures, not every same-named routine.
// Bare-name selection stops after two candidates, the regproc ambiguity boundary.
func selectRoutine(view catalog.Read, path namespaceSelection, name string, signature *catalog.ProcedureSignature) (catalog.RoutineIdentity, int) {
	if signature != nil {
		return selectExactRoutine(view, path, name, *signature)
	}
	var selected catalog.RoutineIdentity
	count := 0
	seen := make(map[catalog.ProcedureSignature]bool)
	accept := func(p catalog.RoutineIdentity) bool {
		if seen[p.Signature] {
			return false
		}
		seen[p.Signature] = true
		selected = p
		count++
		return count == 2
	}
	for i := range path.count() {
		namespace := path.at(i)
		if !routineNamespaceVisible(view, namespace) {
			continue
		}
		for _, d := range view.Descriptors() {
			if p, ok := d.(*catalog.Procedure); ok && p.Namespace() == namespace {
				if p.Name() == name && accept(catalog.RoutineIdentity{p.Address().ObjectID, p.Namespace().ObjectID, p.Name(), p.Signature}) {
					return selected, count
				}
				if p.Aggregate != nil {
					for _, helper := range p.Aggregate.Helpers {
						if helper.Name == name && accept(helper) {
							return selected, count
						}
					}
				}
			}
		}
		if namespace.ObjectID == ids.PGCatalogNamespaceOID {
			for _, p := range routinesByName[name] {
				if accept(p) {
					return selected, count
				}
			}
		}
	}
	return selected, count
}
