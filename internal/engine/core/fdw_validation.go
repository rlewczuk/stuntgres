package core

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// ValidateForeignOptions borrows this statement's routine runtime, omitting only
// explicit-call EXECUTE admission for the captured catalog validator. The caller
// holds neither Engine.Mu nor ReplacementMu; publication remains its responsibility.
func (s *StatementScope) ValidateForeignOptions(d *catalog.Procedure, kind catalog.ForeignObjectKind, options []catalog.ForeignOption) error {
	frame, result, err := s.prepareForeignValidator(d, kind, options)
	if err != nil {
		return err
	}
	return s.invokeFDWValidator(d, frame, result)
}

// prepareForeignValidator retains the statement journal before borrowing its
// current type descriptors and creating the invocation-owned argument frame.
func (s *StatementScope) prepareForeignValidator(d *catalog.Procedure, kind catalog.ForeignObjectKind, options []catalog.ForeignOption) (*proc.Frame, sqltype.Usage, error) {
	if err := s.executionContext().Err(); err != nil {
		return nil, sqltype.Usage{}, err
	}
	e := s.Engine()
	e.Mu.Lock()
	// This seam admits only actual SQL-validator invocations, including table
	// ALTER and IMPORT. Preserve any journal already owned by the statement so
	// routine row effects and the outer catalog candidate reach root Finish.
	if s.Catalog == nil {
		s.Catalog = s.Owner.Catalog.Clone()
	}
	read := e.PrivateCatalogLocked(s.Catalog)
	e.Mu.Unlock()
	return foreignValidatorFrame(read, d, kind, options)
}

// invokeFDWValidator owns the prepared frame through result validation and
// retains caller authority: no support owner or impersonation is supplied.
// SECURITY DEFINER and SET use the shared routine environment.
func (s *StatementScope) invokeFDWValidator(d *catalog.Procedure, frame *proc.Frame, result sqltype.Usage) error {
	defer frame.Close()
	value, err := s.executeSQLRoutine(d, frame, result, nil, sqltype.CollationRef{}, false, false, nil, fdwValidatorAdmission)
	if err != nil {
		return err
	}
	// OidFunctionCall2 requires a non-NULL Datum even though its value and
	// declared return type are otherwise ignored. Native void callbacks have
	// their own genuine provider path and do not pass through this SQL check.
	if value.IsNull() {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "XX000", Message: fmt.Sprintf("function %d returned NULL", d.Address().ObjectID)}
	}
	return nil
}

func foreignValidatorFrame(read catalog.Read, d *catalog.Procedure, kind catalog.ForeignObjectKind, options []catalog.ForeignOption) (*proc.Frame, sqltype.Usage, error) {
	if d.SQL == nil || len(d.SQL.Inputs) != 2 {
		return nil, sqltype.Usage{}, fmt.Errorf("stuntgres: foreign validator has no SQL routine definition")
	}
	inputs := make([]sqltype.Usage, 2)
	for i, usage := range d.SQL.Inputs {
		typ, ok := read.LookupType(usage.Type.ID())
		if !ok {
			return nil, sqltype.Usage{}, fmt.Errorf("stuntgres: foreign validator argument type is unavailable")
		}
		inputs[i] = usage
		inputs[i].Type = typ
	}
	result := d.SQL.Result
	typ, ok := read.LookupType(result.Type.ID())
	if !ok {
		return nil, sqltype.Usage{}, fmt.Errorf("stuntgres: foreign validator result type is unavailable")
	}
	result.Type = typ
	text, ok := read.LookupType(sqltype.Text)
	if !ok {
		return nil, sqltype.Usage{}, fmt.Errorf("stuntgres: foreign validator text type is unavailable")
	}
	elements := make([]sqltype.Value, len(options))
	for i, option := range options {
		elements[i] = sqltype.String(text, option.Name+"="+option.Value)
	}
	var dimensions []sqltype.ArrayDimension
	if len(elements) != 0 {
		dimensions = []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}
	}
	array, err := sqltype.OwnedArrayValue(inputs[0].Type, dimensions, elements)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	class, err := sqltype.OIDValue(inputs[1].Type, uint32(kind))
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	frame := proc.NewFrame(inputs)
	if err = frame.Bind(0, array, inputs[0]); err == nil {
		err = frame.Bind(1, class, inputs[1])
	}
	if err != nil {
		frame.Close()
		return nil, sqltype.Usage{}, err
	}
	return frame, result, nil
}
