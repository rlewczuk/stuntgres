package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Lexize is ts_lexize: one dictionary's lexemes for text.
func Lexize[A Authority[A]](h Host[A], value sqltype.Value, text string, result *sqltype.Descriptor) (sqltype.Value, error) {
	if err := h.Operation().Context().Err(); err != nil {
		return sqltype.Value{}, err
	}
	oid, err := value.RegTSOID()
	if err != nil {
		return sqltype.Value{}, err
	}
	runtime, err := acquireDictionary(h, ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: ids.OID(oid)})
	if err != nil {
		return sqltype.Value{}, err
	}
	lexemes, err := runtime.Lexize(h.Operation().Context(), text)
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.LexicalArray(result, lexemes)
}
