package extension

import (
 "fmt"
 "stuntgres/internal/engine/authorization"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/sqltype"
)

const (
 LtreeLqueryProcedureStart = LtreePathMemberEnd
 LtreeLqueryOperatorStart = LtreeLqueryProcedureStart + int(sqltype.LtreeLqueryOperationCount)
 LtreeLqueryMemberEnd = LtreeLqueryOperatorStart + int(sqltype.LtreeLqueryOperationCount)
)

var ltreeLqueryNames = [...]string{"ltq_regex", "ltq_rregex"}

func LtreeLqueryProcedureSymbol(op sqltype.LtreeLqueryOperation) string {
 if op >= sqltype.LtreeLqueryOperationCount { return "" }
 return ltreeLqueryNames[op]
}
func LtreeLqueryProcedureName(name string) bool {
 for _, candidate := range ltreeLqueryNames { if candidate == name { return true } }
 return false
}
func LtreeLqueryProcedure(v catalog.Read, proc *catalog.Procedure) (sqltype.LtreeLqueryOperation, bool) {
 if proc == nil { return 0, false }
 ext, ok := v.Extension(proc.Extension)
 if !ok || len(ext.Members) != LtreeMemberCount { return 0, false }
 for i := range ltreeLqueryNames {
  if ext.Members[LtreeLqueryProcedureStart+i] == proc.Address() { return sqltype.LtreeLqueryOperation(i), true }
 }
 return 0, false
}

func ltreeLqueryTypes(v catalog.Read, ext *catalog.Extension, index int) (*sqltype.Descriptor, *sqltype.Descriptor, error) {
 path, ok := v.Type(ext.Members[3]); if !ok || path.Extension != ext.Address() || !path.SQLType.IsLtree() { return nil, nil, fmt.Errorf("invalid ltree lquery path type") }
 query, ok := v.Type(ext.Members[0]); if !ok || query.Extension != ext.Address() || !query.SQLType.IsLquery() { return nil, nil, fmt.Errorf("invalid ltree lquery query type") }
 if err := ValidateProviderHeader(v, path, ids.TypeClassID); err != nil { return nil, nil, err }
 if err := ValidateProviderHeader(v, query, ids.TypeClassID); err != nil { return nil, nil, err }
 left, right := path.SQLType, query.SQLType
 if index == 1 { left, right = right, left }
 return left, right, nil
}

func (p *LtreeProvider) stageLquery(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef, path, query *sqltype.Descriptor) error {
 for i, name := range ltreeLqueryNames {
  left, right := path, query; if i == 1 { left, right = right, left }
  revision, err := stage.Revision(); if err != nil { return err }
  proc := &catalog.Procedure{ObjectAddress: ext.Members[LtreeLqueryProcedureStart+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Signature: catalog.ProcedureSignature(fmt.Sprintf("%d,%d", left.ID(), right.ID())), RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: 16, Volatility: expr.Immutable, Strict: true, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name, Binary: "$libdir/ltree", HasBinary: true}, Implementation: p}
  if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) { return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, name)} }
  if err := stage.Create(proc); err != nil { return err }
 }
 for i := range ltreeLqueryNames {
  left, right := path, query; if i == 1 { left, right = right, left }
  revision, err := stage.Revision(); if err != nil { return err }
  op := &catalog.Operator{ObjectAddress: ext.Members[LtreeLqueryOperatorStart+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: "~", Owner: owner, Left: ids.OID(left.ID()), Right: ids.OID(right.ID()), Result: 16, Function: ext.Members[LtreeLqueryProcedureStart+i].ObjectID, Commutator: ext.Members[LtreeLqueryOperatorStart+(i^1)].ObjectID, Restriction: 5040, Join: 5041}
  if stage.LookupName(catalog.DescriptorNameKey(op)) != (ids.ObjectAddress{}) { return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: "operator ~ already exists"} }
  if err := stage.Create(op); err != nil { return err }
 }
 return nil
}

func validateLtreeLquery(v catalog.Read, ext *catalog.Extension) error {
 invalid := func() error { return fmt.Errorf("stuntgres: invalid ltree lquery manifest") }
 for i := range ltreeLqueryNames {
  if ext.Members[LtreeLqueryProcedureStart+i].ClassID != ids.ProcedureClassID || ext.Members[LtreeLqueryOperatorStart+i].ClassID != ids.OperatorClassID { return invalid() }
  proc, ok := v.Procedure(ext.Members[LtreeLqueryProcedureStart+i]); if !ok { return invalid() }
  if _, err := ProcedureSymbol(v, proc); err != nil { return err }
  left, right, err := ltreeLqueryTypes(v, ext, i); if err != nil { return err }
  op, ok := v.Operator(ext.Members[LtreeLqueryOperatorStart+i])
  if !ok || op.Name() != "~" || op.Namespace() != ext.TargetNamespace || op.Extension != ext.Address() || op.Left != ids.OID(left.ID()) || op.Right != ids.OID(right.ID()) || op.Result != 16 || op.Function != proc.Address().ObjectID || op.Commutator != ext.Members[LtreeLqueryOperatorStart+(i^1)].ObjectID || op.Negator != 0 || op.Restriction != 5040 || op.Join != 5041 || op.Hash || op.Merge { return invalid() }
  if err := ValidateProviderHeader(v, op, ids.OperatorClassID); err != nil { return err }
 }
 return nil
}
