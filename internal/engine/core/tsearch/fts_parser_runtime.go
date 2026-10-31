package tsearch

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fts"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

// ResolveParser is the parser a ts_parse or ts_token_type argument names.
func ResolveParser[A Authority[A]](h Host[A], value sqltype.Value) (fts.ParserRuntime, error) {
	if value.Type().ID() != sqltype.Oid {
		text, err := value.Text()
		if err != nil {
			return fts.ParserRuntime{}, err
		}
		in, _ := h.Environments()
		input, ok := in.RegTS.(regobj.TextSearchInput)
		if !ok {
			return fts.ParserRuntime{}, fmt.Errorf("stuntgres: missing captured text parser input")
		}
		return input.ResolveParserRuntime(h.Operation().Context(), text)
	}
	oid, err := value.OID()
	if err != nil {
		return fts.ParserRuntime{}, err
	}
	h.ReacquireEngine()
	read := h.PrivateCatalogLocked(h.PendingCatalog())
	d, ok := read.TSParser(ids.ObjectAddress{ClassID: ids.TSParserClassID, ObjectID: ids.OID(oid)})
	var runtime fts.ParserRuntime
	if ok {
		runtime, err = catalog.ResolveParserRuntime(read, d)
	}
	h.ReleaseEngineLocked()
	if !ok {
		return fts.ParserRuntime{}, sqlError("XX000", fmt.Sprintf("cache lookup failed for text search parser %d", oid))
	}
	return runtime, err
}
