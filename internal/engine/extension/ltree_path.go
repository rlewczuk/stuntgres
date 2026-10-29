package extension

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

const (
	LtreePathProcedureStart = LtreeHierarchyMemberEnd
	LtreePathOperatorStart  = LtreePathProcedureStart + int(sqltype.LtreePathOperationCount)
	LtreePathMemberEnd      = LtreePathOperatorStart + 3
)

type ltreePathRecipe struct{ name, symbol, signature, result string }

var ltreePathRecipes = [...]ltreePathRecipe{
	{"subltree", "subltree", "ltree,23,23", "ltree"},
	{"subpath", "subpath", "ltree,23,23", "ltree"},
	{"subpath", "subpath", "ltree,23", "ltree"},
	{"index", "ltree_index", "ltree,ltree", "23"},
	{"index", "ltree_index", "ltree,ltree,23", "23"},
	{"nlevel", "nlevel", "ltree", "23"},
	{"ltree2text", "ltree2text", "ltree", "25"},
	{"text2ltree", "text2ltree", "25", "ltree"},
	{"lca", "_lca", "ltree[]", "ltree"},
	{"lca", "lca", "ltree,ltree", "ltree"},
	{"lca", "lca", "ltree,ltree,ltree", "ltree"},
	{"lca", "lca", "ltree,ltree,ltree,ltree", "ltree"},
	{"lca", "lca", "ltree,ltree,ltree,ltree,ltree", "ltree"},
	{"lca", "lca", "ltree,ltree,ltree,ltree,ltree,ltree", "ltree"},
	{"lca", "lca", "ltree,ltree,ltree,ltree,ltree,ltree,ltree", "ltree"},
	{"lca", "lca", "ltree,ltree,ltree,ltree,ltree,ltree,ltree,ltree", "ltree"},
	{"ltree_addltree", "ltree_addltree", "ltree,ltree", "ltree"},
	{"ltree_addtext", "ltree_addtext", "ltree,25", "ltree"},
	{"ltree_textadd", "ltree_textadd", "25,ltree", "ltree"},
}

