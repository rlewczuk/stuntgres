package engine

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func collationName(view CatalogRead, id uint32) string {
	if view.state != nil {
		if d, ok := view.state.descriptors[ids.ObjectAddress{ClassID: CollationClassID, ObjectID: ids.OID(id)}].(*collationDescriptor); ok {
			return d.name
		}
	}
	for i := range builtinCollations {
		if uint32(builtinCollations[i].oid) == id {
			return builtinCollations[i].name
		}
	}
	return ""
}

func collationSQLName(view CatalogRead, id uint32) string {
	if view.state != nil {
		if d, ok := view.state.descriptors[ids.ObjectAddress{ClassID: CollationClassID, ObjectID: ids.OID(id)}].(*collationDescriptor); ok {
			return quoteIdentifier(view.state.descriptors[d.Namespace()].Name()) + "." + quoteIdentifier(d.name)
		}
	}
	if name := collationName(view, id); name != "" {
		return quoteIdentifier("pg_catalog") + "." + quoteIdentifier(name)
	}
	return ""
}

func (e *Engine) findCollation(auth AuthorizationView, raw parser.ObjectName) (*collationDescriptor, error) {
	path, err := e.lookupNamespaces(auth, raw, collationLookup)
	if err != nil {
		return nil, err
	}
	name := raw.Parts[len(raw.Parts)-1].Text
	for i := range path.count() {
		a := auth.definitionLookup(ids.NameKey{ClassID: CollationClassID, Namespace: path.at(i), Name: name})
		descriptor, _ := auth.definitionDescriptor(a)
		if d, ok := descriptor.(*collationDescriptor); ok {
			one := [1]collationCandidate{{a, d.encoding}}
			if selectCollation(one[:], 6) != (ids.ObjectAddress{}) {
				return d, nil
			}
		}
	}
	return nil, expressionError("42704", fmt.Sprintf(`collation "%s" for encoding "UTF8" does not exist`, objectNameText(raw)), raw.Parts[0].Pos)
}

func (e *Engine) resolveColumnCollation(u sqltype.Usage, raw *parser.ObjectName, op *Operation) (sqltype.Usage, error) {
	if raw == nil {
		return u, nil
	}
	pos := raw.Parts[0].Pos
	if u.Type.Usage().Collation.ID == 0 {
		binder := newExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
		return sqltype.Usage{}, expressionError("42804", "collations are not supported by type "+binder.diagnosticTypeName(u.Type), pos)
	}
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return sqltype.Usage{}, err
	}
	d, err := e.findCollation(auth, *raw)
	if err != nil {
		return sqltype.Usage{}, err
	}
	ref, err := d.usage()
	if err != nil {
		return sqltype.Usage{}, expressionError("0A000", "collation is unsupported", pos)
	}
	u.Collation = ref
	return u, nil
}

func (e *Engine) lookupCollation(raw parser.ObjectName, op *Operation) (uint32, error) {
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return 0, err
	}
	d, err := e.findCollation(auth, raw)
	if err != nil {
		return 0, err
	}
	if _, err = d.usage(); err != nil {
		return 0, expressionError("0A000", "collation is unsupported", raw.Parts[0].Pos)
	}
	return uint32(d.oid), nil
}
