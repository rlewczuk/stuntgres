package proc

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// walkHost is a fake PLHost. It evaluates integer/boolean/NULL literals,
// variables, one-level record fields and a few integer operators, and records
// every host call it receives.
type walkHost struct {
	t                *testing.T
	ctx              context.Context
	integer, boolean *sqltype.Descriptor
	calls            []string
	rows             *walkRows
	beginErr         error
	endErr           error
	// onEvaluate runs before each evaluation.
	onEvaluate func(f *PLFrame)
	// seen keeps every record value a field reference read.
	seen []sqltype.Value
}

var _ PLHost = (*walkHost)(nil)

func newWalkHost(t *testing.T) *walkHost {
	return &walkHost{t: t, ctx: context.Background(), integer: plUnitInteger(t), boolean: plUnitBuiltin(t, sqltype.Bool)}
}

func (h *walkHost) Context() context.Context { return h.ctx }

func (h *walkHost) Identity() string { return "walk_unit()" }
func (h *walkHost) EnterRoutine()    {}
func (h *walkHost) LeaveRoutine()    {}
func (h *walkHost) Diagnostics(*PLFrame, PLInstruction) error {
	return fmt.Errorf("walk unit has no diagnostic source")
}

func (h *walkHost) TypeName(typ *sqltype.Descriptor) string { return typ.DiagnosticName() }

func (h *walkHost) Capture(*PLRun, *PLFrame, PLInstruction) error {
	return fmt.Errorf("walk unit has no SQL capture producer")
}

func (h *walkHost) Command(*PLRun, *PLFrame, PLInstruction) error {
	return fmt.Errorf("walk unit has no SQL command producer")
}

func (h *walkHost) Transaction(PLInstruction) error {
	return fmt.Errorf("walk unit has no transaction owner")
}

func (h *walkHost) Dynamic(*PLRun, *PLFrame, PLInstruction) error {
	return fmt.Errorf("walk unit has no dynamic command producer")
}

func (h *walkHost) LookupType(id sqltype.ID) (*sqltype.Descriptor, bool) {
	switch id {
	case sqltype.Bool:
		return h.boolean, true
	case sqltype.Int4:
		return h.integer, true
	}
	return nil, false
}

func (h *walkHost) Evaluate(f *PLFrame, q *parser.Select, at PLOrigin) (sqltype.Value, sqltype.Usage, error) {
	h.calls = append(h.calls, "evaluate "+at.Action)
	if h.onEvaluate != nil {
		h.onEvaluate(f)
	}
	v, err := h.eval(f, q.Targets[0])
	if err != nil {
		return sqltype.Value{}, sqltype.Usage{}, err
	}
	return v, v.Type().Usage(), nil
}

func (h *walkHost) eval(f *PLFrame, e parser.Expr) (sqltype.Value, error) {
	switch e := e.(type) {
	case parser.LiteralExpr:
		switch e.Value.Kind {
		case parser.IntegerValue:
			return sqltype.Integer(h.integer, e.Value.Integer)
		case parser.BooleanValue:
			return sqltype.Boolean(h.boolean, e.Value.Boolean), nil
		case parser.NullValue:
			return sqltype.Null(h.integer), nil
		}
	case parser.ColumnExpr:
		parts := e.Name.Parts
		v := f.Values[h.slot(f, parts[0].Text)]
		if len(parts) == 1 {
			return v, nil
		}
		h.seen = append(h.seen, v)
		return v.CompositeField(0), nil
	case parser.OperatorExpr:
		var operands [2]int64
		for i, arg := range e.Args {
			v, err := h.eval(f, arg)
			if err != nil {
				return sqltype.Value{}, err
			}
			if operands[i], err = v.Integer(); err != nil {
				return sqltype.Value{}, err
			}
		}
		a, b := operands[0], operands[1]
		switch e.Name.Parts[0].Text {
		case "+":
			return sqltype.Integer(h.integer, a+b)
		case "-":
			return sqltype.Integer(h.integer, a-b)
		case "*":
			return sqltype.Integer(h.integer, a*b)
		case "/":
			if b == 0 {
				return sqltype.Value{}, plError("22012", "division by zero")
			}
			return sqltype.Integer(h.integer, a/b)
		case "=":
			return sqltype.Boolean(h.boolean, a == b), nil
		case ">=":
			return sqltype.Boolean(h.boolean, a >= b), nil
		}
	}
	h.t.Fatalf("walk unit cannot evaluate %T", e)
	return sqltype.Value{}, nil
}

func (h *walkHost) slot(f *PLFrame, name string) int {
	for i := len(f.Program.Variables) - 1; i >= 0; i-- {
		if f.Program.Variables[i].Name == name {
			return i
		}
	}
	h.t.Fatalf("walk unit has no variable %q", name)
	return 0
}

