package core

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// ValidateConnectby checks the installed connectby declaration against
// current authority.
func ValidateConnectby[A Authority](h Host[A], n plan.InstalledConnectbyExpr) error {
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
		return fmt.Errorf("stuntgres: stale installed connectby declaration")
	}
	n.Procedure, n.RoutineReference = current, current
	return stored.ValidateInstalledConnectby(n, auth.Access().Catalog)
}
func connectbyReturnError(detail string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "invalid connectby return type", Detail: detail}
}
func validateConnectbyReturn(r functions.ConnectbyRequest) error {
	width := 3
	if r.Branch {
		width++
	}
	if r.Ordered {
		width++
	}
	if r.Result.CompositeLen() != width {
		return connectbyReturnError(fmt.Sprintf("Return row must have %d columns, not %d.", width, r.Result.CompositeLen()))
	}
	if r.Result.CompositeMember(2).Usage.Type.ID() != sqltype.Int4 {
		return connectbyReturnError("Third return column (depth) must be type integer.")
	}
	if r.Branch && r.Result.CompositeMember(3).Usage.Type.ID() != sqltype.Text {
		return connectbyReturnError("Fourth return column (branch) must be type text.")
	}
	if r.Ordered && r.Result.CompositeMember(width-1).Usage.Type.ID() != sqltype.Int4 {
		ordinal := "Fourth"
		if r.Branch {
			ordinal = "Fifth"
		}
		return connectbyReturnError(ordinal + " return column (serial) must be type integer.")
	}
	return nil
}
func validateConnectbySource(result *sqltype.Descriptor, schema []catalog.Column) error {
	if len(schema) < 2 {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "invalid connectby source data query", Detail: "The query must return at least two columns."}
	}
	for i, label := range []string{"key", "parent key"} {
		source, target := schema[i].Usage, result.CompositeMember(i).Usage
		if source.Type.ID() != target.Type.ID() || target.Typmod >= 0 && source.Typmod != target.Typmod {
			return connectbyReturnError("Source " + label + " type " + exec.ResultUsageName(source, nil) + " does not match return " + label + " type " + exec.ResultUsageName(target, nil) + ".")
		}
	}
	return nil
}

type connectbyChildren struct {
	rows   [][2]sqltype.Value
	schema []catalog.Column
}
type connectbyFrame struct {
	children connectbyChildren
	index    int
	level    int64
	branch   string
}

// Connectby walks the hierarchy depth first, one nested child query per key.
func Connectby[A Authority](h Host[A], r functions.ConnectbyRequest) ([]sqltype.Value, error) {
	ctx := h.Context()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateConnectbyReturn(r); err != nil {
		return nil, err
	}
	input, output := r.Input, r.Output
	var scratch [5]sqltype.Value
	fields := scratch[:r.Result.CompositeLen()]
	serial := int64(0)
	rows := make([]sqltype.Value, 0)
	emit := func(key, parent *string, level int64, branch string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		for i, text := range [2]*string{key, parent} {
			usage := r.Result.CompositeMember(i).Usage
			fields[i] = sqltype.Null(usage.Type)
			if text != nil {
				value, err := usage.Type.InputWithEnvironment(ctx, *text, usage.Typmod, input)
				if err != nil {
					return err
				}
				fields[i] = value
			}
			if text == nil {
				if err := usage.Type.CheckDomainInput(ctx, fields[i], input); err != nil {
					return err
				}
			}
		}
		var err error
		fields[2], err = sqltype.Integer(r.Result.CompositeMember(2).Usage.Type, level)
		if err != nil {
			return err
		}
		if r.Branch {
			fields[3] = sqltype.String(r.Result.CompositeMember(3).Usage.Type, branch)
		}
		serial++
		if r.Ordered {
			at := len(fields) - 1
			fields[at], err = sqltype.Integer(r.Result.CompositeMember(at).Usage.Type, serial)
			if err != nil {
				return err
			}
		}
		row, err := sqltype.CompositeValue(r.Result, fields)
		if err != nil {
			return err
		}
		rows = append(rows, row)
		return nil
	}
	if err := emit(&r.Start, nil, 0, r.Start); err != nil {
		return nil, err
	}
	// One SPI consumer serves every child query of this call.
	command := &connectbyCommand[A]{host: h, spi: h}
	queryChildren := func(key string) (connectbyChildren, error) {
		var value [1]sqltype.Value
		value[0] = sqltype.String(r.Text, key)
		literal, err := functions.FormatText(ctx, "%L", functions.FormatValues{Ordinary: value[:]}, r.Result.CompositeMember(2).Usage.Type, output)
		if err != nil {
			return connectbyChildren{}, err
		}
		text := "SELECT " + r.Key + ", " + r.Parent + " FROM " + r.Relation + " WHERE " + r.Parent + " = " + literal + " AND " + r.Key + " IS NOT NULL AND " + r.Key + " <> " + r.Parent
		if r.Ordered {
			text += " ORDER BY " + r.Order
		}
		children, err := connectbyQuery(command, text)
		if err != nil {
			return connectbyChildren{}, err
		}
		if len(children.rows) > 0 {
			if err := validateConnectbySource(r.Result, children.schema); err != nil {
				return connectbyChildren{}, err
			}
		}
		return children, nil
	}
	children, err := queryChildren(r.Start)
	if err != nil {
		return nil, err
	}
	stack := []connectbyFrame{{children: children, level: 1, branch: r.Start}}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		frame := &stack[len(stack)-1]
		if frame.index == len(frame.children.rows) {
			stack = stack[:len(stack)-1]
			continue
		}
		child := frame.children.rows[frame.index]
		frame.index++
		var key, parent *string
		if !child[0].IsNull() {
			text, err := child[0].Type().OutputWithEnvironment(ctx, child[0], output)
			if err != nil {
				return nil, err
			}
			key = &text
		}
		if !child[1].IsNull() {
			text, err := child[1].Type().OutputWithEnvironment(ctx, child[1], output)
			if err != nil {
				return nil, err
			}
			parent = &text
		}
		branch := frame.branch
		if key != nil {
			if strings.Contains(r.Delimiter+branch+r.Delimiter, r.Delimiter+*key+r.Delimiter) {
				return nil, sqlError("42P19", "infinite recursion detected")
			}
			branch += r.Delimiter + *key
		}
		level := frame.level
		if err := emit(key, parent, level, branch); err != nil {
			return nil, err
		}
		if key == nil || r.MaxDepth > 0 && level >= r.MaxDepth {
			continue
		}
		children, err := queryChildren(*key)
		if err != nil {
			return nil, err
		}
		stack = append(stack, connectbyFrame{children: children, level: level + 1, branch: branch})
	}
	return rows, nil
}

