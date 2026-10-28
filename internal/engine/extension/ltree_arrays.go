package extension

import (
 "fmt"
 "strings"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqltype"
)

const LtreeArrayProcedureStart = 9
const LtreeArrayOperatorStart = LtreeArrayProcedureStart + int(sqltype.LtreeArrayOperationCount)
const LtreeMemberCount = LtreeArrayOperatorStart + int(sqltype.LtreeArrayOperationCount)

type ltreeArrayRecipe struct { name, operator, left, right, result string }
var ltreeArrayRecipes = [...]ltreeArrayRecipe{
 {"_ltree_isparent", "@>", "ltree[]", "ltree", "16"},
 {"_ltree_r_isparent", "<@", "ltree", "ltree[]", "16"},
 {"_ltree_risparent", "<@", "ltree[]", "ltree", "16"},
 {"_ltree_r_risparent", "@>", "ltree", "ltree[]", "16"},
 {"_ltq_regex", "~", "ltree[]", "lquery", "16"},
 {"_ltq_rregex", "~", "lquery", "ltree[]", "16"},
 {"lt_q_regex", "?", "ltree", "lquery[]", "16"},
 {"lt_q_rregex", "?", "lquery[]", "ltree", "16"},
 {"_lt_q_regex", "?", "ltree[]", "lquery[]", "16"},
 {"_lt_q_rregex", "?", "lquery[]", "ltree[]", "16"},
 {"_ltxtq_exec", "@", "ltree[]", "ltxtquery", "16"},
 {"_ltxtq_rexec", "@", "ltxtquery", "ltree[]", "16"},
 {"_ltree_extract_isparent", "?@>", "ltree[]", "ltree", "ltree"},
 {"_ltree_extract_risparent", "?<@", "ltree[]", "ltree", "ltree"},
}

func LtreeArrayProcedureSymbol(operation sqltype.LtreeArrayOperation) string {
	if operation >= sqltype.LtreeArrayOperationCount { return "" }
	return ltreeArrayRecipes[operation].name
}

// LtreeArrayProcedureName is a discovery hint only; binding still resolves the
// current catalog declaration and validates its installed role.
func LtreeArrayProcedureName(name string) bool {
	for _, recipe := range ltreeArrayRecipes {
		if recipe.name == name {
			return true
		}
	}
	return false
}

func ltreeMemberClass(slot int) ids.OID {
 if slot < LtreeArrayProcedureStart && slot%3 == 0 { return ids.TypeClassID }
 if slot >= LtreeArrayOperatorStart { return ids.OperatorClassID }
 return ids.ProcedureClassID
}

func ltreeRoleType(v catalog.Read, ext *catalog.Extension, role string) (*sqltype.Descriptor, error) {
 if role == "16" { typ, ok := v.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 16}); if ok { return typ.SQLType, nil }; return nil, fmt.Errorf("missing bool type") }
 scalar := strings.TrimSuffix(role, "[]")
 for _, recipe := range ltreeRecipes {
  if recipe.name != scalar { continue }
  typ, ok := v.Type(ext.Members[recipe.slot])
  if !ok || !recipe.accept(typ.SQLType) || typ.Extension != ext.Address() { break }
  if role == scalar { return typ.SQLType, nil }
  array, ok := v.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.Array)})
  if !ok || !array.SQLType.IsArray() || !array.SQLType.Concrete() || array.SQLType.Element() != typ.SQLType { break }
  return array.SQLType, nil
 }
 return nil, fmt.Errorf("invalid ltree symbolic type %s", role)
}

func ltreeArraySignature(v catalog.Read, ext *catalog.Extension, index int) (catalog.ProcedureSignature, ids.OID, error) {
 return ltreeArraySignatureWith(func(role string) (*sqltype.Descriptor, error) { return ltreeRoleType(v, ext, role) }, index)
}

// Staging resolves its privately constructed descriptors; current identities
// resolve the same recipe through their validated catalog roles.
func ltreeArraySignatureWith(resolve func(string) (*sqltype.Descriptor, error), index int) (catalog.ProcedureSignature, ids.OID, error) {
 if index < 0 || index >= len(ltreeArrayRecipes) { return "", 0, fmt.Errorf("invalid ltree array role") }
 recipe := ltreeArrayRecipes[index]
 left, err := resolve(recipe.left); if err != nil { return "", 0, err }
 right, err := resolve(recipe.right); if err != nil { return "", 0, err }
 result, err := resolve(recipe.result); if err != nil { return "", 0, err }
 if err := sqltype.LtreeArrayOperation(index).Validate(left, right, result); err != nil { return "", 0, err }
 return catalog.ProcedureSignature(fmt.Sprintf("%d,%d", left.ID(), right.ID())), ids.OID(result.ID()), nil
}

func LtreeArrayProcedure(v catalog.Read, proc *catalog.Procedure) (sqltype.LtreeArrayOperation, bool) {
 if proc == nil { return 0, false }
 ext, ok := v.Extension(proc.Extension); if !ok || len(ext.Members) != LtreeMemberCount { return 0, false }
 for index := range ltreeArrayRecipes { if ext.Members[LtreeArrayProcedureStart+index] == proc.Address() { return sqltype.LtreeArrayOperation(index), true } }
 return 0, false
}

func validateLtreeArrayOperator(v catalog.Read, ext *catalog.Extension, index int) error {
 invalid := func() error { return fmt.Errorf("stuntgres: invalid ltree array operator") }
 recipe := ltreeArrayRecipes[index]
 op, ok := v.Operator(ext.Members[LtreeArrayOperatorStart+index]); if !ok { return invalid() }
 left, err := ltreeRoleType(v, ext, recipe.left); if err != nil { return err }
 right, err := ltreeRoleType(v, ext, recipe.right); if err != nil { return err }
 _, result, err := ltreeArraySignature(v, ext, index); if err != nil { return err }
 var commutator, restriction, join ids.OID
 if index < int(sqltype.LtreeArrayFirstAncestor) { commutator = ext.Members[LtreeArrayOperatorStart+(index^1)].ObjectID; restriction, join = 5040, 5041 }
 if err := ValidateProviderHeader(v, op, ids.OperatorClassID); err != nil { return err }
 if op.Extension != ext.Address() || op.Left != ids.OID(left.ID()) || op.Right != ids.OID(right.ID()) || op.Result != result || op.Function != ext.Members[LtreeArrayProcedureStart+index].ObjectID || op.Commutator != commutator || op.Negator != 0 || op.Restriction != restriction || op.Join != join || op.Hash || op.Merge || op.Name() != recipe.operator { return invalid() }
 return nil
}
