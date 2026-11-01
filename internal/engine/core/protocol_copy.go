package core

import (
	"io"
	"stuntgres/internal/engine/copyfmt"
	"stuntgres/internal/parser"
)

// A frontend grants stream capability only for its active canonical execution.
// Embedded Execute supplies nil and retains its explicit COPY boundary.
type protocolCopyExecution struct {
	source      io.Reader
	destination io.Writer
	hooks       CopyProtocolHooks
}

// CopyProtocolMetadata describes the stream after real target/query admission.
type CopyProtocolMetadata struct {
	From          bool
	Format        int16
	ColumnFormats []int16
}

// CopyProtocolHooks are optional transport callbacks. FinishInput is a terminal
// input veto: it runs before staged rows can reach applyInsertValidated.
type CopyProtocolHooks struct {
	Start       func(CopyProtocolMetadata) error
	FinishInput func() error
}

// protocolCopyStart returns the frontend start publication, or nil without a
// Start hook. Callers run it inside copyIO.
func protocolCopyStart(hooks CopyProtocolHooks, from bool, options copyfmt.Options, columns int) func() error {
	if hooks.Start == nil {
		return nil
	}
	var format int16
	if options.Format == copyfmt.Binary {
		format = 1
	}
	formats := make([]int16, columns)
	for i := range formats {
		formats[i] = format
	}
	return func() error {
		return hooks.Start(CopyProtocolMetadata{From: from, Format: format, ColumnFormats: formats})
	}
}

func (s *StatementScope) startProtocolCopy(hooks CopyProtocolHooks, from bool, options copyfmt.Options, columns int) error {
	start := protocolCopyStart(hooks, from, options, columns)
	if start == nil {
		return nil
	}
	_, err := s.copyIO(func() (int, error) { return 0, start() })
	return err
}

// CopyBoundary is the database/sql capability error; it does no object lookup.
func CopyBoundary(s *parser.Copy) error {
	switch s.Endpoint {
	case parser.CopyFile:
		return SQLError("0A000", "COPY server files are unsupported")
	case parser.CopyProgram:
		return SQLError("0A000", "COPY PROGRAM is unsupported")
	}
	if s.From {
		return SQLError("0A000", "COPY FROM STDIN requires stuntgres.CopyFrom")
	}
	return SQLError("0A000", "COPY TO STDOUT requires stuntgres.CopyTo")
}
