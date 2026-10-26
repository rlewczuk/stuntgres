package core_test

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPLpgSQLSQLExecutionOwnership(t *testing.T) {
	t.Run("prepared_publication", sqlExecutionPreparedOwnership)
	for _, command := range []string{"PERFORM x", "PERFORM missing", "PERFORM 1/0", "SELECT x"} {
		t.Run(command, func(t *testing.T) {
			s, d, f, read := loopUnitFrame(t, "DECLARE x integer; BEGIN "+command+"; RETURN 7; END")
			p := d.SQL.PL
			body := p.Root.Statements[0].Block
			f.Enter(p.Root)
			defer f.Leave(p.Root)
			f.Enter(body)
			defer f.Leave(body)
			integer, _ := read.LookupType(sqltype.Int4)
			value, _ := sqltype.Integer(integer, 4)
			f.Values[body.Bindings["x"]] = value
			in := body.Statements[0]
			syntax, checked := *in.Syntax.SQL, *in.SQL
			run := core.ScopeNewPLRun(s, d, f, read)
			expected, completed, depth := s.Expected, s.Completed, core.ScopeActiveSQLRoutineDepth(s)
			demand := core.ScopeSQLRoutine(s)
			for range 2 {
				err := run.Host.Command(run, f, in)
				if (command == "PERFORM x") != (err == nil) {
					t.Fatal("reached command result", command, err)
				}
				if s.Expected != expected || s.Completed != completed || core.ScopeActiveSQLRoutineDepth(s) != depth || !core.SameSQLRoutineDemand(core.ScopeSQLRoutine(s), demand) {
					t.Fatal("command retained lexical/binding/authority/completion scratch", err)
				}
				if f.Values[body.Bindings["x"]] != value || *in.Syntax.SQL != syntax || *in.SQL != checked || p != d.SQL.PL || p.ValidateDefinition(d.SQL) != nil {
					t.Fatal("reached binding mutated immutable source/program or input")
				}
			}
			input := proc.NewFrame(nil)
			defer input.Close()
			other := proc.NewPLFrame(p, d.SQL, input, sqltype.CollationRef{})
			defer other.Close()
			if &other.Values[0] == &f.Values[0] || other.Program != f.Program {
				t.Fatal("frame copied program or shared values")
			}
			other.Values[p.FoundSlot] = sqltype.Boolean(p.Variables[p.FoundSlot].Usage.Type, false)
			if reflect.DeepEqual(other.Values, f.Values) {
				t.Fatal("private values overwritten")
			}
		})
	}
}

type sqlExecutionCloseRows struct {
	exec.RowResult
	count  int
	err    error
	before func()
}

func (r *sqlExecutionCloseRows) Close() error {
	r.count++
	if r.before != nil {
		r.before()
	}
	return errors.Join(r.RowResult.Close(), r.err)
}

func TestPLpgSQLSQLExecutionCleanup(t *testing.T) {
	s, _, frame, read := loopUnitFrame(t, "BEGIN RETURN 7; END")
	integer, _ := read.LookupType(sqltype.Int4)
	one, _ := sqltype.Integer(integer, 1)
	two, _ := sqltype.Integer(integer, 2)
	three, _ := sqltype.Integer(integer, 3)
	schema := []catalog.Column{{Name: "i", Usage: integer.Usage()}}
	late := &sqlstate.Error{Code: "22012", Message: "division by zero"}
	for _, fail := range []bool{false, true} {
		evaluated := 0
		projection := queryProbe(integer.Usage(), func(c *exec.Context) (sqltype.Value, error) {
			evaluated++
			if fail && evaluated == 3 {
				return sqltype.Value{}, late
			}
			return (plan.ColumnExpr{Info: exprInfo(integer.Usage()), Scope: 1, Frame: 1, Slot: 0}).Eval(c)
		})
		producer, _ := queryLoopUnitRows(t, schema, [][]sqltype.Value{{one}, {two}, {three}}, projection)
		primaryDemand := core.ScopeSQLRoutine(s)
		restore := s.ActivateSQLRoutine(nil, read)
		core.SetRoutineDemandLexical(core.ScopeSQLRoutineRef(s), frame)
		closedInActivation := false
		closeErr := errors.New("producer cleanup")
		rows := &sqlExecutionCloseRows{RowResult: producer, err: closeErr, before: func() { closedInActivation = core.RoutineDemandLexical(core.ScopeSQLRoutineRef(s)) == frame }}
		present, err := core.DrainPLSQL(context.Background(), rows)
		if evaluated != 3 || fail && (!errors.Is(err, late) || present) || !fail && (err != nil || !present) {
			t.Fatal("bounded/partial discard or late error lost", evaluated, present, err)
		}
		err = core.ClosePLCapture(core.Result{Rows: rows}, err, func(e error) error { return e })
		err = restore(err)
		var fatal *proc.PLExceptionCleanupError
		if !errors.Is(err, closeErr) || fail && !errors.Is(err, late) || !errors.As(err, &fatal) || rows.count != 1 || !closedInActivation || !core.SameSQLRoutineDemand(core.ScopeSQLRoutine(s), primaryDemand) {
			t.Fatal("cleanup cause/identity/order or scratch lost", err)
		}
	}
	// Cancellation is admitted by the real projection, joined before inspection.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	admitted, release, joined := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	projection := queryProbe(integer.Usage(), func(*exec.Context) (sqltype.Value, error) { close(admitted); <-release; return one, nil })
	producer, _ := queryLoopUnitRows(t, schema, [][]sqltype.Value{{one}, {two}}, projection)
	go func() {
		_, err := core.DrainPLSQL(ctx, producer)
		joined <- core.ClosePLCapture(core.Result{Rows: producer}, err, func(e error) error { return e })
	}()
	<-admitted
	cancel()
	close(release)
	if err := <-joined; !errors.Is(err, context.Canceled) || !producer.closed {
		t.Fatal("admitted cancellation/close/join", err)
	}
}

