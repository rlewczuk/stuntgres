package core

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// These borrowed live nodes belong to one serialized operation. They are never
// lexical SQL scratch, prepared artifacts, catalog state or session caches.
// PL nodes are embedded in the invocation host. Only requested context renders.
type routineLive struct {
	parent     *routineLive
	host       *plHost
	definition *proc.Definition
	origin     proc.PLOrigin
	sqlName    string
	statement  int
	query      string
}
type plExpressionLive struct {
	parent *plExpressionLive
	site   *parser.Select
}

func (h *plHost) EnterRoutine() {
	h.live = routineLive{parent: h.s.routineLive, host: h, definition: h.run.Definition}
	h.s.routineLive = &h.live
}
func (h *plHost) LeaveRoutine() {
	h.s.routineLive = h.live.parent
	h.live = routineLive{}
}
func (h *plHost) setOrigin(at proc.PLOrigin) proc.PLOrigin {
	previous := h.live.origin
	h.live.origin = at
	return previous
}
func (s *StatementScope) currentPLContext() string {
	var text strings.Builder
	for node := s.routineLive; node != nil; node = node.parent {
		if node.host == nil && node.sqlName == "" && node.query == "" {
			continue
		}
		if text.Len() != 0 {
			text.WriteByte('\n')
		}
		if node.host != nil {
			text.WriteString(proc.PLActionContext(node.definition.Source, node.host.Identity(), node.origin.Position, node.origin.Action))
		} else if node.query != "" {
			text.WriteString("SQL statement \"")
			text.WriteString(node.query)
			text.WriteByte('"')
		} else {
			text.WriteString(exec.SQLRoutineStatementFrame(node.sqlName, node.statement))
		}
	}
	return text.String()
}

func (h *plHost) Diagnostics(f *proc.PLFrame, in proc.PLInstruction) error {
	action := "at GET DIAGNOSTICS"
	if in.Diagnostics.Stacked {
		action = "at GET STACKED DIAGNOSTICS"
	}
	at := proc.PLOrigin{Position: in.Syntax.Pos, Action: action}
	previous := h.setOrigin(at)
	defer func() { h.live.origin = previous }()
	failure := func(err error) error {
		return proc.PLRuntimeQueryError(err, h.run.Definition, h.Identity, at.Position, action, proc.PLDiagnosticQuery{})
	}
	caught := f.HandledError()
	if in.Diagnostics.Stacked && caught == nil {
		return failure(SQLError("0Z002", "GET STACKED DIAGNOSTICS cannot be used outside an exception handler"))
	}
	// Snapshot the source before sequential destination conversions. In particular
	// assigning SQLSTATE/SQLERRM does not modify the original handled diagnostic.
	count := f.RowCount()
	var stack string
	for _, a := range in.Diagnostics.Assignments {
		var value sqltype.Value
		switch a.Item {
		case parser.PLRowCount:
			typ, ok := h.LookupType(sqltype.Int8)
			if !ok {
				return fmt.Errorf("stuntgres: missing diagnostics bigint")
			}
			var err error
			value, err = sqltype.Integer(typ, count)
			if err != nil {
				return err
			}
		case parser.PLRoutineOID:
			typ, ok := h.LookupType(sqltype.Oid)
			if !ok {
				return fmt.Errorf("stuntgres: missing diagnostics oid")
			}
			var err error
			value, err = sqltype.OIDValue(typ, h.d.RoutineOID())
			if err != nil {
				return err
			}
		default:
			var text string
			switch a.Item {
			case parser.PLContext:
				if stack == "" {
					stack = h.s.currentPLContext()
				}
				text = stack
			case parser.PLReturnedSQLState:
				text = caught.Code
			case parser.PLMessageText:
				text = caught.Message
			case parser.PLExceptionDetail:
				text = caught.Detail
			case parser.PLExceptionHint:
				text = caught.Hint
			case parser.PLExceptionContext:
				text = caught.Context
			case parser.PLSchemaName:
				text = caught.Schema
			case parser.PLTableName:
				text = caught.Table
			case parser.PLColumnName:
				text = caught.Column
			case parser.PLDatatypeName:
				text = caught.DataType
			case parser.PLConstraintName:
				text = caught.Constraint
			default:
				return fmt.Errorf("stuntgres: invalid checked diagnostics item")
			}
			typ, ok := h.LookupType(sqltype.Text)
			if !ok {
				return fmt.Errorf("stuntgres: missing diagnostics text")
			}
			value = sqltype.String(typ, text)
		}
		if err := h.AssignValue(f, a.Target, value, at); err != nil {
			return failure(err)
		}
	}
	return nil
}
