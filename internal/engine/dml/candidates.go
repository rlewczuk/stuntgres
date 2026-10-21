package dml

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
)

type UpdateCandidate struct {
	storage.RowVersion
	Slot        int
	SourceIndex int
	Physical    exec.QueryPhysicalRow
}

type DeleteUsingCandidate struct {
	visibility.DeleteCandidate
	SourceIndex int
	Physical    exec.QueryPhysicalRow
}

func StatementCTERoot(statement parser.Statement) parser.Statement {
	switch s := statement.(type) {
	case *parser.CreateQueryTable:
		return s.Query
	case *parser.CreateView:
		return s.Query
	case *parser.DeclareCursor:
		return s.Query
	}
	return statement
}
