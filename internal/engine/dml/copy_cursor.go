package dml

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/copyfmt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// CopyCursor executes text and CSV COPY FROM: it converts the fields its
// line reader produces into target values and attaches COPY diagnostics. It
// reads the operation's COPY input state and the host's input environment at
// every use, as the rows arrive.
type CopyCursor struct {
	op                *session.Operation
	host              Host
	binding           analyze.Binding
	in                copyfmt.LineReader
	defaults, omitted []bool
	column, value     string
	nullInput         bool
	state             *CopyFromState
	evaluation        exec.Context
}

// NewCopyCursor is the text or CSV COPY FROM cursor of b over in.
func NewCopyCursor(h Host, b analyze.Binding, in copyfmt.LineReader, state *CopyFromState) *CopyCursor {
	// One backing array for both row flags offsets the targets allocation.
	n := len(b.Relation.Columns)
	flags := make([]bool, 2*n)
	c := &CopyCursor{op: h.Operation(), host: h, binding: b, in: in, state: state, defaults: flags[:n:n], omitted: flags[n:]}
	c.evaluation = exec.Context{EvalStatement: exec.EvalStatement{Host: h}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	for i := range c.omitted {
		c.omitted[i] = true
	}
	for _, target := range b.Targets {
		c.omitted[target] = false
	}
	return c
}

func (c *CopyCursor) Schema() []catalog.Column { return nil }
func (c *CopyCursor) Close() error             { return nil }
func (c *CopyCursor) input() sqltype.InputEnvironment {
	input, _ := c.host.Environments()
	if c.state != nil && c.state.InputChecks != nil {
		input.DomainChecks = c.state.InputChecks
	}
	return input
}
func (c *CopyCursor) rowDefaults() []bool { return c.defaults }
func (c *CopyCursor) Next(ctx context.Context, dst []sqltype.Value) (bool, error) {
	wireInput := c.op.CopyInput.Active.Load()
	if wireInput {
		ctx = c.op.Context()
	}
	if err := ctx.Err(); err != nil {
		if c.op.CopyInput.Held() && session.PendingProtocolUserCancel(ctx) {
			// User cancellation may arrive after Context selected its live
			// standard context. Refresh only the admitted decoder's view;
			// its owner retains deadlines, terminal and source authority.
			ctx = c.op.Context()
			err = ctx.Err()
		}
		if err != nil {
			return false, c.Diagnostic(err)
		}
	}
	c.column, c.value, c.nullInput = "", "", false
	if c.state != nil {
		c.state.pending = false
	}
	if err := c.in.Header(); err != nil {
		return false, c.Diagnostic(err)
	}
	present, err := c.in.ReadLine()
	if err != nil || !present {
		return false, c.Diagnostic(err)
	}
	if wireInput {
		ctx = c.op.Context()
	}
	if err = c.in.Split(true); err != nil {
		return false, c.Diagnostic(err)
	}
	fields := c.in.Fields()
	if len(fields) > len(c.binding.Targets) {
		return false, c.Diagnostic(copyfmt.LineError("extra data after last expected column", ""))
	}
	copy(c.defaults, c.omitted)
	for i, target := range c.binding.Targets {
		column := c.binding.Relation.Columns[target]
		if i >= len(fields) {
			return false, c.Diagnostic(copyfmt.LineError(fmt.Sprintf("missing data for column \"%s\"", column.Name), ""))
		}
		field := fields[i]
		dst[i] = sqltype.Null(column.Usage.Type)
		if c.in.FieldNull(i) {
			c.column, c.nullInput = column.Name, true
			if err := column.Usage.Type.CheckDomainInput(ctx, dst[i], c.input()); err != nil {
				if c.reject(err) {
					clear(dst[i+1:])
					return true, nil
				}
				return false, c.Diagnostic(err)
			}
			c.column, c.nullInput = "", false
			continue
		}
		c.column = column.Name
		c.nullInput = false
		c.value = string(field.Decoded)
		if field.Default {
			// Selected markers are demanded in COPY-list order, before any
			// later conversion or missing-field error. Use the same default
			// origin/evaluator as insertion, without the input-soft adapter.
			c.evaluation.Context = ctx
			c.evaluation.Input, c.evaluation.Output = c.host.Environments()
			dst[i], err = view.EvalBindingDefault(&c.binding, &c.evaluation, target, c.binding.Insert.Defaults[target])
			if err != nil {
				return false, c.Diagnostic(err)
			}
			c.defaults[target] = false // Already consumed, never run again.
			c.column = ""
			continue
		}
		dst[i], err = column.Usage.Type.InputWithEnvironment(ctx, c.value, column.Usage.Typmod, c.input())
		if err != nil {
			if c.reject(err) {
				clear(dst[i+1:])
				return true, nil
			}
			return false, c.Diagnostic(err)
		}
		c.column = ""
	}
	if err := c.op.PendingCopyInputError(); err != nil {
		return false, c.Diagnostic(err)
	}
	return true, nil
}

// Recovery is confined to fresh selected-field conversion. Defaults, WHERE,
// framing and table constraints never enter this classifier.
func (c *CopyCursor) reject(err error) bool {
	if c.state == nil || c.state.Options.OnError != copyfmt.Ignore || !sqltype.InputSoftError(err) {
		return false
	}
	c.state.pending = true
	if c.state.Options.LogVerbosity == copyfmt.LogVerbose {
		value := "null input"
		if !c.nullInput {
			value = fmt.Sprintf("\"%s\"", copyfmt.ContextValue(c.value))
		}
		message := fmt.Sprintf("skipping row due to data type incompatibility at line %d for column \"%s\": %s", c.in.Number(), c.column, value)
		c.op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message, Context: "COPY " + c.binding.Relation.Name()})
	}
	return true
}

// Diagnostic attaches the COPY line and column context to err.
func (c *CopyCursor) Diagnostic(err error) error {
	if err == nil {
		return nil
	}
	// Map only this operation's SQL-owned cancellation before attaching the
	// active COPY input context; ordinary caller context and I/O errors retain
	// their identity, and relation-opening errors never enter this cursor.
	err = c.host.CopyCommandError(err)
	var record *sqlstate.Error
	if !errors.As(err, &record) {
		return err
	}
	// Only source-marked input failures extend an existing inner context.
	// Other contextual primary errors (including joined cancellation) retain
	// their original diagnostic and wrapper identity.
	if record.Context != "" {
		if _, inputFailure := err.(*sqlstate.InputError); !inputFailure {
			return err
		}
	}
	// Buffered insertion and an earlier cursor failure may already own COPY
	// context. Match the existing binary cursor's annotation convention while
	// retaining inner parser/routine context before this outer COPY frame.
	if strings.Contains(record.Context, "COPY "+c.binding.Relation.Name()+", line ") {
		return err
	}
	result := *record
	result.Context = fmt.Sprintf("COPY %s, line %d", c.binding.Relation.Name(), c.in.Number())
	if c.column != "" {
		if c.nullInput {
			result.Context += fmt.Sprintf(", column %s: null input", c.column)
		} else {
			result.Context += fmt.Sprintf(", column %s: \"%s\"", c.column, copyfmt.ContextValue(c.value))
		}
	} else if c.in.LineValid() && record.Code != "23505" {
		result.Context += fmt.Sprintf(": \"%s\"", copyfmt.ContextValue(string(c.in.Line())))
	}
	if record.Context != "" {
		result.Context = record.Context + "\n" + result.Context
	}
	return sqlstate.InputDiagnostic(err, &result)
}
