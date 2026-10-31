package contrib

import (
	"fmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// ValidateTablefuncCrosstab checks the installed crosstab declaration against
// current authority.
func ValidateTablefuncCrosstab[A Authority](h Host[A], n plan.InstalledTablefuncCrosstabExpr) error {
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
		return fmt.Errorf("stuntgres: stale installed crosstab declaration")
	}
	// Validate the retained capture as well as the current declaration. Replacing
	// it here would erase its principal and sealed-origin provenance fences.
	return stored.ValidateInstalledTablefuncCrosstab(n, auth.Access().Catalog)
}

// TablefuncCrosstab runs crosstab's source SQL and pivots the last command's
// rows.
func TablefuncCrosstab[A Authority](h Host[A], text string, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	spi := exec.NestedSQL(h)
	if err := spi.EnterNestedSQL(); err != nil {
		return nil, err
	}
	defer spi.LeaveNestedSQL()
	read, statements, err := spi.ParseNestedSQL(h.Operation().Context(), text)
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
	command := &crosstabCommand[A]{host: h, spi: spi, text: text, result: result}
	request := exec.NestedRequest{Text: text, Diagnose: true, Caller: "crosstab"}
	for i, statement := range statements {
		command.last = i == len(statements)-1
		if err := spi.ExecuteNestedSQL(statement, read, request, command); err != nil {
			return nil, err
		}
	}
	return command.values, nil
}

// crosstabCommand is one command of the crosstab source SQL. Only the last
// command's rows become the pivot.
type crosstabCommand[A Authority] struct {
	host   Host[A]
	spi    exec.NestedSQL
	text   string
	result *sqltype.Descriptor
	last   bool
	values []sqltype.Value
}

func (c *crosstabCommand[A]) NestedCommand(parser.Statement) (bool, error) { return false, nil }

func (c *crosstabCommand[A]) NestedAnalyzed(statement parser.Statement) error {
	tag := c.spi.NestedReadOnlyViolation(statement)
	if tag == "" {
		if _, ok := statement.(*parser.Select); !ok {
			tag = c.spi.NestedCommandTag(statement)
		}
	}
	if tag != "" {
		return exec.RewriteSQLDiagnostic(sqlError("0A000", tag+" is not allowed in a non-volatile function"), c.text)
	}
	return nil
}

func (c *crosstabCommand[A]) NestedRows(_ parser.Statement, rows exec.RowResult) error {
	if rows == nil {
		return nil
	}
	h := c.host
	var err error
	if c.last {
		input, output := h.Environments()
		// Match the shared routine-result diagnostic path: successful pivots
		// never capture a catalog/path display snapshot.
		var name func(sqltype.ID) string
		var namingErr error
		display := func(id sqltype.ID) string {
			if name == nil && namingErr == nil {
				h.ReacquireEngine()
				auth, captureErr := h.RoutineAuthorizationLocked()
				if captureErr == nil {
					name, captureErr = h.TypeDiagnosticName(auth)
				}
				h.ReleaseEngineLocked()
				namingErr = captureErr
			}
			if name != nil {
				return name(id)
			}
			return ""
		}
		c.values, err = exec.ConsumeCrosstab(h.Operation().Context(), rows, c.result, input, output, c.text, display)
		if namingErr != nil {
			err = namingErr
		}
		return err
	}
	scratch := make([]sqltype.Value, len(rows.Schema()))
	for {
		present, nextErr := rows.Next(h.Operation().Context(), scratch)
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
