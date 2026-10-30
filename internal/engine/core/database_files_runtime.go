package core

import (
 "fmt"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/pgcatalog"
 "stuntgres/internal/sqltype"
)

// DatabaseFilesInquiry answers catalog-only absence, never inventing physical storage.
func (h *statementHost) DatabaseFilesInquiry(kind functions.DatabaseFilesKind, arg, second sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
 s := h.scope()
 if err := s.Op.Context().Err(); err != nil { return sqltype.Value{}, err }
 e := s.Engine()
 e.Mu.Lock()
 defer e.Mu.Unlock()
 retained, err := h.catalogInquiryCatalog()
 if err != nil { return sqltype.Value{}, err }
 if retained.Epoch() != e.Catalog.State.Epoch() { return sqltype.Value{}, ids.ErrCatalogReplaced }
 if kind == functions.FilenodeRelation {
  if _, err := arg.OID(); err != nil { return sqltype.Value{}, err }
  if _, err := second.OID(); err != nil { return sqltype.Value{}, err }
  // No PostgreSQL physical mappings exist in this engine, including temporary ones.
  return sqltype.Null(result), nil
 }
 if kind != functions.RelationFilenode && kind != functions.RelationFilepath { return sqltype.Value{}, fmt.Errorf("stuntgres: invalid database files inquiry kind") }
 oid, err := arg.ClassOID()
 if err != nil { return sqltype.Value{}, err }
 view := e.PrivateCatalogLocked(s.PendingCatalog())
 relation, found := view.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)})
 if !found { return sqltype.Null(result), nil }
 switch pgcatalog.ClassKind(view, relation) {
 case "r", "i", "S", "t", "m":
  if kind == functions.RelationFilenode { return sqltype.Value{}, SQLError("0A000", "pg_relation_filenode is not supported: the embedded engine has no physical PostgreSQL relation filenode mapping") }
  return sqltype.Value{}, SQLError("0A000", "pg_relation_filepath is not supported: the embedded engine has no physical PostgreSQL relation file paths")
 default:
  return sqltype.Null(result), nil
 }
}
