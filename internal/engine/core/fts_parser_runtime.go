package core

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fts"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) ResolveFTSParser(value sqltype.Value) (fts.ParserRuntime, error) {
	s := h.scope()
	if value.Type().ID() != sqltype.Oid {
		text, err := value.Text()
		if err != nil {
			return fts.ParserRuntime{}, err
		}
		input, ok := s.Input.RegTS.(regobj.TextSearchInput)
		if !ok {
			return fts.ParserRuntime{}, fmt.Errorf("stuntgres: missing captured text parser input")
		}
		return input.ResolveParserRuntime(s.Op.Context(), text)
	}
	oid, err := value.OID()
	if err != nil {
		return fts.ParserRuntime{}, err
	}
	e := s.Engine()
	e.Mu.Lock()
	changes := s.Catalog
	if changes == nil {
		changes = s.Owner.Catalog
	}
	read := e.PrivateCatalogLocked(changes)
	d, ok := read.TSParser(ids.ObjectAddress{ClassID: ids.TSParserClassID, ObjectID: ids.OID(oid)})
	var runtime fts.ParserRuntime
	if ok {
		runtime, err = catalog.ResolveParserRuntime(read, d)
	}
	e.Mu.Unlock()
	if !ok {
		return fts.ParserRuntime{}, SQLError("XX000", fmt.Sprintf("cache lookup failed for text search parser %d", oid))
	}
	return runtime, err
}