func (h *walkHost) Convert(v sqltype.Value, target sqltype.Usage, at PLOrigin) (sqltype.Value, error) {
	h.calls = append(h.calls, "convert "+at.Action)
	if v.IsNull() {
		return sqltype.Null(target.Type), nil
	}
	if v.Type().ID() != target.Type.ID() {
		return sqltype.Value{}, plError("42804", fmt.Sprintf("cannot convert %s", v.Type().Name()))
	}
	return v, nil
}

func (h *walkHost) AssignTarget(*PLFrame, PLTarget, parser.Expr, *parser.Select, PLOrigin) error {
	h.t.Fatal("walk unit has no field targets")
	return nil
}

func (h *walkHost) TargetUsage(*PLFrame, PLTarget) (sqltype.Usage, error) {
	h.t.Fatal("walk unit has no partial target usages")
	return sqltype.Usage{}, nil
}

func (h *walkHost) AssignValue(*PLFrame, PLTarget, sqltype.Value, PLOrigin) error {
	h.t.Fatal("walk unit has no partial value targets")
	return nil
}

type walkCheckpoint struct{ id int }

func (h *walkHost) BeginException() (PLCheckpoint, error) {
	h.calls = append(h.calls, "begin")
	if h.beginErr != nil {
		return nil, h.beginErr
	}
	return &walkCheckpoint{id: len(h.calls)}, nil
}

func (h *walkHost) EndException(c PLCheckpoint, rollback bool, primary error) error {
	if _, ok := c.(*walkCheckpoint); !ok {
		h.t.Fatalf("foreign checkpoint %T", c)
	}
	h.calls = append(h.calls, fmt.Sprintf("end rollback=%v", rollback))
	if h.endErr != nil {
		return &PLExceptionCleanupError{Primary: primary, Cleanup: h.endErr}
	}
	return nil
}

func (h *walkHost) OpenQuery(f *PLFrame, in PLInstruction) (PLRows, error) {
	h.calls = append(h.calls, "open")
	if h.rows == nil {
		return nil, plError("42P01", "no rows")
	}
	return h.rows, nil
}

func (h *walkHost) Cursor(*PLRun, *PLFrame, PLInstruction) (sqltype.Value, PLFlow, error) {
	h.t.Fatal("walk unit has no cursors")
	return sqltype.Value{}, PLFlow{}, nil
}

func (h *walkHost) Message(f *PLFrame, in PLInstruction) error {
	if in.Raise != nil {
		h.calls = append(h.calls, "raise")
	} else {
		h.calls = append(h.calls, "assert")
	}
	return nil
}

// walkRows is a fake one-column ("i" integer) query-FOR producer.
type walkRows struct {
	host     *walkHost
	values   []int64
	next     int
	nextErr  error
	closeErr error
	closed   int
	primary  error
	// atClose runs at the start of Close, while the loop target is in scope.
	atClose func()
}

func (r *walkRows) Width() int { return 1 }

func (r *walkRows) Column(int) (string, sqltype.Usage) { return "i", r.host.integer.Usage() }

func (r *walkRows) Next(ctx context.Context, dst []sqltype.Value) (bool, error) {
	if r.nextErr != nil {
		return false, r.nextErr
	}
	if r.next == len(r.values) {
		return false, nil
	}
	v, err := sqltype.Integer(r.host.integer, r.values[r.next])
	dst[0] = v
	r.next++
	return true, err
}

func (r *walkRows) Close(primary error) error {
	r.host.calls = append(r.host.calls, "close")
	if r.atClose != nil {
		r.atClose()
	}
	r.closed++
	r.primary = primary
	if primary == nil && r.closeErr != nil {
		return r.closeErr
	}
	return primary
}

func walkUnitRun(t *testing.T, h *walkHost, source string) *PLRun {
	t.Helper()
	d := walkUnitDefinition(t, source)
	f := NewPLFrame(d.PL, d, NewFrame(nil), sqltype.CollationRef{})
	t.Cleanup(f.Close)
	return &PLRun{Host: h, Definition: d, Frame: f}
}

func walkState(err error) string {
	var diagnostic interface{ SQLState() string }
	if errors.As(err, &diagnostic) {
		return diagnostic.SQLState()
	}
	return ""
}

// walkUnitDefinition compiles and validates source as an integer function.
// Types resolve to integer except boolean, text and record.
func walkUnitDefinition(t *testing.T, source string) *Definition {
	t.Helper()
	integer, text := plUnitInteger(t), plUnitBuiltin(t, sqltype.Text)
	d := &Definition{Kind: PLpgSQLBody, Name: "walk_unit", Result: integer.Usage(), Source: source}
	raw, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	d.PLSyntax = raw
	resolve := plUnitResolver(t, integer.Usage())
	d.PL, err = CompilePL(d, raw, false, plUnitCollation(t), func(raw parser.PLType) (sqltype.Usage, bool, error) {
		switch raw.Type.Name {
		case "record":
			return sqltype.Usage{}, true, nil
		case "text":
			return text.Usage(), false, nil
		}
		return resolve(raw)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.PL.ValidateDefinition(d); err != nil {
		t.Fatal(err)
	}
	return d
}
