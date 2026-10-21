package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) resolveGrantObject(v AuthorizationView, name parser.ObjectName, kind string) (ids.ObjectRef, error) {
	if kind == "" || kind == "sequence" {
		return e.resolveRelationReference(v, name, true)
	}
	if kind == "tablespace" {
		return resolveGrantTablespace(v, catalog.ObjectNameText(name))
	}
	var address ids.ObjectAddress
	if kind == "schema" {
		if len(name.Parts) == 1 {
			address = v.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name.Parts[0].Text})
			if name.Parts[0].Text == "pg_temp" {
				address = temporaryNamespace(v)
			}
		}
		if address == (ids.ObjectAddress{}) {
			return ids.ObjectRef{}, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, catalog.ObjectNameText(name)))
		}
	} else {
		path, err := e.lookupNamespaces(v, name, analyze.TypeLookup)
		if err != nil {
			return ids.ObjectRef{}, err
		}
		for i := range path.Count() {
			found := v.Catalog.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: path.At(i), Name: name.Parts[len(name.Parts)-1].Text})
			if len(found) == 1 {
				address = found[0]
				break
			}
		}
		if address == (ids.ObjectAddress{}) {
			return ids.ObjectRef{}, sqlError("42704", fmt.Sprintf(`type "%s" does not exist`, catalog.ObjectNameText(name)))
		}
		t := v.Catalog.MustType(address)
		if kind == "domain" && !t.SQLType.IsDomain() {
			return ids.ObjectRef{}, sqlError("42809", fmt.Sprintf(`"%s" is not a domain`, catalog.ObjectNameText(name)))
		}
		if t.SQLType.IsArray() {
			diagnostic := sqlError("0LP01", "cannot set privileges of array types").(*sqlstate.Error)
			diagnostic.Hint = "Set the privileges of the element type instead."
			return ids.ObjectRef{}, diagnostic
		}
	}
	return e.catalog.Domain.Ref(v.Catalog.Epoch(), address), nil
}
