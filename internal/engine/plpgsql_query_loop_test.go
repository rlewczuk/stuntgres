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

// queryLoopProducer records whether the loop's owner closed the borrowed producer.
type queryLoopProducer struct {
	exec.RowResult
	closed bool
}

func (p *queryLoopProducer) Close() error {
	err := p.RowResult.Close()
	p.closed = true
	return err
}

func queryLoopUnitRows(t *testing.T, schema []catalog.Column, values [][]sqltype.Value, projection expr.Expr) (*queryLoopProducer, *exec.Source) {
	t.Helper()
	root := &plan.ProjectNode{QuerySchema: schema, Child: &plan.SourceNode{QuerySchema: schema}, Frame: 1, Expressions: []expr.Expr{projection}}
	// A nonnil anonymous slice denotes a captured source, including zero rows;
	// nil instead denotes a physical source requiring an admitted view.
	if values == nil {
		values = [][]sqltype.Value{}
	}
	source := &exec.Source{Anonymous: values}
	rows, err := exec.OpenNestedQueryRows(context.Background(), root, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &queryLoopProducer{RowResult: rows}, source
}

func TestPLpgSQLQueryLoopLifetime(t *testing.T) {
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
			owned := &exec.CTEOwnedRows{RowResult: rows, Owner: owner, Source: source}
			_, flow, err := plQueryLoopTestRun(s, d, f, read, owned).Statements(outer.Statements[:1])
			if retained.Type() != nil {
				if got, _ := retained.CompositeField(0).Integer(); got != 1 {
					t.Fatal("later row mutated retained first row", got)
				}
			}
			if body == "r.i:=1/0;" {
				var state interface{ SQLState() string }
				if !errors.As(err, &state) || state.SQLState() != "22012" {
					t.Fatal("body failure lost", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			found, _ := f.Values[f.Program.FoundSlot].Boolean()
			if found != (body != "r.i:=1/0;") || f.Values[in.QueryLoop.Target.Slot].Type() == nil || !owner.Closed || !rows.closed || RoutineDemandLexical(ScopeSQLRoutineRef(s)) != nil {
				t.Fatal("producer/lexical lifetime", found, flow, err)
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
		_, _, err := plQueryLoopTestRun(s, d, f, read, rows).Statements(outer.Statements[:1])
		if found, _ := f.Values[f.Program.FoundSlot].Boolean(); err != nil || found || !rows.closed {
			t.Fatal("empty demand", found, err)
		}
		target := f.Values[in.QueryLoop.Target.Slot]
		if target.Type() == nil || target.CompositeLen() != 1 || target.Type().CompositeMember(0).Name != "i" || !target.CompositeField(0).IsNull() {
			t.Fatal("empty lost descriptor-shaped NULL", target)
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
	ctx, cancel := context.WithCancel(s.Op.Context())
	defer cancel()
	originalContext := s.Op.Ctx
	s.Op.Ctx = queryLoopAssignedCancel{Context: ctx, cancel: cancel, assigned: func() bool { return f.Values[in.QueryLoop.Target.Slot].Type() != nil }}
	rows, source := queryLoopUnitRows(t, []catalog.Column{{Name: "i", Usage: integer.Usage()}}, [][]sqltype.Value{{value}, {value}}, plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 0})
	owner := &exec.CTEInvocation{Frame: 1}
	owned := &exec.CTEOwnedRows{RowResult: rows, Owner: owner, Source: source}
	_, flow, err := plQueryLoopTestRun(s, d, f, read, owned).Statements(outer.Statements[:1])
	s.Op.Ctx = originalContext
	if f.Values[in.QueryLoop.Target.Slot].Type() == nil || !errors.Is(err, context.Canceled) || flow.Kind != proc.PLNormal || !owner.Closed || !rows.closed || RoutineDemandLexical(ScopeSQLRoutineRef(s)) != nil {
		t.Fatal("empty-body cancellation retained producer authority", flow, err)
	}
	// The same scope/frame can acquire fresh lexical demand after cancellation.
	restore := ScopeActivateSQLRoutine(s, nil, read)
	SetRoutineDemandLexical(ScopeSQLRoutineRef(s), f)
	if err := restore(nil); err != nil || RoutineDemandLexical(ScopeSQLRoutineRef(s)) != nil {
		t.Fatal("caller scratch did not recover", err)
	}
}

// plQueryLoopTestRun drives query loops over rows in place of the planned
// producer. The real adapter still owns activation, close and FOUND order.
func plQueryLoopTestRun(s *StatementScope, d *catalog.Procedure, f *proc.PLFrame, read catalog.Read, rows exec.RowResult) *proc.PLRun {
	run := ScopeNewPLRun(s, d, f, read)
	run.Host = plInjectedRows{PLHost: run.Host.(*PLHost), rows: rows}
	return run
}

type plInjectedRows struct {
	*PLHost
	rows exec.RowResult
}

func (h plInjectedRows) OpenQuery(f *proc.PLFrame, in proc.PLInstruction) (proc.PLRows, error) {
	q := PLHostBeginQueryRows(h.PLHost, f, in)
	*PLQueryRowsResultRef(q), *PLQueryRowsSchemaRef(q) = Result{Rows: h.rows}, h.rows.Schema()
	return q, nil
}
