package core

import (
	"fmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (h *contribHost) ValidateTablefuncCrosstab(n plan.InstalledTablefuncCrosstabExpr) error {
	s, e := h.scope(), h.scope().Engine()
	if err := s.Active(); err != nil {
		return err
	}
	e.Mu.Lock()
	auth, err := s.writeAuthorizationLocked(e)
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	current, ok := auth.Catalog.Procedure(n.Ref.Address)
	if !ok || n.RoutineReference != n.Procedure || !exec.SameRoutineDeclaration(current, n.Procedure) {
		return fmt.Errorf("stuntgres: stale installed crosstab declaration")
	}
	// Validate the retained capture as well as the current declaration. Replacing
	// it here would erase its principal and sealed-origin provenance fences.
	return stored.ValidateInstalledTablefuncCrosstab(n, auth.Catalog)
}

func (h *contribHost) TablefuncCrosstab(text string, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	spi := h.nestedSQL()
	if err := spi.EnterNestedSQL(); err != nil {
		return nil, err
	}
	defer spi.LeaveNestedSQL()
	read, statements, err := spi.ParseNestedSQL(h.scope().Op.Context(), text)
	if err != nil {
		return nil, exec.RewriteSQLDiagnostic(err, text)
	}
	// SPI prepares the entire string before executing any of its plans.
	for _, statement := range statements {
		if err := spi.PrepareNestedSQL(statement, read); err != nil {
			return nil, exec.RewriteSQLDiagnostic(err, text)
		}
	}
	// Admission, expected analysis and source authority belong to one command,
	// not the entire SPI string. In particular, the next SELECT must not reuse
	// the preceding SELECT's prepared/source captures.
	command := &crosstabCommand{host: h, spi: spi, text: text, result: result}
	request := exec.NestedRequest{Text: text, Diagnose: true, Caller: "crosstab"}
	for i, statement := range statements {
		command.last = i == len(statements)-1
		if err := spi.ExecuteNestedSQL(statement, read, request, command); err != nil {
			return nil, err
		}
	}
	return command.values, nil
}

func (h *contribHost) nestedSQL() exec.NestedSQL { return (*statementHost)(h) }

// crosstabCommand is one command of the crosstab source SQL. Only the last
// command's rows become the pivot.
type crosstabCommand struct {
	host   *contribHost
	spi    exec.NestedSQL
	text   string
	result *sqltype.Descriptor
	last   bool
	values []sqltype.Value
}

func (c *crosstabCommand) NestedCommand(parser.Statement) (bool, error) { return false, nil }

func (c *crosstabCommand) NestedAnalyzed(statement parser.Statement) error {
	tag := c.spi.NestedReadOnlyViolation(statement)
	if tag == "" {
		if _, ok := statement.(*parser.Select); !ok {
			tag = c.spi.NestedCommandTag(statement)
		}
	}
	if tag != "" {
		return exec.RewriteSQLDiagnostic(SQLError("0A000", tag+" is not allowed in a non-volatile function"), c.text)
	}
	return nil
}

func (c *crosstabCommand) NestedRows(_ parser.Statement, rows exec.RowResult) error {
	if rows == nil {
		return nil
	}
	h := c.host
	s, e := h.scope(), h.scope().Engine()
	var err error
	if c.last {
		input, output := h.host().Environments()
		// Match the shared routine-result diagnostic path: successful pivots
		// never capture a catalog/path display snapshot.
		var name func(sqltype.ID) string
		var namingErr error
		display := func(id sqltype.ID) string {
			if name == nil && namingErr == nil {
				e.Mu.Lock()
				auth, captureErr := s.RoutineAuthorizationLocked()
				if captureErr == nil {
					name, captureErr = CaptureTypeDiagnosticName(auth)
				}
				e.Mu.Unlock()
				namingErr = captureErr
			}
			if name != nil {
				return name(id)
			}
			return ""
		}
		c.values, err = exec.ConsumeCrosstab(s.Op.Context(), rows, c.result, input, output, c.text, display)
		if namingErr != nil {
			err = namingErr
		}
		return err
	}
	scratch := make([]sqltype.Value, len(rows.Schema()))
	for {
		present, nextErr := rows.Next(s.Op.Context(), scratch)
		if nextErr != nil {
			err = exec.RewriteSQLDiagnostic(nextErr, c.text)
			break
		}
		if !present {
			break
		}
	}
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	return err
}
