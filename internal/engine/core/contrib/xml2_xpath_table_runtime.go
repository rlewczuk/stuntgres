package contrib

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func ValidateXML2XPathTable[A Authority](h Host[A], n plan.InstalledXML2XPathTableExpr) error {
	if err := h.Active(); err != nil {
		return err
	}
	h.ReacquireEngine()
	auth, err := h.WriteAuthorizationLocked()
	h.ReleaseEngineLocked()
	if err != nil {
		return err
	}
	current, ok := auth.Access().Catalog.Procedure(n.Ref.Address)
	if !ok || n.RoutineReference != n.Procedure || !exec.SameRoutineDeclaration(current, n.Procedure) {
		return fmt.Errorf("stale installed xpath_table declaration")
	}
	return stored.ValidateInstalledXML2XPathTable(n, auth.Access().Catalog)
}

func XML2XPathTable[A Authority](h Host[A], args [5]string, result *sqltype.Descriptor, procedure *catalog.Procedure) ([]sqltype.Value, error) {
	restore, err := h.ActivateXML2XPathTableCommand(procedure)
	if err != nil {
		return nil, err
	}
	defer restore()
	if err := h.EnterNestedSQL(); err != nil {
		return nil, err
	}
	defer h.LeaveNestedSQL()
	text := "SELECT " + args[0] + ", " + args[1] + " FROM " + args[2] + " WHERE " + args[4]
	read, statements, err := h.ParseNestedSQL(h.Operation().Context(), text)
	if err != nil {
		return nil, exec.SPIStatementDiagnostic(err, text)
	}
	for _, statement := range statements {
		if err := h.PrepareNestedSQL(statement, read); err != nil {
			return nil, exec.SPIStatementDiagnostic(err, text)
		}
	}
	command := xpathTableCommand[A]{host: h}
	request := exec.NestedRequest{Text: text, Diagnose: true, Caller: "xpath_table", FreshCommand: true, CloseFailed: true}
	for i, statement := range statements {
		command.last = i == len(statements)-1
		if err := h.ExecuteNestedSQL(statement, read, request, &command); err != nil {
			return nil, err
		}
	}
	if !command.selected {
		return nil, sqlError("XX000", "xpath_table: SPI execution failed for query "+text)
	}
	if command.width != 2 {
		return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "expression returning multiple columns is not valid in parameter list", Detail: fmt.Sprintf("Expected two columns in SPI result, got %d.", command.width)}
	}
	input, output := h.Environments()
	return sqltype.XML2Table(h.Operation().Context(), command.source, result, args[3], input, output, func(notice sqlstate.Error) {
		h.Operation().EmitNotice(session.LevelNotice, notice)
	})
}

// Only the final SELECT's owned cells survive. Every source is completely
// drained and closed before XML parsing or declared record input begins.
type xpathTableCommand[A Authority] struct {
	host           Host[A]
	last, selected bool
	width          int
	source         []sqltype.Value
}

func (*xpathTableCommand[A]) NestedCommand(parser.Statement) (bool, error) { return false, nil }
func (*xpathTableCommand[A]) NestedAnalyzed(parser.Statement) error        { return nil }
func (c *xpathTableCommand[A]) NestedRows(statement parser.Statement, rows exec.RowResult) (err error) {
	if c.last {
		_, c.selected = statement.(*parser.Select)
	}
	if rows == nil {
		return nil
	}
	defer func() {
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
	}()
	width := len(rows.Schema())
	if c.last {
		c.width = width
	}
	scratch := make([]sqltype.Value, width)
	for {
		if err := c.host.Operation().Context().Err(); err != nil {
			return err
		}
		present, err := rows.Next(c.host.Operation().Context(), scratch)
		if err != nil {
			return err
		}
		if !present {
			return nil
		}
		if c.last && c.selected && width == 2 {
			c.source = append(c.source, scratch...)
		}
	}
}
