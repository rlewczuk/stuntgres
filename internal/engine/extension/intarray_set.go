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

type intarraySetRecipe struct {
	name, operator string
	right, result  ids.OID
	commutator     int // index in the selected operator range; -1 means absent
}

var intarraySetRecipes = [...]intarraySetRecipe{
	{"_int_overlap", "&&", 1007, 16, 0},
	{"_int_contains", "@>", 1007, 16, 2},
	{"_int_contained", "<@", 1007, 16, 1},
	{"intarray_push_elem", "+", 23, 1007, -1},
	{"intarray_push_array", "+", 1007, 1007, 4},
	{"intarray_del_elem", "-", 23, 1007, -1},
	{"intset_subtract", "-", 1007, 1007, -1},
	{"intset_union_elem", "|", 23, 1007, -1},
	{"_int_union", "|", 1007, 1007, 8},
	{"_int_inter", "&", 1007, 1007, 9},
}
var intarrayEstimatorNames = [...]string{"_int_overlap_sel", "_int_contains_sel", "_int_contained_sel", "_int_overlap_joinsel", "_int_contains_joinsel", "_int_contained_joinsel"}

func intarrayProcedureDeclaration(slot int) (string, catalog.ProcedureSignature, ids.OID, expr.Volatility) {
	if slot >= IntarraySetProcedureStart && slot < IntarrayEstimatorStart {
		r := intarraySetRecipes[slot-IntarraySetProcedureStart]
		return r.name, catalog.ProcedureSignature(fmt.Sprintf("1007,%d", r.right)), r.result, expr.Immutable
	}
	signature := catalog.ProcedureSignature("2281,26,2281,23")
	if slot >= IntarrayEstimatorStart+3 {
		signature = "2281,26,2281,21,2281"
	}
	return intarrayEstimatorNames[slot-IntarrayEstimatorStart], signature, 701, expr.Stable
}

func IntarrayProcedureName(name string) bool {
	for _, r := range intarraySetRecipes {
		if r.name == name {
			return true
		}
	}
	for _, estimator := range intarrayEstimatorNames {
		if estimator == name {
			return true
		}
	}
	return false
}

func IntarraySetProcedureSymbol(operation sqltype.IntarraySetOperation) string {
	if operation >= sqltype.IntarraySetOperationCount {
		return ""
	}
	return intarraySetRecipes[operation].name
}

// IntarraySetProcedure selects only authenticated original executable roles.
func IntarraySetProcedure(v catalog.Read, p *catalog.Procedure) (sqltype.IntarraySetOperation, bool) {
	if p == nil || p.Implementation != StockIntarray || p.Origin == nil {
		return 0, false
	}
	slot := p.Origin.Declaration
	if slot < IntarraySetProcedureStart || slot >= IntarrayEstimatorStart {
		return 0, false
	}
	if _, err := ProcedureSymbol(v, p); err != nil {
		return 0, false
	}
	return sqltype.IntarraySetOperation(slot - IntarraySetProcedureStart), true
}

func (p *IntarrayProvider) stageSet(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	for slot := IntarraySetProcedureStart; slot < IntarraySetOperatorStart; slot++ {
		name, signature, result, volatility := intarrayProcedureDeclaration(slot)
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		proc := &catalog.Procedure{ObjectAddress: ext.Members[slot], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Signature: signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: result, Volatility: volatility, Strict: true, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name, Binary: "$libdir/_int", HasBinary: true}, Implementation: p}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, name), Context: intarraySetProcedureContexts[slot-IntarraySetProcedureStart]}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	for i, r := range intarraySetRecipes {
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		op := &catalog.Operator{ObjectAddress: ext.Members[IntarraySetOperatorStart+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: r.operator, Owner: owner, Left: 1007, Right: r.right, Result: r.result, Function: ext.Members[IntarraySetProcedureStart+i].ObjectID}
		if r.commutator >= 0 {
			op.Commutator = ext.Members[IntarraySetOperatorStart+r.commutator].ObjectID
		}
		if i < 3 {
			op.Restriction = ext.Members[IntarrayEstimatorStart+i].ObjectID
			op.Join = ext.Members[IntarrayEstimatorStart+3+i].ObjectID
		}
		if stage.LookupName(catalog.DescriptorNameKey(op)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("operator %s already exists", r.operator), Context: intarraySetOperatorContexts[i]}
		}
		if err := stage.Create(op); err != nil {
			return err
		}
	}
	return nil
}
