package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (e *Engine) regclassLocked(auth AuthorizationView, text string) (ids.ObjectRef, error) {
	typ, _ := auth.definitionType(sqltype.Oid)
	if oid, numeric, err := regobj.ClassNumeric(text, typ); numeric {
		if err != nil {
			return ids.ObjectRef{}, err
		}
		return e.catalog.Domain.Ref(auth.Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}), nil
	}
	name, err := parser.ParseRegclassName(text)
	if err != nil {
		return ids.ObjectRef{}, sqlError("42602", "invalid name syntax")
	}
	database := ""
	if auth.session != nil {
		database = auth.session.DatabaseName
	}
	name, err = regobj.ClassQualifiedName(name, text, database)
	if err != nil {
		return ids.ObjectRef{}, err
	}
	return e.resolveRelationReference(auth, name, false)
}

// Element input retains the statement's immutable authority, including its
// already-selected relation path. It never locks or consults a later catalog.
type capturedRegclassInput struct{ capture regobj.InputCapture }

func (r capturedRegclassInput) ResolveRegclass(ctx context.Context, text string) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	a := r.capture.Authority
	typ, _ := catalog.DefinitionTypeAt(a.View.Catalog, a.Definitions, sqltype.Oid)
	if oid, numeric, err := regobj.ClassNumeric(text, typ); numeric {
		return oid, err
	}
	name, err := parser.ParseRegclassName(text)
	if err != nil {
		return 0, sqlError("42602", "invalid name syntax")
	}
	name, err = regobj.ClassQualifiedName(name, text, r.capture.Database)
	if err != nil {
		return 0, err
	}
	path := r.capture.Path
	if len(name.Parts) == 2 {
		ns := catalog.DefinitionLookupAt(a.View.Catalog, a.Definitions, ids.NameKey{ClassID: ids.NamespaceClassID, Name: name.Parts[0].Text})
		if name.Parts[0].Text == "pg_temp" {
			ns = r.capture.Temporary
		}
		if ns == (ids.ObjectAddress{}) {
			path = nil
		} else {
			namespace, _ := catalog.DefinitionNamespaceAt(a.View.Catalog, a.Definitions, ns)
			if namespace.Temporary != (ids.SessionToken{}) && namespace.Temporary != r.capture.Token {
				return 0, sqlError("42501", fmt.Sprintf("permission denied for schema %s", namespace.Name()))
			}
			if err := a.View.CanUse(a.Environment, a.View.Ref(ns)); err != nil {
				return 0, err
			}
			path = []ids.ObjectAddress{ns}
		}
	}
	for _, ns := range path {
		if address := catalog.DefinitionLookupAt(a.View.Catalog, a.Definitions, ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: name.Parts[len(name.Parts)-1].Text}); address != (ids.ObjectAddress{}) {
			descriptor, _ := catalog.DescriptorAt(a.View.Catalog, a.Definitions, address)
			if _, ok := descriptor.(*catalog.Relation); !ok {
				return 0, fmt.Errorf("stuntgres: invalid relation descriptor")
			}
			return uint32(address.ObjectID), nil
		}
	}
	return 0, sqlError("42P01", fmt.Sprintf(`relation "%s" does not exist`, catalog.ObjectNameText(name)))
}
