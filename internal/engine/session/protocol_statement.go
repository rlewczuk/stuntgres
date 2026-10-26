package session

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Pure statement checks of the protocol and prepared paths.

func PreparableBody(stmt parser.Statement) bool {
	switch stmt.(type) {
	case *parser.Call, *parser.Select, *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
		return true
	}
	return false
}

func ProtocolPlannedStatement(stmt parser.Statement) bool {
	if PreparableBody(stmt) {
		return true
	}
	switch stmt.(type) {
	case *parser.DeclareCursor, *parser.CreateQueryTable:
		return true
	}
	return false
}

func ProtocolPlainSelect(stmt parser.Statement) bool {
	q, ok := stmt.(*parser.Select)
	if !ok {
		return false
	}
	for _, cte := range q.With {
		if !ProtocolPlainSelect(cte.Query) {
			return false
		}
	}
	return true
}

func ProtocolParameterOIDs(count int, types []sqltype.Usage) []uint32 {
	ids := make([]uint32, count)
	for i, usage := range types {
		if usage.Type != nil {
			ids[i] = uint32(usage.Type.ID())
		}
	}
	return ids
}

func ProtocolHasRows(stmt parser.Statement, columns []catalog.Column) bool {
	if columns != nil {
		return true
	}
	switch raw := stmt.(type) {
	case *parser.Select:
		return true
	case *parser.Insert:
		return raw.Returning != nil
	case *parser.Update:
		return raw.Returning != nil
	case *parser.Delete:
		return raw.Returning != nil
	case *parser.Merge:
		return raw.Returning != nil
	}
	return false
}
