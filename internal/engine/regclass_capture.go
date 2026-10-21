package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (e *Engine) regclassLocked(auth AuthorizationView, text string) (ids.ObjectRef, error) {
	typ, _ := auth.definitionType(sqltype.Oid)
	if oid, numeric, err := regclassNumeric(text, typ); numeric {
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
	name, err = regclassQualifiedName(name, text, database)
	if err != nil {
		return ids.ObjectRef{}, err
	}
	return e.resolveRelationReference(auth, name, false)
}

// Element input retains the statement's immutable authority, including its
// already-selected relation path. It never locks or consults a later catalog.
type capturedRegclassInput struct {
	auth      AuthorizationView
	path      []ids.ObjectAddress
	temporary ids.ObjectAddress
	token     ids.SessionToken
	database  string
}

func (e *Engine) captureRegclassInput(auth AuthorizationView) (sqltype.RegclassResolver, error) {
	path, err := e.referencePath(auth)
	if err != nil {
		return nil, err
	}
	input := capturedRegclassInput{auth: auth, path: path, temporary: temporaryNamespace(auth)}
	if auth.session != nil {
		input.token = auth.session.SessionToken
		input.database = auth.session.DatabaseName
	}
	input.auth.session = nil
	input.auth.settings = nil
	return input, nil
}

func (r capturedRegclassInput) ResolveRegclass(ctx context.Context, text string) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	typ, _ := r.auth.definitionType(sqltype.Oid)
	if oid, numeric, err := regclassNumeric(text, typ); numeric {
		return oid, err
	}
	name, err := parser.ParseRegclassName(text)
	if err != nil {
		return 0, sqlError("42602", "invalid name syntax")
	}
	name, err = regclassQualifiedName(name, text, r.database)
	if err != nil {
		return 0, err
	}
	path := r.path
	if len(name.Parts) == 2 {
		ns := r.auth.definitionLookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name.Parts[0].Text})
		if name.Parts[0].Text == "pg_temp" {
			ns = r.temporary
		}
		if ns == (ids.ObjectAddress{}) {
			path = nil
		} else {
			namespace, _ := r.auth.definitionNamespace(ns)
			if namespace.Temporary != (ids.SessionToken{}) && namespace.Temporary != r.token {
				return 0, sqlError("42501", fmt.Sprintf("permission denied for schema %s", namespace.Name()))
			}
			if err := r.auth.CanUse(r.auth.Ref(ns)); err != nil {
				return 0, err
			}
			path = []ids.ObjectAddress{ns}
		}
	}
	for _, ns := range path {
		if address := r.auth.definitionLookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: name.Parts[len(name.Parts)-1].Text}); address != (ids.ObjectAddress{}) {
			descriptor, _ := r.auth.definitionDescriptor(address)
			if _, ok := descriptor.(*catalog.Relation); !ok {
				return 0, fmt.Errorf("stuntgres: invalid relation descriptor")
			}
			return uint32(address.ObjectID), nil
		}
	}
	return 0, sqlError("42P01", fmt.Sprintf(`relation "%s" does not exist`, catalog.ObjectNameText(name)))
}
