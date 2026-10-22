package access

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// CheckPersistentCreationNamespace keeps the TEMP capability boundary of
// non-table creation and namespace moves.
func CheckPersistentCreationNamespace(view catalog.Read, namespace ids.ObjectAddress) error {
	if view.MustNamespace(namespace).Temporary != (ids.SessionToken{}) {
		return sqlError("0A000", "temporary schema creation is unsupported")
	}
	return nil
}

// CheckTemporaryStorageAccess rejects another session's stored TEMP relation.
// Namespace lookup and role privileges precede physical storage admission.
// Identity/metadata providers do not acquire another session's stored TEMP data.
func CheckTemporaryStorageAccess(view catalog.Read, relation *catalog.Relation, token ids.SessionToken) error {
	if relation == nil {
		return ids.ErrCatalogReplaced
	}
	if !relation.RelationKind.Stored() {
		return nil
	}
	namespace, ok := view.Namespace(relation.Namespace())
	if !ok {
		return ids.ErrCatalogReplaced
	}
	if namespace.Temporary != (ids.SessionToken{}) && namespace.Temporary != token {
		return sqlError("0A000", "cannot access temporary tables of other sessions")
	}
	return nil
}

// OwnTemporaryNamespace reports whether ns is the session's own TEMP namespace
// temp, as the session's access.Environment reports it. TOAST namespaces and
// persistent namespaces are never owned. Callers read temp only for a TEMP
// namespace.
func OwnTemporaryNamespace(temp ids.ObjectAddress, ns *catalog.Namespace) bool {
	return ns != nil && !ns.TemporaryToast && ns.Temporary != (ids.SessionToken{}) && temp == ns.Address()
}

// AdjustRelationCreationPersistence is the persistence of a relation created
// in namespace ref of v's catalog with the requested persistence: a logged
// relation in the session's own TEMP namespace becomes temporary, and the TEMP
// boundary rejects the rest. env's TEMP namespace is read only for a TEMP namespace.
// Creation permission precedes persistence adjustment, including for SET SCHEMA.
// Callers supply the coherent catalog containing any privately staged namespace.
func AdjustRelationCreationPersistence(v View, env Environment, ref ids.ObjectRef, requested string) (string, error) {
	ns := v.Catalog.MustNamespace(ref.Address)
	temporary := ns.Temporary != (ids.SessionToken{})
	own := temporary && OwnTemporaryNamespace(env.TemporaryNamespace(), ns)
	switch requested {
	case "temporary":
		if own {
			return requested, nil
		}
		if temporary {
			return "", sqlError("42P16", "cannot create relations in temporary schemas of other sessions")
		}
		return "", sqlError("42P16", "cannot create temporary relation in non-temporary schema")
	case "logged":
		if own {
			return "temporary", nil
		}
		if temporary {
			return "", sqlError("42P16", "cannot create relations in temporary schemas of other sessions")
		}
	default:
		if temporary {
			return "", sqlError("42P16", "only temporary relations may be created in temporary schemas")
		}
	}
	return requested, nil
}
