package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) LexizeDictionary(value sqltype.Value, text string, result *sqltype.Descriptor) (sqltype.Value, error) {
	s := h.scope()
	if err := s.Op.Context().Err(); err != nil {
		return sqltype.Value{}, err
	}
	oid, err := value.RegTSOID()
	if err != nil {
		return sqltype.Value{}, err
	}
	runtime, err := h.acquireDictionary(ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: ids.OID(oid)})
	if err != nil {
		return sqltype.Value{}, err
	}
	lexemes, err := runtime.Lexize(s.Op.Context(), text)
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.LexicalArray(result, lexemes)
}
