package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
)

type updateCandidate struct {
	storage.RowVersion
	slot        int
	sourceIndex int
	physical    exec.QueryPhysicalRow
}

type deleteUsingCandidate struct {
	visibility.DeleteCandidate
	sourceIndex int
	physical    exec.QueryPhysicalRow
}

func statementCTERoot(statement parser.Statement) parser.Statement {
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
