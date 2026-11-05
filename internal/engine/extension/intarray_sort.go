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

type intarraySortRecipe struct {
	name      string
	signature catalog.ProcedureSignature
	statement string
	line      int
}

var intarraySortRecipes = [...]intarraySortRecipe{
	{"sort", "1007", "sort(_int4)\nRETURNS _int4", 248},
	{"sort", "1007,25", "sort(_int4, text)\nRETURNS _int4", 243},
	{"sort_asc", "1007", "sort_asc(_int4)\nRETURNS _int4", 253},
	{"sort_desc", "1007", "sort_desc(_int4)\nRETURNS _int4", 258},
	{"uniq", "1007", "uniq(_int4)\nRETURNS _int4", 263},
}

func IntarraySortProcedureSymbol(operation sqltype.IntarraySortOperation) string {
	if operation >= sqltype.IntarraySortOperationCount {
		return ""
	}
	return intarraySortRecipes[operation].name
}

// IntarraySortProcedure authenticates the original installation role, not names.
func IntarraySortProcedure(v catalog.Read, p *catalog.Procedure) (sqltype.IntarraySortOperation, bool) {
	if p == nil || p.Implementation != StockIntarray || p.Origin == nil {
		return 0, false
	}
	slot := p.Origin.Declaration
	if slot < IntarraySortProcedureStart || slot >= IntarrayMemberCount {
		return 0, false
	}
	if _, err := ProcedureSymbol(v, p); err != nil {
		return 0, false
	}
	return sqltype.IntarraySortOperation(slot - IntarraySortProcedureStart), true
}

func (p *IntarrayProvider) stageSort(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	for i, recipe := range intarraySortRecipes {
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		proc := &catalog.Procedure{ObjectAddress: ext.Members[IntarraySortProcedureStart+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: recipe.name, Signature: recipe.signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: 1007, Volatility: expr.Immutable, Strict: true, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: recipe.name, Binary: "$libdir/_int", HasBinary: true}, Implementation: p}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, recipe.name), Context: fmt.Sprintf("SQL statement \"CREATE FUNCTION %s\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"intarray--1.2.sql\", near line %d", recipe.statement, recipe.line)}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	return nil
}

func (p *IntarrayProvider) validateSort(v catalog.Read, ext *catalog.Extension) error {
	for slot := IntarraySortProcedureStart; slot < IntarrayMemberCount; slot++ {
		proc, ok := v.Procedure(ext.Members[slot])
		if !ok || ext.Members[slot].ClassID != ids.ProcedureClassID {
			return fmt.Errorf("stuntgres: invalid intarray sort manifest")
		}
		if _, err := ProcedureSymbol(v, proc); err != nil {
			return err
		}
	}
	return nil
}
