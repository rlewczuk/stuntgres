package extension

import (
 "fmt"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqltype"
)

var HstoreComparisonNames = [...]string{"hstore_eq", "hstore_ne", "hstore_lt", "hstore_le", "hstore_gt", "hstore_ge", "hstore_cmp", "hstore_hash", "hstore_hash_extended"}
var hstoreComparisonOperators = [...]string{"=", "<>", "#<#", "#<=#", "#>#", "#>=#"}

func HstoreComparisonName(name string) bool {
 for _, n := range HstoreComparisonNames { if name == n { return true } }
 return false
}

func HstoreComparisonDeclaration(kind int, typ sqltype.ID) (string, catalog.ProcedureSignature, ids.OID) {
 if kind < 0 || kind >= len(HstoreComparisonNames) { return "", "", 0 }
 signature := string(HstoreSignature(typ))
 result := ids.OID(sqltype.Bool)
 switch kind {
 case 7: result = ids.OID(sqltype.Int4)
 case 8: signature += ",20"; result = ids.OID(sqltype.Int8)
 default: signature += "," + signature; if kind == 6 { result = ids.OID(sqltype.Int4) }
 }
 return HstoreComparisonNames[kind], catalog.ProcedureSignature(signature), result
}

func hstoreMemberClass(slot int) ids.OID {
 switch {
 case slot == HstoreTypeSlot: return ids.TypeClassID
 case slot == HstoreArrayOperatorSlot || slot == HstoreMatrixOperatorSlot || slot >= HstoreComparisonOperatorStart && slot < HstoreBtreeFamilySlot: return ids.OperatorClassID
 case slot == HstoreJSONBCastSlot: return ids.CastClassID
 case slot == HstoreBtreeFamilySlot || slot == HstoreHashFamilySlot: return ids.OperatorFamilyClassID
 case slot == HstoreBtreeClassSlot || slot == HstoreHashClassSlot: return ids.OperatorClassCatalogID
 default: return ids.ProcedureClassID
 }
}

func validateHstoreComparisons(v catalog.Read, ext *catalog.Extension, base *catalog.Type) error {
 invalid := func() error { return fmt.Errorf("stuntgres: invalid hstore comparison manifest") }
 for i := range HstoreComparisonNames {
  p, ok := v.Procedure(ext.Members[HstoreComparisonProcedureStart+i]); if !ok { return invalid() }
  if _, err := ProcedureSymbol(v,p); err != nil { return err }
 }
 for i,name := range hstoreComparisonOperators {
  o,ok := v.Operator(ext.Members[HstoreComparisonOperatorStart+i])
  if !ok || o.Name()!=name || o.Namespace()!=ext.TargetNamespace || o.Extension!=ext.Address() || o.Left!=ids.OID(base.SQLType.ID()) || o.Right!=o.Left || o.Result!=16 || o.Function!=ext.Members[HstoreComparisonProcedureStart+i].ObjectID || o.Commutator!=ext.Members[HstoreComparisonOperatorStart+cubeCommutators[i]].ObjectID || o.Negator!=ext.Members[HstoreComparisonOperatorStart+cubeNegators[i]].ObjectID || o.Restriction!=cubeRestrictions[i] || o.Join!=cubeJoins[i] || o.Hash!=(i==0) || o.Merge!=(i==0) { return invalid() }
  if err:=ValidateProviderHeader(v,o,ids.OperatorClassID);err!=nil{return err}
 }
 for _, hash := range []bool{false,true} {
  familySlot,classSlot,method,name := HstoreBtreeFamilySlot,HstoreBtreeClassSlot,ids.OID(403),"btree_hstore_ops"
  strategies:=cubeStrategies[:]; supportKinds:=[]int{6}
  if hash { familySlot,classSlot,method,name=HstoreHashFamilySlot,HstoreHashClassSlot,405,"hash_hstore_ops"; strategies=[]int{0}; supportKinds=[]int{7,8} }
  f,ok:=v.OperatorFamily(ext.Members[familySlot]);if !ok || f.Name()!=name || f.Method!=method || f.Extension!=ext.Address() || f.Namespace()!=ext.TargetNamespace {return invalid()}
  if err:=ValidateProviderHeader(v,f,ids.OperatorFamilyClassID);err!=nil{return err}
  c,ok:=v.OperatorClass(ext.Members[classSlot].ObjectID);if !ok || c.Name()!=name || c.Method!=method || c.Family!=f.Address().ObjectID || c.InputType!=ids.OID(base.SQLType.ID()) || c.StorageType!=0 || !c.Default || c.Extension!=ext.Address() || c.Namespace()!=ext.TargetNamespace {return invalid()}
  if err:=ValidateProviderHeader(v,c,ids.OperatorClassCatalogID);err!=nil{return err}
  for i,op:=range strategies {m,ok:=v.FamilyOperator(c.Family,c.InputType,c.InputType,int16(i+1),'s');if !ok || m.Operator!=ext.Members[HstoreComparisonOperatorStart+op].ObjectID || m.Method!=method || m.SortFamily!=0 || m.InternalOwner!=c.Address() || m.Revision()==0 {return invalid()}}
  for i,kind:=range supportKinds {m,ok:=v.FamilySupport(c.Family,c.InputType,c.InputType,int16(i+1));if !ok || m.Function!=ext.Members[HstoreComparisonProcedureStart+kind].ObjectID || m.InternalOwner!=c.Address() || !m.CatalogVisible || m.Revision()==0 {return invalid()}}
 }
 return nil
}
