package core

import (
	"fmt"

	"stuntgres/internal/engine/extension"
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
	e := s.Engine()
	e.Mu.Lock()
	changes := s.Catalog
	if changes == nil {
		changes = s.Owner.Catalog
	}
	read := e.PrivateCatalogLocked(changes)
	d, ok := read.TSDictionary(ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: ids.OID(oid)})
	if ok {
		err = (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(read, d)
	}
	e.Mu.Unlock()
	if !ok {
		return sqltype.Value{}, SQLError("XX000", fmt.Sprintf("cache lookup failed for text search dictionary %d", oid))
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	return d.Runtime.Lexize(s.Op.Context(), text, result)
}
