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

type intarraySliceRecipe struct {
	name string
	signature catalog.ProcedureSignature
	result ids.OID
	statement string
	line int
}

var intarraySliceRecipes = [...]intarraySliceRecipe{
	{"icount", "1007", 23, "icount(_int4)\nRETURNS int4", 233},
	{"idx", "1007,23", 23, "idx(_int4, int4)\nRETURNS int4", 268},
	{"subarray", "1007,23", 1007, "subarray(_int4, int4)\nRETURNS _int4", 284},
	{"subarray", "1007,23,23", 1007, "subarray(_int4, int4, int4)\nRETURNS _int4", 279},
	{"intset", "23", 1007, "intset(int4)\nRETURNS _int4", 228},
}

func IntarraySliceProcedureSymbol(operation sqltype.IntarraySliceOperation) string {
	if operation >= sqltype.IntarraySliceOperationCount {
		return ""
	}
	return intarraySliceRecipes[operation].name
}

// IntarraySliceProcedure authenticates the original role, not its current name.
func IntarraySliceProcedure(v catalog.Read, p *catalog.Procedure) (sqltype.IntarraySliceOperation, bool) {
	if p == nil || p.Implementation != StockIntarray || p.Origin == nil {
		return 0, false
	}
	slot := p.Origin.Declaration
	if slot < IntarraySliceProcedureStart || slot >= IntarraySliceOperatorStart {
		return 0, false
	}
	if _, err := ProcedureSymbol(v, p); err != nil {
		return 0, false
	}
	return sqltype.IntarraySliceOperation(slot - IntarraySliceProcedureStart), true
}

func (p *IntarrayProvider) stageSlice(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	for i, recipe := range intarraySliceRecipes {
		revision, err := stage.Revision()
		if err != nil { return err }
		proc := &catalog.Procedure{ObjectAddress: ext.Members[IntarraySliceProcedureStart+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: recipe.name, Signature: recipe.signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: recipe.result, Volatility: expr.Immutable, Strict: true, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: recipe.name, Binary: "$libdir/_int", HasBinary: true}, Implementation: p}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, recipe.name), Context: fmt.Sprintf("SQL statement \"CREATE FUNCTION %s\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line %d", recipe.statement, recipe.line)}
		}
		if err := stage.Create(proc); err != nil { return err }
	}
	for i := range 2 {
		revision, err := stage.Revision()
		if err != nil { return err }
		left, right := ids.OID(0), ids.OID(1007)
		statement, line := "CREATE OPERATOR # (\n\tRIGHTARG = _int4,\n\tPROCEDURE = icount\n)", 238
		if i == 1 {
			left, right = 1007, 23
			statement, line = "CREATE OPERATOR # (\n\tLEFTARG = _int4,\n\tRIGHTARG = int4,\n\tPROCEDURE = idx\n)", 273
		}
		op := &catalog.Operator{ObjectAddress: ext.Members[IntarraySliceOperatorStart+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: "#", Owner: owner, Left: left, Right: right, Result: 23, Function: ext.Members[IntarraySliceProcedureStart+i].ObjectID}
		if stage.LookupName(catalog.DescriptorNameKey(op)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: "operator # already exists", Context: fmt.Sprintf("SQL statement \"%s\"\nextension script file \"intarray--1.2.sql\", near line %d", statement, line)}
		}
		if err := stage.Create(op); err != nil { return err }
	}
	return nil
}

func (p *IntarrayProvider) validateSlice(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid intarray slice manifest") }
	for slot := IntarraySliceProcedureStart; slot < IntarraySliceOperatorStart; slot++ {
		proc, ok := v.Procedure(ext.Members[slot])
		if !ok || ext.Members[slot].ClassID != ids.ProcedureClassID { return invalid() }
		if _, err := ProcedureSymbol(v, proc); err != nil { return err }
	}
	for i := range 2 {
		address := ext.Members[IntarraySliceOperatorStart+i]
		op, ok := v.Operator(address)
		left, right := ids.OID(0), ids.OID(1007)
		if i == 1 { left, right = 1007, 23 }
		if !ok || address.ClassID != ids.OperatorClassID || op.Function != ext.Members[IntarraySliceProcedureStart+i].ObjectID || op.Left != left || op.Right != right || op.Result != 23 || op.Commutator != 0 || op.Negator != 0 || op.Restriction != 0 || op.Join != 0 || op.Hash || op.Merge || !catalog.SameInstallation(op, ext.Installation) { return invalid() }
		if err := ValidateProviderHeader(v, op, ids.OperatorClassID); err != nil { return err }
		if err := catalog.ValidateInstalledClassOrigin(v, op); err != nil { return err }
		canonical, ok := ext.Installation.Declarations[IntarraySliceOperatorStart+i].(*catalog.Operator)
		if !ok || canonical.Name() != "#" { return invalid() }
	}
	return nil
}
