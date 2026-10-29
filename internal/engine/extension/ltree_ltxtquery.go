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
	LtreeLtxtqueryProcedureStart = LtreeLqueryMemberEnd
	LtreeLtxtqueryOperatorStart  = LtreeLtxtqueryProcedureStart + int(sqltype.LtreeLtxtqueryOperationCount)
	LtreeMemberCount             = LtreeLtxtqueryOperatorStart + int(sqltype.LtreeLtxtqueryOperationCount)
)

var ltreeLtxtqueryRecipes = [...]struct {
	name, operator, left, right string
	result                      ids.OID
}{
	{"ltxtq_exec", "@", "ltree", "ltxtquery", 16},
	{"ltxtq_rexec", "@", "ltxtquery", "ltree", 16},
}

func LtreeLtxtqueryProcedureSymbol(op sqltype.LtreeLtxtqueryOperation) string {
	if op >= sqltype.LtreeLtxtqueryOperationCount {
		return ""
	}
	return ltreeLtxtqueryRecipes[op].name
}

func LtreeLtxtqueryProcedureName(name string) bool {
	for _, recipe := range ltreeLtxtqueryRecipes {
		if name == recipe.name {
			return true
		}
	}
	return false
}

func LtreeLtxtqueryProcedure(v catalog.Read, proc *catalog.Procedure) (sqltype.LtreeLtxtqueryOperation, bool) {
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
	index := proc.Origin.Declaration - LtreeLtxtqueryProcedureStart
	if index < 0 || index >= len(ltreeLtxtqueryRecipes) || symbol != ltreeLtxtqueryRecipes[index].name {
		return 0, false
	}
	return sqltype.LtreeLtxtqueryOperation(index), true
}

func ltreeLtxtqueryTypes(v catalog.Read, installation *catalog.Installation, index int) (*sqltype.Descriptor, *sqltype.Descriptor, error) {
	if index < 0 || index >= len(ltreeLtxtqueryRecipes) {
		return nil, nil, fmt.Errorf("invalid ltree ltxtquery role")
	}
	recipe := ltreeLtxtqueryRecipes[index]
	left, err := ltreeRoleType(v, installation, recipe.left)
	if err != nil {
		return nil, nil, err
	}
	right, err := ltreeRoleType(v, installation, recipe.right)
	if err != nil {
		return nil, nil, err
	}
	return left, right, nil
}

func (p *LtreeProvider) stageLtxtquery(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef, base, query *sqltype.Descriptor) error {
	for i, recipe := range ltreeLtxtqueryRecipes {
		name := recipe.name
		left, right := base, query
		if recipe.left == "ltxtquery" {
			left, right = right, left
		}
		signature := catalog.ProcedureSignature(fmt.Sprintf("%d,%d", left.ID(), right.ID()))
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		proc := &catalog.Procedure{ObjectAddress: ext.Members[LtreeLtxtqueryProcedureStart+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Signature: signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: recipe.result, Volatility: expr.Immutable, Strict: true, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name, Binary: "$libdir/ltree", HasBinary: true}, Implementation: p}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, name)}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	for i, recipe := range ltreeLtxtqueryRecipes {
		name := recipe.operator
		left, right := base, query
		if recipe.left == "ltxtquery" {
			left, right = right, left
		}
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		op := &catalog.Operator{ObjectAddress: ext.Members[LtreeLtxtqueryOperatorStart+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Owner: owner, Left: ids.OID(left.ID()), Right: ids.OID(right.ID()), Result: recipe.result, Function: ext.Members[LtreeLtxtqueryProcedureStart+i].ObjectID, Commutator: ext.Members[LtreeLtxtqueryOperatorStart+(i^1)].ObjectID, Restriction: 5040, Join: 5041}
		if stage.LookupName(catalog.DescriptorNameKey(op)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("operator %s already exists", name)}
		}
		if err := stage.Create(op); err != nil {
			return err
		}
	}
	return nil
}

func validateLtreeLtxtquery(v catalog.Read, ext *catalog.Extension, base *catalog.Type) error {
	query, ok := v.Type(ext.Members[6])
	if !ok || !query.SQLType.IsLtxtquery() {
		return fmt.Errorf("stuntgres: invalid ltxtquery role")
	}
	invalid := func() error { return fmt.Errorf("stuntgres: invalid ltree ltxtquery manifest") }
	for i, recipe := range ltreeLtxtqueryRecipes {
		name := recipe.operator
		left, right := base, query
		if recipe.left == "ltxtquery" {
			left, right = right, left
		}
		if ext.Members[LtreeLtxtqueryProcedureStart+i].ClassID != ids.ProcedureClassID || ext.Members[LtreeLtxtqueryOperatorStart+i].ClassID != ids.OperatorClassID {
			return invalid()
		}
		proc, ok := v.Procedure(ext.Members[LtreeLtxtqueryProcedureStart+i])
		if !ok {
			return invalid()
		}
		if _, err := ProcedureSymbol(v, proc); err != nil {
			return err
		}
		op, ok := v.Operator(ext.Members[LtreeLtxtqueryOperatorStart+i])
		if !ok || op.Name() != name || op.Namespace() != ext.TargetNamespace || op.Extension != ext.Address() || op.Left != ids.OID(left.SQLType.ID()) || op.Right != ids.OID(right.SQLType.ID()) || op.Result != 16 || op.Function != proc.Address().ObjectID || op.Commutator != ext.Members[LtreeLtxtqueryOperatorStart+(i^1)].ObjectID || op.Negator != 0 || op.Restriction != 5040 || op.Join != 5041 || op.Hash || op.Merge {
			return invalid()
		}
		if err := ValidateProviderHeader(v, op, ids.OperatorClassID); err != nil {
			return err
		}
	}
	return nil
}
