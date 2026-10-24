package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

func queryLoopUnitRows(t *testing.T, schema []catalog.Column, values [][]sqltype.Value, projection expr.Expr) (*nestedQueryRows, *exec.Source) {
	t.Helper()
	root := &plan.ProjectNode{QuerySchema: schema, Child: &plan.SourceNode{QuerySchema: schema}, Frame: 1, Expressions: []expr.Expr{projection}}
	// A nonnil anonymous slice denotes a captured source, including zero rows;
	// nil instead denotes a physical source requiring an admitted view.
	if values == nil {
		values = [][]sqltype.Value{}
	}
	source := &exec.Source{Anonymous: values}
	rows, err := openNestedQueryRows(context.Background(), root, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	return rows.(*nestedQueryRows), source
}

func TestPLpgSQLQueryLoopLifetime(t *testing.T) {
	t.Run("retained_cte", queryLoopCTELifetime)
	t.Run("fetch_and_assignment_failure", queryLoopDemandFailure)
	for _, body := range []string{"", "CONTINUE;", "EXIT;", "EXIT outer_block;", "RETURN 7;", "r.i:=r.i+0;", "r.i:=1/0;"} {
		t.Run(body, func(t *testing.T) {
			s, d, f, read := loopUnitFrame(t, `<<outer_block>> DECLARE r record; BEGIN FOR r IN SELECT 1 AS i LOOP `+body+` END LOOP; RETURN 7; END outer_block`)
			outer := d.SQL.PL.Root.Statements[0].Block
			f.Enter(d.SQL.PL.Root)
			defer f.Leave(d.SQL.PL.Root)
			f.Enter(outer)
			defer f.Leave(outer)
			in := outer.Statements[0]
			integer, _ := read.LookupType(sqltype.Int4)
			one, _ := sqltype.Integer(integer, 1)
			two, _ := sqltype.Integer(integer, 2)
			schema := []catalog.Column{{Name: "i", Usage: integer.Usage()}}
			var retained sqltype.Value
			// Column evaluation uses the real Stage's bound source/index path.
			projection := queryProbe(integer.Usage(), func(c *exec.Context) (sqltype.Value, error) {
				current := f.Values[in.QueryLoop.Target.Slot]
				if retained.Type() != nil {
					if got, _ := retained.CompositeField(0).Integer(); got != 1 {
						t.Fatal("published record backing reused", got)
					}
				}
				if current.Type() != nil {
					retained = current
				}
				return (plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 0}).Eval(c)
			})
			rows, source := queryLoopUnitRows(t, schema, [][]sqltype.Value{{one}, {two}}, projection)
			owner := &exec.CTEInvocation{Frame: 1}
			owned := &cteOwnedRows{RowResult: rows, owner: owner, source: source}
			restore := s.activateSQLRoutine(nil, read)
			s.sqlRoutine.lexical, s.sqlRoutine.scalar, s.sqlRoutine.rows = f, false, true
			iterated := false
			_, flow, err := s.consumePLQuery(d, f, in, read, owned, &iterated)
			if retained.Type() != nil {
				if got, _ := retained.CompositeField(0).Integer(); got != 1 {
					t.Fatal("later row mutated retained first row", got)
				}
			}
			closeErr := owned.Close()
			err = restore(err)
			if body == "r.i:=1/0;" {
				var state interface{ SQLState() string }
				if !errors.As(err, &state) || state.SQLState() != "22012" {
					t.Fatal("body failure lost", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !iterated || closeErr != nil || !owner.Closed || !rows.closed || s.sqlRoutine.lexical != nil {
				t.Fatal("producer/lexical lifetime", iterated, flow, err, closeErr)
			}
			if err := owned.Close(); err != nil {
				t.Fatal("repeat close", err)
			}
			if body == "RETURN 7;" && flow.Kind != proc.PLReturn {
				t.Fatal("return token lost", flow)
			}
			if body == "EXIT;" && flow.Kind != proc.PLNormal {
				t.Fatal("own EXIT escaped", flow)
			}
			if body == "EXIT outer_block;" && (flow.Kind != proc.PLExit || flow.Target != outer.Control) {
				t.Fatal("outward owner token lost", flow)
			}
		})
	}
	t.Run("empty_and_detached_rows", func(t *testing.T) {
		s, d, f, read := loopUnitFrame(t, `DECLARE r record; BEGIN FOR r IN SELECT 1 AS i LOOP END LOOP; RETURN 7; END`)
		outer := d.SQL.PL.Root.Statements[0].Block
		f.Enter(d.SQL.PL.Root)
		defer f.Leave(d.SQL.PL.Root)
		f.Enter(outer)
		defer f.Leave(outer)
		in := outer.Statements[0]
		integer, _ := read.LookupType(sqltype.Int4)
		schema := []catalog.Column{{Name: "i", Usage: integer.Usage()}}
		projection := plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 0}
		rows, _ := queryLoopUnitRows(t, schema, nil, projection)
		iterated := false
		_, _, err := s.consumePLQuery(d, f, in, read, rows, &iterated)
		if err != nil || iterated {
			t.Fatal("empty demand", iterated, err)
		}
		target := f.Values[in.QueryLoop.Target.Slot]
		if target.Type() == nil || target.CompositeLen() != 1 || target.Type().CompositeMember(0).Name != "i" || !target.CompositeField(0).IsNull() {
			t.Fatal("empty lost descriptor-shaped NULL", target)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("close_failure", func(t *testing.T) {
		integer := sqltype.Builtins()[0]
		for _, d := range sqltype.Builtins() {
			if d.ID() == sqltype.Int4 {
				integer = d
			}
		}
		schema := []catalog.Column{{Name: "i", Usage: integer.Usage()}}
		value, _ := sqltype.Integer(integer, 1)
		rows, source := queryLoopUnitRows(t, schema, [][]sqltype.Value{{value}}, plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage()}, Scope: 1, Frame: 1, Slot: 0})
		failure := errors.New("source cleanup veto")
		rows.stage.Finish = func() error { return failure }
		owner := &exec.CTEInvocation{Frame: 1}
		owned := &cteOwnedRows{RowResult: rows, owner: owner, source: source}
		if err := owned.Close(); !errors.Is(err, failure) || !owner.Closed || !rows.closed {
			t.Fatal("cleanup failure lost ownership", err)
		}
		if err := owned.Close(); err != nil {
			t.Fatal("duplicate cleanup", err)
		}
	})
}

type queryLoopAssignedCancel struct {
	context.Context
	cancel   context.CancelFunc
	assigned func() bool
}

func (c queryLoopAssignedCancel) Err() error {
	if c.assigned() {
		c.cancel()
	}
	return c.Context.Err()
}

func TestPLpgSQLQueryLoopCancellation(t *testing.T) {
	s, d, f, read := loopUnitFrame(t, `DECLARE r record; BEGIN FOR r IN SELECT 1 AS i LOOP END LOOP; RETURN 7; END`)
	outer := d.SQL.PL.Root.Statements[0].Block
	f.Enter(d.SQL.PL.Root)
	defer f.Leave(d.SQL.PL.Root)
	f.Enter(outer)
	defer f.Leave(outer)
	in := outer.Statements[0]
	integer, _ := read.LookupType(sqltype.Int4)
	value, _ := sqltype.Integer(integer, 1)
	ctx, cancel := context.WithCancel(s.op.Context())
	defer cancel()
	originalContext := s.op.Ctx
	s.op.Ctx = queryLoopAssignedCancel{Context: ctx, cancel: cancel, assigned: func() bool { return f.Values[in.QueryLoop.Target.Slot].Type() != nil }}
	rows, source := queryLoopUnitRows(t, []catalog.Column{{Name: "i", Usage: integer.Usage()}}, [][]sqltype.Value{{value}, {value}}, plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 0})
	owner := &exec.CTEInvocation{Frame: 1}
	owned := &cteOwnedRows{RowResult: rows, owner: owner, source: source}
	restore := s.activateSQLRoutine(nil, read)
	s.sqlRoutine.lexical, s.sqlRoutine.rows = f, true
	iterated := false
	_, flow, err := s.consumePLQuery(d, f, in, read, owned, &iterated)
	closeErr := owned.Close()
	err = restore(err)
	s.op.Ctx = originalContext
	if !iterated || !errors.Is(err, context.Canceled) || flow.Kind != proc.PLNormal || closeErr != nil || !owner.Closed || !rows.closed || s.sqlRoutine.lexical != nil {
		t.Fatal("empty-body cancellation retained producer authority", flow, err, closeErr)
	}
	// The same scope/frame can acquire fresh lexical demand after cancellation.
	restore = s.activateSQLRoutine(nil, read)
	s.sqlRoutine.lexical = f
	if err := restore(nil); err != nil || s.sqlRoutine.lexical != nil {
		t.Fatal("caller scratch did not recover", err)
	}
}
