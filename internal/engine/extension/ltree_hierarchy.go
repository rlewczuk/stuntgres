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
	LtreeHierarchyProcedureStart = LtreeHashClassSlot + 1
	LtreeHierarchyOperatorStart  = LtreeHierarchyProcedureStart + int(sqltype.LtreeHierarchyOperationCount)
	LtreeHierarchyMemberEnd      = LtreeHierarchyOperatorStart + int(sqltype.LtreeHierarchyOperationCount)
)

var ltreeHierarchyNames = [...]string{"ltree_isparent", "ltree_risparent"}
var ltreeHierarchyOperators = [...]string{"@>", "<@"}

func LtreeHierarchyProcedureSymbol(op sqltype.LtreeHierarchyOperation) string {
	if op >= sqltype.LtreeHierarchyOperationCount {
		return ""
	}
	return ltreeHierarchyNames[op]
}

func LtreeHierarchyProcedureName(name string) bool {
	for _, candidate := range ltreeHierarchyNames {
		if name == candidate {
			return true
		}
	}
	return false
}

func LtreeHierarchyProcedure(v catalog.Read, proc *catalog.Procedure) (sqltype.LtreeHierarchyOperation, bool) {
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
	index := proc.Origin.Declaration - LtreeHierarchyProcedureStart
	if index < 0 || index >= len(ltreeHierarchyNames) || symbol != ltreeHierarchyNames[index] {
		return 0, false
	}
	return sqltype.LtreeHierarchyOperation(index), true
}

func (p *LtreeProvider) stageHierarchy(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef, base *sqltype.Descriptor) error {
	signature := catalog.ProcedureSignature(fmt.Sprintf("%d,%d", base.ID(), base.ID()))
	for i, name := range ltreeHierarchyNames {
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		proc := &catalog.Procedure{ObjectAddress: ext.Members[LtreeHierarchyProcedureStart+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Signature: signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: 16, Volatility: expr.Immutable, Strict: true, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name, Binary: "$libdir/ltree", HasBinary: true}, Implementation: p}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, name)}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	for i, name := range ltreeHierarchyOperators {
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		op := &catalog.Operator{ObjectAddress: ext.Members[LtreeHierarchyOperatorStart+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: name, Owner: owner, Left: ids.OID(base.ID()), Right: ids.OID(base.ID()), Result: 16, Function: ext.Members[LtreeHierarchyProcedureStart+i].ObjectID, Commutator: ext.Members[LtreeHierarchyOperatorStart+(i^1)].ObjectID, Restriction: 5040, Join: 5041}
		if stage.LookupName(catalog.DescriptorNameKey(op)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("operator %s already exists", name)}
		}
		if err := stage.Create(op); err != nil {
			return err
		}
	}
	return nil
}

func validateLtreeHierarchy(v catalog.Read, ext *catalog.Extension, base *catalog.Type) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid ltree hierarchy manifest") }
	for i, name := range ltreeHierarchyOperators {
		if ext.Members[LtreeHierarchyProcedureStart+i].ClassID != ids.ProcedureClassID || ext.Members[LtreeHierarchyOperatorStart+i].ClassID != ids.OperatorClassID {
			return invalid()
		}
		proc, ok := v.Procedure(ext.Members[LtreeHierarchyProcedureStart+i])
		if !ok {
			return invalid()
		}
		if _, err := ProcedureSymbol(v, proc); err != nil {
			return err
		}
		op, ok := v.Operator(ext.Members[LtreeHierarchyOperatorStart+i])
		if !ok || op.Name() != name || op.Namespace() != ext.TargetNamespace || op.Extension != ext.Address() || op.Left != ids.OID(base.SQLType.ID()) || op.Right != op.Left || op.Result != 16 || op.Function != proc.Address().ObjectID || op.Commutator != ext.Members[LtreeHierarchyOperatorStart+(i^1)].ObjectID || op.Negator != 0 || op.Restriction != 5040 || op.Join != 5041 || op.Hash || op.Merge {
			return invalid()
		}
		if err := ValidateProviderHeader(v, op, ids.OperatorClassID); err != nil {
			return err
		}
	}
	return nil
}
