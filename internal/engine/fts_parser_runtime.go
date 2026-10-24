package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) ResolveFTSParser(value sqltype.Value) error {
	s := h.scope()
	var oid uint32
	var err error
	if value.Type().ID() == sqltype.Oid {
		oid, err = value.OID()
	} else {
		var text string
		text, err = value.Text()
		if err != nil { return err }
		input, ok := s.input.RegTS.(regobj.TextSearchInput)
		if !ok {
			return fmt.Errorf("stuntgres: missing captured text parser input")
		}
		oid, err = input.ResolveTextParser(s.op.Context(), text)
	}
	if err != nil { return err }
	if _, ok := catalog.BuiltinTextParser(oid); !ok {
		return sqlError("XX000", fmt.Sprintf("cache lookup failed for text search parser %d", oid))
	}
	return nil
}