// Keep the actual column's row-dependent provenance, not a constant projection.
func exprInfo(usage sqltype.Usage) expr.Info {
	return expr.Info{Usage: usage, Property: expr.Properties{RowDependent: true}}
}

func TestPLpgSQLSQLExecutionCopyback(t *testing.T) {
	s, d, f, read := loopUnitFrame(t, "DECLARE a integer; b integer NOT NULL:=20; r record; BEGIN RETURN 7; END")
	p := d.SQL.PL
	body := p.Root.Statements[0].Block
	f.Enter(p.Root)
	defer f.Leave(p.Root)
	f.Enter(body)
	defer f.Leave(body)
	integer, _ := read.LookupType(sqltype.Int4)
	ten, _ := sqltype.Integer(integer, 10)
	twenty, _ := sqltype.Integer(integer, 20)
	seven, _ := sqltype.Integer(integer, 7)
	a, b := body.Bindings["a"], body.Bindings["b"]
	f.Values[a], f.Values[b] = ten, twenty
	run := core.ScopeNewPLRun(s, d, f, read)
	targets := []proc.PLTarget{{Slot: a}, {Slot: b}}
	values := []sqltype.Value{seven, sqltype.Null(integer)}
	captured := append([]sqltype.Value(nil), f.Values...)
	at := proc.PLOrigin{Position: body.Statements[0].Syntax.Pos, Action: "at CALL"}
	for i, target := range targets {
		err := run.Host.AssignValue(f, target, values[i], at)
		if i == 0 && err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			var state interface{ SQLState() string }
			if !errors.As(err, &state) || state.SQLState() != "22004" {
				t.Fatal("late assignment failure", err)
			}
		}
	}
	if f.Values[a] != seven || f.Values[b] != twenty || captured[a] != ten || captured[b] != twenty {
		t.Fatal("collective staging or failed individual publication")
	}
	// The same production target assignment rereads the current root every time.
	for _, v := range []sqltype.Value{twenty, seven} {
		if err := run.Host.AssignValue(f, targets[0], v, at); err != nil {
			t.Fatal(err)
		}
	}
	if f.Values[a] != seven {
		t.Fatal("repeated destination did not retain last successful output")
	}
	input := proc.NewFrame(nil)
	detached, err := input.Result(seven, integer.Usage())
	input.Close()
	if err != nil {
		t.Fatal(err)
	}
	if detached != seven {
		t.Fatal("immutable output did not survive callee frame close")
	}
	varchar, _ := read.LookupType(sqltype.VarChar)
	text, _ := read.LookupType(sqltype.Text)
	limited := varchar.Usage()
	limited.Typmod = 7
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "a", Usage: integer.Usage()}, {Attribute: 2, Name: "b", Usage: limited}, {Attribute: 3, Name: "keep", Usage: text.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	row, err := sqltype.OwnedCompositeValue(shape, []sqltype.Value{ten, sqltype.String(varchar, "old"), sqltype.String(text, "keep")})
	if err != nil {
		t.Fatal(err)
	}
	slot := body.Bindings["r"]
	f.Values[slot] = row
	field := func(name string) proc.PLTarget {
		return proc.PLTarget{Slot: slot, Steps: []parser.TargetStep{{Field: &parser.Identifier{Text: name}}}}
	}
	if err := run.Host.AssignValue(f, field("a"), seven, at); err != nil {
		t.Fatal(err)
	}
	preceding := f.Values[slot]
	err = run.Host.AssignValue(f, field("b"), sqltype.String(varchar, "long"), at)
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "22001" || f.Values[slot] != preceding || f.Values[slot].CompositeField(0) != seven || f.Values[slot].CompositeField(2) != row.CompositeField(2) || row.CompositeField(0) != ten {
		t.Fatal("failed field changed ancestors/siblings or lost earlier output", err)
	}
	if err := run.Host.AssignValue(f, field("b"), sqltype.String(varchar, "new"), at); err != nil {
		t.Fatal(err)
	}
	if f.Values[slot].CompositeField(0) != seven || f.Values[slot] == row || row.CompositeField(1) != sqltype.String(varchar, "old") {
		t.Fatal("overlapping field outputs reused stale root or mutated source")
	}
	if p.ValidateDefinition(d.SQL) != nil || body.Bindings["b"] != b {
		t.Fatal("copyback changed checked declaration association")
	}
}
