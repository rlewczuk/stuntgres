package tsearch

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Lexize is ts_lexize: one dictionary's lexemes for text.
func Lexize[A Authority[A]](h Host[A], value sqltype.Value, text string, result *sqltype.Descriptor) (out sqltype.Value, err error) {
	mark := h.MarkDictionaryInitialization()
	defer func() {
		if err != nil {
			h.AbortDictionaryInitialization(mark)
		}
	}()
	if err := h.Operation().Context().Err(); err != nil {
		return sqltype.Value{}, err
	}
	oid, err := value.RegTSOID()
	if err != nil {
		return sqltype.Value{}, err
	}
	capture := captureDictionaryInvocation(h)
	runtime, err := capture.dictionary(h, ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: ids.OID(oid)})
	if err != nil {
		return sqltype.Value{}, err
	}
	lexemes, err := lexizeDictionary(h, runtime, h.Operation().Context(), text)
	if err != nil {
		return sqltype.Value{}, err
	}
	out, err = sqltype.LexicalArray(result, lexemes)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err = capture.accept(h.Operation().Context(), h); err != nil {
		return sqltype.Value{}, err
	}
	return out, nil
}