func LtreePathProcedureSymbol(op sqltype.LtreePathOperation) string {
	if op >= sqltype.LtreePathOperationCount {
		return ""
	}
	return ltreePathRecipes[op].symbol
}
func LtreePathProcedureName(name string) bool {
	for _, recipe := range ltreePathRecipes {
		if recipe.name == name {
			return true
		}
	}
	return false
}
func LtreePathProcedure(v catalog.Read, proc *catalog.Procedure) (sqltype.LtreePathOperation, bool) {
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
	index := proc.Origin.Declaration - LtreePathProcedureStart
	if index < 0 || index >= len(ltreePathRecipes) || symbol != ltreePathRecipes[index].symbol {
		return 0, false
	}
	return sqltype.LtreePathOperation(index), true
}
func ltreePathSignatureWith(resolve func(string) (*sqltype.Descriptor, error), index int) (catalog.ProcedureSignature, ids.OID, error) {
	if index < 0 || index >= len(ltreePathRecipes) {
		return "", 0, fmt.Errorf("invalid ltree path role")
	}
	recipe := ltreePathRecipes[index]
	roles := strings.Split(recipe.signature, ",")
	var formals [8]*sqltype.Descriptor
	var signature strings.Builder
	for i, role := range roles {
		typ, err := resolve(role)
		if err != nil {
			return "", 0, err
		}
		formals[i] = typ
		if i != 0 {
			signature.WriteByte(',')
		}
		fmt.Fprintf(&signature, "%d", typ.ID())
	}
	result, err := resolve(recipe.result)
	if err != nil {
		return "", 0, err
	}
	if err := sqltype.LtreePathOperation(index).Validate(formals, len(roles), result); err != nil {
		return "", 0, err
	}
	return catalog.ProcedureSignature(signature.String()), ids.OID(result.ID()), nil
}
func ltreePathSignature(v catalog.Read, installation *catalog.Installation, index int) (catalog.ProcedureSignature, ids.OID, error) {
	return ltreePathSignatureWith(func(role string) (*sqltype.Descriptor, error) { return ltreeRoleType(v, installation, role) }, index)
}
func (p *LtreeProvider) stagePaths(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef, resolve func(string) (*sqltype.Descriptor, error)) error {
	for i, recipe := range ltreePathRecipes {
		signature, result, err := ltreePathSignatureWith(resolve, i)
		if err != nil {
			return err
		}
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		proc := &catalog.Procedure{ObjectAddress: ext.Members[LtreePathProcedureStart+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: recipe.name, Signature: signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: result, Volatility: expr.Immutable, Strict: true, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: recipe.symbol, Binary: "$libdir/ltree", HasBinary: true}, Implementation: p}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, recipe.name)}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	for i := range 3 {
		index := int(sqltype.LtreeConcat) + i
		roles := strings.Split(ltreePathRecipes[index].signature, ",")
		left, err := resolve(roles[0])
		if err != nil {
			return err
		}
		right, err := resolve(roles[1])
		if err != nil {
			return err
		}
		base, err := resolve("ltree")
		if err != nil {
			return err
		}
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		op := &catalog.Operator{ObjectAddress: ext.Members[LtreePathOperatorStart+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: "||", Owner: owner, Left: ids.OID(left.ID()), Right: ids.OID(right.ID()), Result: ids.OID(base.ID()), Function: ext.Members[LtreePathProcedureStart+index].ObjectID}
		if stage.LookupName(catalog.DescriptorNameKey(op)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: "operator || already exists"}
		}
		if err := stage.Create(op); err != nil {
			return err
		}
	}
	return nil
}
func validateLtreePaths(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid ltree path manifest") }
	for i := range ltreePathRecipes {
		if ext.Members[LtreePathProcedureStart+i].ClassID != ids.ProcedureClassID {
			return invalid()
		}
		proc, ok := v.Procedure(ext.Members[LtreePathProcedureStart+i])
		if !ok {
			return invalid()
		}
		if _, err := ProcedureSymbol(v, proc); err != nil {
			return err
		}
	}
	for i := range 3 {
		if err := validateLtreePathOperator(v, ext, i); err != nil {
			return err
		}
	}
	return nil
}

func validateLtreePathOperator(v catalog.Read, ext *catalog.Extension, ordinal int) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid ltree path operator") }
	index := int(sqltype.LtreeConcat) + ordinal
	address := ext.Members[LtreePathOperatorStart+ordinal]
	op, ok := v.Operator(address)
	if !ok || address.ClassID != ids.OperatorClassID {
		return invalid()
	}
	procedure, ok := v.Procedure(ext.Members[LtreePathProcedureStart+index])
	if !ok {
		return invalid()
	}
	if _, err := ProcedureSymbol(v, procedure); err != nil {
		return err
	}
	roles := strings.Split(ltreePathRecipes[index].signature, ",")
	left, err := ltreeRoleType(v, ext.Installation, roles[0])
	if err != nil {
		return err
	}
	right, err := ltreeRoleType(v, ext.Installation, roles[1])
	if err != nil {
		return err
	}
	_, result, err := ltreePathSignature(v, ext.Installation, index)
	if err != nil {
		return err
	}
	if err := ValidateProviderHeader(v, op, ids.OperatorClassID); err != nil {
		return err
	}
	if op.Extension != ext.Address() || op.Namespace() != ext.TargetNamespace || op.Name() != "||" || op.Left != ids.OID(left.ID()) || op.Right != ids.OID(right.ID()) || op.Result != result || op.Function != procedure.Address().ObjectID || op.Commutator != 0 || op.Negator != 0 || op.Restriction != 0 || op.Join != 0 || op.Hash || op.Merge {
		return invalid()
	}
	return nil
}
