package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// plTriggerRelation resolves the firing relation's row type and names for
// proc.NewPLTriggerFrame.
func plTriggerRelation(read catalog.Read, relation *catalog.Relation) (proc.PLTriggerRelation, error) {
	shape, ok := read.LookupType(relation.RowType)
	if !ok {
		return proc.PLTriggerRelation{}, fmt.Errorf("stuntgres: missing trigger relation row type")
	}
	ns, ok := read.Get(relation.Namespace()).(*catalog.Namespace)
	if !ok {
		return proc.PLTriggerRelation{}, fmt.Errorf("stuntgres: missing trigger namespace")
	}
	return proc.PLTriggerRelation{Shape: shape, OID: uint32(relation.Address().ObjectID), Name: relation.Name(), Namespace: ns.Name()}, nil
}

// invokePLTrigger is reached only by the admitted trigger payload entry, never
// by ordinary SQL routine dispatch. Its caller owns depth and live provenance.
func (s *StatementScope) invokePLTrigger(d *catalog.Procedure, f *proc.PLFrame, read catalog.Read, relation *catalog.Relation) (sqltype.Value, error) {
	diagnosticIdentity, err := s.plDiagnosticIdentityFor(d, read)
	if err != nil {
		return sqltype.Value{}, err
	}
	previousIdentity := s.sqlRoutine.plDiagnostic
	s.sqlRoutine.plDiagnostic = diagnosticIdentity
	defer func() { s.sqlRoutine.plDiagnostic = previousIdentity }()
	result, flow, err := s.executePLBlock(d, f, d.SQL.PL.Root, read)
	if err != nil {
		return sqltype.Value{}, err
	}
	identity := "PL/pgSQL function " + s.plRoutineDiagnosticIdentity(read, d)
	if flow.Kind != proc.PLReturn {
		return sqltype.Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2F005", Message: "control reached end of trigger procedure without RETURN", Context: identity}
	}
	shape, ok := read.LookupType(relation.RowType)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: trigger row type disappeared")
	}
	result, err = proc.TriggerReturn(shape, result)
	if diagnostic, ok := err.(*sqlstate.Error); ok {
		copy := *diagnostic
		copy.Context = identity + " during function exit"
		err = &copy
	}
	return result, err
}
