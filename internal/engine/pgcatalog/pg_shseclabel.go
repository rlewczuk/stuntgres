package pgcatalog

import (
 "fmt"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqltype"
)

const ShseclabelOID ids.OID = 3592

// No native security-label provider is loaded; there are no legitimate rows.
func capturePGShseclabel(in Input) ([][]sqltype.Value, error) {
 if err := in.Context.Err(); err != nil { return nil, err }
 view, source := in.Catalog, in.Source
 address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ShseclabelOID}
 if in.Owner == nil || view.Owner() != in.Owner || view.State() == nil || source == nil ||
  source.Address() != address || source.RelationKind != catalog.CatalogRelation ||
  in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != view.Epoch() ||
  in.Ref.Address != address || view.Get(address) != source || !in.Roles.BelongsTo(in.Owner.Principals) {
  return nil, fmt.Errorf("stuntgres: foreign shared security label source authority")
 }
 names := [...]string{"objoid", "classoid", "provider", "label"}
 if len(source.Columns) != len(names) { return nil, fmt.Errorf("stuntgres: invalid shared security label source shape") }
 for i, name := range names {
  id, collation := sqltype.Oid, uint32(0)
  if i >= 2 { id, collation = sqltype.Text, 950 }
  typ, ok := view.LookupType(id)
  c := source.Columns[i]
  if !ok || c.Name != name || c.Usage.Type != typ || c.Nullable || !c.NullabilityKnown || c.Usage.Typmod != -1 || c.DeclaredDimensions != 0 || c.Usage.Collation.ID != collation {
   return nil, fmt.Errorf("stuntgres: foreign shared security label source type")
  }
 }
 return nil, in.Context.Err()
}
