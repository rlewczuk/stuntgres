package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (e *Engine) findCollation(auth AuthorizationView, raw parser.ObjectName) (*catalog.Collation, error) {
	path, err := e.lookupNamespaces(auth, raw, analyze.CollationLookup)
	if err != nil {
		return nil, err
	}
	name := raw.Parts[len(raw.Parts)-1].Text
	for i := range path.Count() {
		a := auth.DefinitionLookup(ids.NameKey{ClassID: ids.CollationClassID, Namespace: path.At(i), Name: name})
		descriptor, _ := auth.definitionDescriptor(a)
		if d, ok := descriptor.(*catalog.Collation); ok {
			one := [1]regobj.CollationCandidate{{Address: a, Encoding: d.Encoding}}
			if regobj.SelectCollation(one[:], 6) != (ids.ObjectAddress{}) {
				return d, nil
			}
		}
	}
	return nil, expr.ErrorAt("42704", fmt.Sprintf(`collation "%s" for encoding "UTF8" does not exist`, catalog.ObjectNameText(raw)), raw.Parts[0].Pos)
}

func (e *Engine) resolveColumnCollation(u sqltype.Usage, raw *parser.ObjectName, op *session.Operation) (sqltype.Usage, error) {
	if raw == nil {
		return u, nil
	}
	pos := raw.Parts[0].Pos
	if u.Type.Usage().Collation.ID == 0 {
		binder := NewExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
		return sqltype.Usage{}, expr.ErrorAt("42804", "collations are not supported by type "+binder.DiagnosticTypeName(u.Type), pos)
	}
	auth, err := e.AuthorizationLocked(op)
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

func (e *Engine) lookupCollation(raw parser.ObjectName, op *session.Operation) (uint32, error) {
	auth, err := e.AuthorizationLocked(op)
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