// Each nested statement uses the real binder/executor, the current private
// catalog and the outer coherent data view. Only the last SELECT supplies rows.
func connectbyQuery[A Authority](command *connectbyCommand[A], text string) (children connectbyChildren, err error) {
	spi := command.spi
	if err = spi.EnterNestedSQL(); err != nil {
		return children, err
	}
	defer spi.LeaveNestedSQL()
	defer func() {
		err = exec.RewriteSQLDiagnostic(err, text)
		if err != nil {
			children = connectbyChildren{}
		}
	}()
	read, statements, err := spi.ParseNestedSQL(command.host.Context(), text)
	if err != nil {
		return children, err
	}
	// Nested sources borrow the saved outer frontier, not its fixed set of
	// admitted source members.
	request := exec.NestedRequest{Caller: "connectby", ReadOnly: true, BorrowSource: true, CloseFailed: true}
	for _, statement := range statements {
		command.children, command.stop = connectbyChildren{}, false
		if err := spi.ExecuteNestedSQL(statement, read, request, command); err != nil {
			return children, err
		}
		children = command.children
		if command.stop {
			break
		}
	}
	return children, nil
}

// connectbyCommand is one command of connectby's source SQL, run as
// read-only SPI.
type connectbyCommand[A Authority] struct {
	host     Host[A]
	spi      exec.NestedSQL
	children connectbyChildren
	stop     bool
}

func (c *connectbyCommand[A]) NestedCommand(statement parser.Statement) (bool, error) {
	// SPI rejects transaction commands and frontend COPY with a negative
	// result, terminating the list without throwing an SQL error. connectby
	// consumes only SPI_OK_SELECT, so that outcome supplies no children.
	switch command := statement.(type) {
	case *parser.TransactionControl, *parser.SavepointControl:
		c.stop = true
		return true, nil
	case *parser.Copy:
		if command.Endpoint != parser.CopyFile && command.Endpoint != parser.CopyProgram {
			c.stop = true
			return true, nil
		}
	}
	// Utility plans are not executed (or execution-validated) in read-only
	// SPI. Optimizable commands must still undergo normal analysis first.
	switch statement.(type) {
	case *parser.Select, *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
	default:
		return false, sqlError("0A000", c.spi.NestedCommandTag(statement)+" is not allowed in a non-volatile function")
	}
	return false, nil
}

func (c *connectbyCommand[A]) NestedAnalyzed(parser.Statement) error { return nil }

func (c *connectbyCommand[A]) NestedRows(statement parser.Statement, rows exec.RowResult) (err error) {
	if rows != nil {
		defer func() {
			if closeErr := rows.Close(); err == nil {
				err = closeErr
			}
		}()
	}
	selecting := false
	switch command := statement.(type) {
	case *parser.Select:
		selecting = command.Into == nil
	}
	if !selecting || rows == nil {
		return nil
	}
	schema := rows.Schema()
	kept := len(schema)
	if kept > 2 {
		kept = 2
	}
	c.children.schema = append([]catalog.Column(nil), schema[:kept]...)
	buffer := make([]sqltype.Value, len(schema))
	host := c.host
	for {
		if err = host.Context().Err(); err != nil {
			return err
		}
		present, nextErr := rows.Next(host.Context(), buffer)
		if nextErr != nil {
			return nextErr
		}
		if !present {
			break
		}
		var row [2]sqltype.Value
		copy(row[:], buffer)
		c.children.rows = append(c.children.rows, row)
	}
	return nil
}
