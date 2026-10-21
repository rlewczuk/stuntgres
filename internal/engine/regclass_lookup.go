package engine

import (
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
