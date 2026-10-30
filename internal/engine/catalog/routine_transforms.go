package catalog

import (
 "fmt"
 "slices"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqltype"
)

// RoutineTransform binds an ordered declaration entry to its genuine pair.
// The type and revision are captured catalog authority, not textual selectors.
type RoutineTransform struct {
 Type *sqltype.Descriptor
 Pair ids.ObjectAddress
 Revision ids.DefinitionRevision
}
type RoutineTransforms struct { entries []RoutineTransform }
func NewRoutineTransforms(entries []RoutineTransform) *RoutineTransforms {
 if len(entries)==0 {return nil}
 return &RoutineTransforms{entries:slices.Clone(entries)}
}
func (r *RoutineTransforms) Len() int {if r==nil{return 0};return len(r.entries)}
func (r *RoutineTransforms) At(i int) RoutineTransform {return r.entries[i]}
func (r *RoutineTransforms) PairFor(typ *sqltype.Descriptor) ids.ObjectAddress {
 if r!=nil {for _,entry:=range r.entries {if entry.Type==typ{return entry.Pair}}}
 return ids.ObjectAddress{}
}
func ValidateRoutineTransforms(v Read,d *Procedure) error {
 if d.Transforms==nil {
  // Released display-only declarations have their own provider validation.
  // A dynamic routine must never project an association it does not own.
  if d.RoutineDefinition()!=nil&&d.Display.TransformTypes!=nil{return fmt.Errorf("stuntgres: missing routine transform association")}
  return nil
 }
 if d.Transforms.Len()!=d.Display.TransformTypes.Len(){return fmt.Errorf("stuntgres: invalid routine transform display")}
 for i,entry:=range d.Transforms.entries {
  pair,ok:=v.Get(entry.Pair).(*Transform)
  if !ok||entry.Type==nil||pair.SQLType!=entry.Type||pair.Language!=d.Language||pair.Revision()!=entry.Revision||ids.OID(entry.Type.ID())!=d.Display.TransformTypes.At(i)||(TransformProvider{}).Validate(v,pair)!=nil {
   return fmt.Errorf("stuntgres: invalid routine transform association")
  }
 }
 return nil
}
