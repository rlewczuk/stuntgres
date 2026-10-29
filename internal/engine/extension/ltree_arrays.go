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

type ltreeArrayRecipe struct{ name, operator, left, right, result string }

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
	{"_ltq_extract_regex", "?~", "ltree[]", "lquery", "ltree"},
	{"_ltxtq_extract_exec", "?@", "ltree[]", "ltxtquery", "ltree"},
}

func LtreeArrayProcedureSymbol(operation sqltype.LtreeArrayOperation) string {
	if operation >= sqltype.LtreeArrayOperationCount {
		return ""
	}
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
	if slot >= LtreeHierarchyOperatorStart && slot < LtreeHierarchyMemberEnd || slot >= LtreePathOperatorStart && slot < LtreePathMemberEnd || slot >= LtreeLqueryOperatorStart && slot < LtreeLqueryMemberEnd || slot >= LtreeLtxtqueryOperatorStart && slot < LtreeMemberCount {
		return ids.OperatorClassID
	}
	if slot < LtreeArrayProcedureStart && slot%3 == 0 {
		return ids.TypeClassID
	}
	if slot >= LtreeArrayOperatorStart && slot < LtreeComparisonProcedureStart || slot >= LtreeComparisonOperatorStart && slot < LtreeBtreeFamilySlot {
		return ids.OperatorClassID
	}
	if slot == LtreeBtreeFamilySlot || slot == LtreeHashFamilySlot {
		return ids.OperatorFamilyClassID
	}
	if slot == LtreeBtreeClassSlot || slot == LtreeHashClassSlot {
		return ids.OperatorClassCatalogID
	}
	return ids.ProcedureClassID
}

// Roles come from the sealed released recipe, never current e-membership.
func ltreeRoleType(v catalog.Read, installation *catalog.Installation, role string) (*sqltype.Descriptor, error) {
	if role == "16" {
		typ, ok := v.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 16})
		if ok {
			return typ.SQLType, nil
		}
		return nil, fmt.Errorf("missing bool type")
	}
	if role == "23" || role == "25" {
		oid := ids.OID(23)
		if role == "25" {
			oid = 25
		}
		typ, ok := v.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: oid})
		if ok {
			return typ.SQLType, nil
		}
		return nil, fmt.Errorf("missing ltree path builtin type")
	}
	if !installation.Valid() || installation.Implementation != StockLtree || len(installation.Members) != LtreeMemberCount {
		return nil, InvalidProviderDescriptor()
	}
	scalar := strings.TrimSuffix(role, "[]")
	for _, recipe := range ltreeRecipes {
		if recipe.name != scalar {
			continue
		}
		typ, ok := v.Type(installation.Members[recipe.slot])
		if !ok || !recipe.accept(typ.SQLType) || !catalog.SameInstallation(typ, installation) {
			break
		}
		if err := validateRegisteredType(v, typ); err != nil {
			return nil, err
		}
		if role == scalar {
			return typ.SQLType, nil
		}
		array, ok := v.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.Array)})
		if !ok || array.SQLType == nil || !array.SQLType.IsArray() || array.SQLType.Concrete() != (recipe.slot != 6) || array.SQLType.Element() != typ.SQLType {
			break
		}
		if err := validateRegisteredType(v, array); err != nil {
			return nil, err
		}
		return array.SQLType, nil
	}
	return nil, fmt.Errorf("invalid ltree symbolic type %s", role)
}

func ltreeArraySignature(v catalog.Read, installation *catalog.Installation, index int) (catalog.ProcedureSignature, ids.OID, error) {
	return ltreeArraySignatureWith(func(role string) (*sqltype.Descriptor, error) { return ltreeRoleType(v, installation, role) }, index)
}

// Staging resolves its privately constructed descriptors; current identities
// resolve the same recipe through their validated catalog roles.
func ltreeArraySignatureWith(resolve func(string) (*sqltype.Descriptor, error), index int) (catalog.ProcedureSignature, ids.OID, error) {
	if index < 0 || index >= len(ltreeArrayRecipes) {
		return "", 0, fmt.Errorf("invalid ltree array role")
	}
	recipe := ltreeArrayRecipes[index]
	left, err := resolve(recipe.left)
	if err != nil {
		return "", 0, err
	}
	right, err := resolve(recipe.right)
	if err != nil {
		return "", 0, err
	}
	result, err := resolve(recipe.result)
	if err != nil {
		return "", 0, err
	}
	if err := sqltype.LtreeArrayOperation(index).Validate(left, right, result); err != nil {
		return "", 0, err
	}
	return catalog.ProcedureSignature(fmt.Sprintf("%d,%d", left.ID(), right.ID())), ids.OID(result.ID()), nil
}

func LtreeArrayProcedure(v catalog.Read, proc *catalog.Procedure) (sqltype.LtreeArrayOperation, bool) {
	if proc == nil || proc.Implementation != StockLtree {
		return 0, false
	}
	symbol, err := ProcedureSymbol(v, proc)
	if err != nil {
		return 0, false
	}
	installation, ok := catalog.OriginInstallation(proc)
	if !ok || len(installation.Members) != LtreeMemberCount {
		return 0, false
	}
	index := proc.Origin.Declaration - LtreeArrayProcedureStart
	if index < 0 || index >= len(ltreeArrayRecipes) || symbol != ltreeArrayRecipes[index].name {
		return 0, false
	}
	return sqltype.LtreeArrayOperation(index), true
}

func validateLtreeArrayOperator(v catalog.Read, installation *catalog.Installation, index int) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid ltree array operator") }
	if !installation.Valid() || installation.Implementation != StockLtree || len(installation.Members) != LtreeMemberCount || index < 0 || index >= len(ltreeArrayRecipes) {
		return invalid()
	}
	recipe := ltreeArrayRecipes[index]
	canonical, ok := installation.Declarations[LtreeArrayOperatorStart+index].(*catalog.Operator)
	if !ok || canonical.Name() != recipe.operator {
		return invalid()
	}
	op, ok := v.Operator(installation.Members[LtreeArrayOperatorStart+index])
	if !ok || !catalog.SameInstallation(op, installation) {
		return invalid()
	}
	left, err := ltreeRoleType(v, installation, recipe.left)
	if err != nil {
		return err
	}
	right, err := ltreeRoleType(v, installation, recipe.right)
	if err != nil {
		return err
	}
	_, result, err := ltreeArraySignature(v, installation, index)
	if err != nil {
		return err
	}
	var commutator, restriction, join ids.OID
	if index < int(sqltype.LtreeArrayFirstAncestor) {
		commutator = installation.Members[LtreeArrayOperatorStart+(index^1)].ObjectID
		restriction, join = 5040, 5041
	}
	if err := ValidateProviderHeader(v, op, ids.OperatorClassID); err != nil {
		return err
	}
	if err := catalog.ValidateInstalledClassOrigin(v, op); err != nil {
		return err
	}
	if op.Left != ids.OID(left.ID()) || op.Right != ids.OID(right.ID()) || op.Result != result || op.Function != installation.Members[LtreeArrayProcedureStart+index].ObjectID || op.Commutator != commutator || op.Negator != 0 || op.Restriction != restriction || op.Join != join || op.Hash || op.Merge {
		return invalid()
	}
	return nil
}
