package extension

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

const (
	segIntervalProcedureStart = 3
	segIntervalOperatorStart  = 11
	SegMemberCount            = 19
)

var segIntervalMembers = [...]struct {
	operator          string
	operation         sqltype.SegIntervalOperation
	commutator        int
	restriction, join ids.OID
}{
	{"<<", sqltype.SegLeft, 1, 1300, 1301},
	{">>", sqltype.SegRight, 0, 1300, 1301},
	{"&<", sqltype.SegOverLeft, -1, 1300, 1301},
	{"&>", sqltype.SegOverRight, -1, 1300, 1301},
	{"=", sqltype.SegSame, 4, 101, 105},
	{"&&", sqltype.SegOverlap, 5, 139, 140},
	{"@>", sqltype.SegContains, 7, 1302, 1303},
	{"<@", sqltype.SegContained, 6, 1302, 1303},
}

func segIntervalCommutator(ext *catalog.Extension, member int) ids.OID {
	if member < 0 {
		return 0
	}
	return ext.Members[segIntervalOperatorStart+member].ObjectID
}

func SegBinarySignature(id sqltype.ID) catalog.ProcedureSignature {
	one := SegSignature(id)
	return one + "," + one
}

// Check the canonical binary signature without formatting allocator identities
// on each evaluation.
func segBinaryInputs(signature catalog.ProcedureSignature, id sqltype.ID) bool {
	first, second, binary := strings.Cut(string(signature), ",")
	if !binary || first != second || first == "" || first[0] < '1' || first[0] > '9' {
		return false
	}
	actual, err := strconv.ParseUint(first, 10, 32)
	return err == nil && actual == uint64(id)
}

func (p *SegProvider) stageInterval(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef, typ *sqltype.Descriptor) error {
	signature := SegBinarySignature(typ.ID())
	for i, member := range segIntervalMembers {
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		proc := &catalog.Procedure{ObjectAddress: ext.Members[segIntervalProcedureStart+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: member.operation.Routine(), Signature: signature, RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: ids.OID(member.operation.ResultID()), Volatility: expr.Immutable, Strict: true, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: member.operation.Routine()}, Implementation: p}
		proc.Display.Binary, proc.Display.HasBinary = "$libdir/seg", true
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf(`function "%s" already exists with same argument types`, proc.Name())}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
		revision, err = stage.Revision()
		if err != nil {
			return err
		}
		op := &catalog.Operator{ObjectAddress: ext.Members[segIntervalOperatorStart+i], ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: member.operator, Left: ids.OID(typ.ID()), Right: ids.OID(typ.ID()), Owner: owner, Result: proc.Result, Function: proc.Address().ObjectID, Commutator: segIntervalCommutator(ext, member.commutator), Restriction: member.restriction, Join: member.join, Merge: member.operation == sqltype.SegSame}
		if stage.LookupName(catalog.DescriptorNameKey(op)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: fmt.Sprintf(`operator %s already exists`, op.Name())}
		}
		if err := stage.Create(op); err != nil {
			return err
		}
	}
	return nil
}

func (p *SegProvider) validateInterval(v catalog.Read, ext *catalog.Extension, base *catalog.Type) error {
	for i, member := range segIntervalMembers {
		proc, ok := v.Procedure(ext.Members[segIntervalProcedureStart+i])
		if !ok {
			return fmt.Errorf("stuntgres: invalid seg interval procedure")
		}
		symbol, err := ProcedureSymbol(v, proc)
		if err != nil {
			return err
		}
		if proc.Address().ClassID != ids.ProcedureClassID || proc.Address() != ext.Members[segIntervalProcedureStart+i] || proc.Extension != ext.Address() || proc.Revision() == 0 || proc.Owner == (authorization.PrincipalRef{}) || v.NameAddress(catalog.DescriptorNameKey(proc)) != proc.Address() || SegProviderOf(proc) != p || symbol != member.operation.Routine() || !segBinaryInputs(proc.Signature, base.SQLType.ID()) || proc.Result != ids.OID(member.operation.ResultID()) || proc.RoutineKind != catalog.FunctionRoutine || !proc.Strict || proc.Volatility != expr.Immutable || proc.SQL != nil || proc.Builtin != nil || proc.Aggregate != nil || proc.Foreign != nil || proc.Dictionary != (ids.ObjectAddress{}) || proc.Conversion.Name() != "" {
			return fmt.Errorf("stuntgres: invalid seg interval procedure")
		}
		op, ok := v.Operator(ext.Members[segIntervalOperatorStart+i])
		if !ok || op.Address().ClassID != ids.OperatorClassID || op.Address() != ext.Members[segIntervalOperatorStart+i] || op.Namespace() != ext.TargetNamespace || op.Extension != ext.Address() || op.Revision() == 0 || op.Owner == (authorization.PrincipalRef{}) || v.NameAddress(catalog.DescriptorNameKey(op)) != op.Address() || op.Name() != member.operator || op.Left != ids.OID(base.SQLType.ID()) || op.Right != op.Left || op.Result != proc.Result || op.Function != proc.Address().ObjectID || op.Commutator != segIntervalCommutator(ext, member.commutator) || op.Negator != 0 || op.Restriction != member.restriction || op.Join != member.join || op.Hash || op.Merge != (member.operation == sqltype.SegSame) {
			return fmt.Errorf("stuntgres: invalid seg interval operator")
		}
	}
	return nil
}
