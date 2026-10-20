package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (e *Engine) findCollation(auth AuthorizationView, raw parser.ObjectName) (*catalog.Collation, error) {
	path, err := e.lookupNamespaces(auth, raw, collationLookup)
	if err != nil {
		return nil, err
	}
	name := raw.Parts[len(raw.Parts)-1].Text
	for i := range path.count() {
		a := auth.definitionLookup(ids.NameKey{ClassID: ids.CollationClassID, Namespace: path.at(i), Name: name})
		descriptor, _ := auth.definitionDescriptor(a)
		if d, ok := descriptor.(*catalog.Collation); ok {
			one := [1]collationCandidate{{a, d.Encoding}}
			if selectCollation(one[:], 6) != (ids.ObjectAddress{}) {
				return d, nil
			}
		}
	}
	return nil, expr.ErrorAt("42704", fmt.Sprintf(`collation "%s" for encoding "UTF8" does not exist`, objectNameText(raw)), raw.Parts[0].Pos)
}

func (e *Engine) resolveColumnCollation(u sqltype.Usage, raw *parser.ObjectName, op *Operation) (sqltype.Usage, error) {
	if raw == nil {
		return u, nil
	}
	pos := raw.Parts[0].Pos
	if u.Type.Usage().Collation.ID == 0 {
		binder := newExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
		return sqltype.Usage{}, expr.ErrorAt("42804", "collations are not supported by type "+binder.diagnosticTypeName(u.Type), pos)
	}
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return sqltype.Usage{}, err
	}
	d, err := e.findCollation(auth, *raw)
	if err != nil {
		return sqltype.Usage{}, err
	}
	ref, err := d.Usage()
	if err != nil {
		return sqltype.Usage{}, expr.ErrorAt("0A000", "collation is unsupported", pos)
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
	if _, err = d.Usage(); err != nil {
		return 0, expr.ErrorAt("0A000", "collation is unsupported", raw.Parts[0].Pos)
	}
	return uint32(d.OID), nil
}
