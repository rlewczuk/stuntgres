package core

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

// InvokeForeignValidator borrows this statement's operation and routine runtime.
// Its caller has released engine and replacement locks; only the surrounding
// DDL executor may publish staged metadata and effects. Non-NULL results are ignored.
func (s *StatementScope) InvokeForeignValidator(p *catalog.Procedure, kind catalog.ForeignObjectKind, options []catalog.ForeignOption) error {
	if err := s.executionContext().Err(); err != nil {
		return err
	}
	frame, err := foreignValidatorFrame(p.SQL.Inputs, kind, options)
	if err != nil {
		return err
	}
	return s.invokeForeignValidatorFrame(p, frame)
}

func (s *StatementScope) invokeForeignValidatorFrame(p *catalog.Procedure, frame *proc.Frame) error {
	defer frame.Close()
	// A validator may write local rows or run nested DDL. Retain a statement
	// journal before invocation so the outer utility's publication and Finish
	// install those effects atomically with its still-private foreign stage.
	if s.Catalog == nil {
		s.Catalog = s.Owner.Catalog.Clone()
	}
	value, err := s.invokeSQLRoutine(p, frame, p.SQL.Result, nil, sqltype.CollationRef{}, false, nil)
	if err == nil && !p.SQL.Void() && value.IsNull() {
		return SQLError("XX000", fmt.Sprintf("function %d returned NULL", p.Address().ObjectID))
	}
	return err
}

func foreignValidatorFrame(inputs []sqltype.Usage, kind catalog.ForeignObjectKind, options []catalog.ForeignOption) (*proc.Frame, error) {
	elements := make([]sqltype.Value, len(options))
	for i, option := range options {
		elements[i] = sqltype.String(inputs[0].Type.Element(), option.Name+"="+option.Value)
	}
	var dimensions []sqltype.ArrayDimension
	if len(elements) != 0 {
		dimensions = []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}
	}
	array, err := sqltype.OwnedArrayValue(inputs[0].Type, dimensions, elements)
	if err != nil {
		return nil, err
	}
	class, err := sqltype.OIDValue(inputs[1].Type, uint32(kind))
	if err != nil {
		return nil, err
	}
	frame := proc.NewFrame(inputs)
	if err = frame.Bind(0, array, inputs[0]); err == nil {
		err = frame.Bind(1, class, inputs[1])
	}
	if err != nil {
		frame.Close()
		return nil, err
	}
	return frame, nil
}
