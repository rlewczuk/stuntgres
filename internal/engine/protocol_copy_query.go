package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// COPY owns the initialized cursor directly; its capture carrier has no name,
// registry entry, birth or portal execution lifecycle.
func (s *StatementScope) prepareCopyQueryRows(query *parser.Select) (RowResult, error) {
	expected, binding := s.Expected, s.protocolOrZero().bind
	s.Expected = nil
	s.setProtocolBind(nil)
	defer func() { s.Expected = expected; s.setProtocolBind(binding) }()
	if !(s.protocolOrZero().command || s.protocolOrZero().simple) || !protocolPlainSelect(query) {
		result, err := s.op.session.Engine().executeStatement(s, query, 0, nil)
		return result.Rows, err
	}
	capture := protocolPortal{}
	s.protocol().bind = &capture
	_, err := s.op.session.Engine().executeStatement(s, query, 0, func(context.Context, ParameterInfo, []sqltype.Value, sqltype.InputEnvironment) error { return nil })
	if err != nil {
		_ = s.protocolTerminalIO(func() error { return capture.releaseExecutor(s.op.Context()) })
		return nil, err
	}
	if capture.producer == nil {
		return nil, errors.New("stuntgres: COPY query did not initialize its producer")
	}
	rows := &protocolCopyQueryRows{scope: s, producer: capture.producer, schema: capture.producer.schema}
	capture.producer = nil
	rows.output = NewFinalRows(rows.schema, 0)
	rows.output.Output = s.output
	err = func() error {
		s.enterProtocolProducer(rows.producer)
		defer s.leaveProtocolProducer(rows.producer)
		return s.authorizeSQLCursor(rows.producer)
	}()
	if err != nil {
		_ = rows.Close()
		return nil, err
	}
	return rows, nil
}

type protocolCopyQueryRows struct {
	scope    *StatementScope
	producer *sqlCursor
	schema   []catalog.Column
	output   *finalRows
}

func (r *protocolCopyQueryRows) Schema() []catalog.Column                     { return r.schema }
func (r *protocolCopyQueryRows) OutputEnvironment() sqltype.OutputEnvironment { return r.output.Output }
func (r *protocolCopyQueryRows) RegclassText(v sqltype.Value) (string, error) {
	return r.output.RegclassText(v)
}

func (r *protocolCopyQueryRows) Next(ctx context.Context, values []sqltype.Value) (bool, error) {
	c := r.producer
	if c == nil {
		return false, nil
	}
	if err := r.scope.active(); err != nil {
		return false, err
	}
	r.scope.enterProtocolProducer(c)
	defer r.scope.leaveProtocolProducer(c)
	if err := c.openProducer(r.scope); err != nil {
		return false, err
	}
	present, err := c.nextProtocolRow(ctx, values)
	if err != nil || !present {
		return present, err
	}
	if err := r.scope.captureProtocolOutput(r.output, values, c.catalog); err != nil {
		return false, err
	}
	return true, nil
}

func (r *protocolCopyQueryRows) Close() error {
	c := r.producer
	if c == nil {
		return nil
	}
	r.producer = nil
	return r.scope.protocolTerminalIO(func() error {
		err := c.dispose(r.scope.op.Context())
		if closeErr := r.output.Close(); err == nil {
			err = closeErr
		}
		return err
	})
}
