package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func builtinCollationName(id uint32) string {
	switch id {
	case 100:
		return "default"
	case 950:
		return "C"
	case 951:
		return "POSIX"
	case 811:
		return "pg_c_utf8"
	case 6411:
		return "pg_unicode_fast"
	}
	return ""
}

// Called under Engine.mu, using the ordinary namespace authorization rules.
// These five deterministic identities share the UTF8 byte/codepoint comparator.
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
	path, err := e.lookupNamespaces(auth, *raw, collationLookup)
	if err != nil {
		return sqltype.Usage{}, err
	}
	name := raw.Parts[len(raw.Parts)-1].Text
	for i := range path.count() {
		if path.at(i) != (ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}) {
			continue
		}
		var id uint32
		switch name {
		case "default":
			id = 100
		case "C":
			id = 950
		case "POSIX":
			id = 951
		case "pg_c_utf8":
			id = 811
		case "pg_unicode_fast":
			id = 6411
		case "C.utf8", "C.UTF-8", "ucs_basic", "unicode":
			return sqltype.Usage{}, expressionError("0A000", "collation is unsupported", pos)
		}
		if id != 0 {
			u.Collation = sqltype.CollationRef{ID: id, Derivation: sqltype.Default}
			return u, nil
		}
	}
	return sqltype.Usage{}, expressionError("42704", fmt.Sprintf(`collation "%s" for encoding "UTF8" does not exist`, objectNameText(*raw)), pos)
}
