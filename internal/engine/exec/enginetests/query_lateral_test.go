package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestQueryLateralInvocation(t *testing.T) {
	t.Run("reopen_values_and_lexical_ancestors", lateralInvocationRows)
	t.Run("written_but_independent", func(t *testing.T) {
		integer := lateralInvocationType(t, sqltype.Int4)
		ancestor := &exec.Context{EvalFrame: exec.EvalFrame{Scope: 1, Frame: 3}}
		invocation := &exec.Context{EvalFrame: exec.EvalFrame{Scope: 1, Frame: 7, Outer: ancestor}}
		for _, lateral := range []bool{false, true} {
			for _, available := range []bool{false, true} {
				state := &exec.State{Source: &exec.Source{}, SourceMode: exec.SourceBorrowed, Frame: 7, Outer: ancestor}
				want := ancestor
				if available {
					state.Invocation = invocation
					if lateral {
						want = invocation
					}
				}
				probe := queryProbe(integer.Usage(), func(c *exec.Context) (sqltype.Value, error) {
					if c.Frame != 8 || c.Outer != want {
						t.Fatal("wrong lexical bridge", c.Frame, c.Outer, want)
					}
					return sqltype.Integer(integer, 42)
				})
				project := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Usage: integer.Usage()}}, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{probe}, Frame: 8}
				n := &plan.DerivedNode{QuerySchema: project.Schema(), Query: &plan.Query{Root: project}, ScanFrame: 7, Lateral: lateral}
				stage, err := exec.OpenDerivedNode(context.Background(), state, n)
				if err != nil {
					t.Fatal(err)
				}
				lateralInvocationPull(t, &stage, 42)
				if err := stage.Close(); err != nil || stage.Source != nil || stage.Finish != nil || state.Frame != 7 || state.Outer != ancestor {
					t.Fatal("child frame escaped", err, state)
				}
			}
		}
	})
	t.Run("zero_width_inherited_identity", lateralInvocationIdentity)
	t.Run("inherited_and_mixed_prefix", lateralInvocationPrefix)
	t.Run("zero_width_inherited_presence", lateralInvocationPresence)
	for _, phase := range []string{"producer", "projection", "close", "cancel"} {
		t.Run("cleanup/"+phase, func(t *testing.T) { lateralInvocationCleanup(t, phase) })
	}
}

func lateralInvocationRows(t *testing.T) {
	integer, boolean := lateralInvocationType(t, sqltype.Int4), lateralInvocationType(t, sqltype.Bool)
	value := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(integer, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	column := func(frame uint32, slot int) plan.ColumnExpr {
		return plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: frame, Slot: slot}
	}
	ancestor := &exec.Context{EvalFrame: exec.EvalFrame{Scope: 1, Frame: 3, Row: []sqltype.Value{value(100)}}}
	for _, values := range []bool{false, true} {
		state := &exec.State{Source: &exec.Source{}, SourceMode: exec.SourceReusable, Frame: 7, Outer: ancestor, Parameters: []sqltype.Value{value(10)}, Methods: plan.PlannerMethods{NoHashJoin: true}}
		leftSchema := plan.QuerySchema{{Name: "id", Usage: integer.Usage()}}
		left := &plan.ValuesNode{QuerySchema: leftSchema, Rows: [][]expr.Expr{}}
		for _, n := range []int64{1, 2, 3} {
			left.Rows = append(left.Rows, []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: value(n)}})
		}
		seen := []int64{}
		read := queryProbe(integer.Usage(), func(c *exec.Context) (sqltype.Value, error) {
			if c.Frame != 8 || c.Outer == nil || c.Outer.Frame != 7 || c.Outer.Outer != ancestor || c.Query.Invocation != c.Outer || c.Query.Source != state.Source || c.Query.Parameters[0] != state.Parameters[0] || c.Query.Methods != state.Methods {
				t.Fatal("lost actual caller frame, lexical ancestor or borrowed authority", c)
			}
			parent, err := column(7, 0).Eval(c)
			if err != nil {
				return sqltype.Value{}, err
			}
			n, err := parent.Integer()
			if err != nil {
				return sqltype.Value{}, err
			}
			outer, err := column(3, 0).Eval(c)
			if err != nil {
				return sqltype.Value{}, err
			}
			a, err := outer.Integer()
			if err != nil {
				return sqltype.Value{}, err
			}
			parameter, err := c.Parameters[0].Integer()
			if err != nil {
				return sqltype.Value{}, err
			}
			seen = append(seen, n)
			return sqltype.Integer(integer, n+a+parameter)
		})
		predicate := queryProbe(boolean.Usage(), func(c *exec.Context) (sqltype.Value, error) {
			v, err := column(7, 0).Eval(c)
			if err != nil {
				return sqltype.Value{}, err
			}
			n, err := v.Integer()
			return sqltype.Boolean(boolean, n != 2), err
		})
		rightSchema := plan.QuerySchema{{Name: "n", Usage: integer.Usage()}}
		var child plan.Node = &plan.SingletonNode{}
		expressions := []expr.Expr{read}
		if values {
			child = &plan.ValuesNode{QuerySchema: rightSchema, Rows: [][]expr.Expr{{read}}}
			expressions = []expr.Expr{column(8, 0)}
		}
		child = &plan.FilterNode{QuerySchema: child.Schema(), Child: child, Predicate: predicate}
		project := &plan.ProjectNode{QuerySchema: rightSchema, Child: child, Expressions: expressions, Frame: 8, FrozenValues: values}
		right := &plan.DerivedNode{QuerySchema: rightSchema, Query: &plan.Query{Root: project}, ScanFrame: 7, Lateral: true, PresenceSlot: 1, Captures: []plan.FunctionCorrelation{{Frame: 7, Slot: 0}}}
		join := &plan.JoinNode{QuerySchema: append(append(plan.QuerySchema{}, leftSchema...), rightSchema...), Left: left, Right: right, LeftWidth: 1, RightWidth: 1, Kind: parser.JoinLeft, CorrelatedRight: true, Frame: 7}
		for replay := range 2 {
			seen = seen[:0]
			stage, err := exec.OpenJoinNode(context.Background(), state, join)
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range []int64{1, 2, 3} {
				i, present, err := stage.NextOccurrence(context.Background())
				if err != nil || !present {
					t.Fatal("missing lateral invocation", replay, n, present, err)
				}
				row := stage.Source.Row(stage.Index(i))
				id, err := row[0].Integer()
				if err != nil || id != n {
					t.Fatal(row, err)
				}
				presence := exec.Context{EvalFrame: exec.EvalFrame{Scope: 1, Frame: 7, PresenceSlots: stage.Source.PresenceSlots, Presence: stage.Source.PresenceRow(stage.Index(i))}}
				absent, err := presence.RowAbsent(plan.RowExpr{PresenceFrame: 7, PresenceSlot: 1})
				if err != nil || absent != (n == 2) {
					t.Fatal("null extension lost occurrence presence", n, absent, err)
				}
				if n == 2 {
					if !row[1].IsNull() {
						t.Fatal("empty right not NULL extended", row)
					}
				} else if result, err := row[1].Integer(); err != nil || result != 110+n {
					t.Fatal(row, err)
				}
			}
			if _, present, err := stage.NextOccurrence(context.Background()); err != nil || present {
				t.Fatal("extra occurrence", present, err)
			}
			if err := stage.Close(); err != nil || stage.Source != nil || stage.Finish != nil || state.Frame != 7 || state.Invocation != nil || state.Outer != ancestor {
				t.Fatal("borrowed invocation escaped close", err, state)
			}
			// VALUES evaluates before its filter; SELECT projects after it.
			want := []int64{1, 3}
			if values {
				want = []int64{1, 2, 3}
			}
			if len(seen) != len(want) {
				t.Fatal("wrong invocation population", values, seen)
			}
			for i := range want {
				if seen[i] != want[i] {
					t.Fatal("reused stale invocation", seen, want)
				}
			}
		}
	}
}

func lateralInvocationIdentity(t *testing.T) {
	oid := lateralInvocationType(t, sqltype.Oid)
	domain := new(ids.Domain)
	ancestorRef := domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 23000})
	localRef := domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 23001})
	ancestor := &exec.Source{Ref: ancestorRef, Occurrence: 7, Anonymous: [][]sqltype.Value{{}}}
	local := &exec.Source{Ref: localRef, Occurrence: 8, Anonymous: [][]sqltype.Value{{}}}
	state := exec.State{Source: &exec.Source{Sources: []*exec.Source{ancestor, local}}, Frame: 1, IdentityReads: true}
	state.Invocation = &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Query: &state, QueryOccurrences: []int{7}, QueryProvenance: []int{0}}}
	first := plan.SystemAttributeExpr{SubID: -6, Info: expr.Info{Usage: oid.Usage(), Property: expr.Properties{RowDependent: true}}, Owner: ancestorRef, Scope: 1, Frame: 1, QueryOccurrence: 1}
	second := first
	second.Owner, second.QueryOccurrence = localRef, 2
	schema := plan.QuerySchema{{Usage: oid.Usage()}, {Usage: oid.Usage()}}
	project := &plan.ProjectNode{QuerySchema: schema, Frame: 2, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{first, second}}
	right := &plan.DerivedNode{QuerySchema: schema, Query: &plan.Query{Root: project}, Lateral: true, ScanFrame: 1, Captures: []plan.FunctionCorrelation{{Frame: 1, QueryOccurrence: 1}, {Frame: 1, QueryOccurrence: 2}}}
	join := &plan.JoinNode{QuerySchema: schema, Left: &plan.SourceNode{Ref: localRef, Occurrence: 1}, Right: right, RightWidth: 2, Frame: 1, CorrelatedRight: true, CorrelationIdentityPrefix: true}
	stage, err := exec.OpenJoinNode(context.Background(), &state, join)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	i, present, err := stage.NextOccurrence(context.Background())
	if err != nil || !present {
		t.Fatal("zero-width source lost its occurrence", present, err)
	}
	row := stage.Source.Row(stage.Index(i))
	for slot, want := range []uint32{uint32(ancestorRef.Address.ObjectID), uint32(localRef.Address.ObjectID)} {
		if got, err := row[slot].OID(); err != nil || got != want {
			t.Fatal("wrong zero-width identity", slot, got, want, err)
		}
	}
	if _, present, err := stage.NextOccurrence(context.Background()); present || err != nil {
		t.Fatal(present, err)
	}
	if state.Invocation.QueryProvenance[0] != 0 || len(state.Invocation.QueryOccurrences) != 1 {
		t.Fatal("borrowed identity backing mutated")
	}
}

func lateralInvocationCleanup(t *testing.T, phase string) {
	source, state, producerProbe := lateralInvocationFixture(t)
	defer source.Close()
	reader, err := exec.OpenSourceStage(context.Background(), state, source, source.NestedRoot.Schema())
	if err != nil {
		t.Fatal(err)
	}
	lateralInvocationPull(t, &reader, 1)
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	producer := source.NestedStage
	cleanup, primary := errors.New("lateral close"), errors.New("lateral evaluation")
	closes := 0
	finish := producer.Finish
	producer.Finish = func() error {
		closes++
		if finish != nil {
			if err := finish(); err != nil {
				return err
			}
		}
		return cleanup
	}
	state.SourceMode = exec.SourceOwned
	state.Frame = 7
	state.Invocation = &exec.Context{EvalFrame: exec.EvalFrame{Scope: 1, Frame: 7}}
	schema := source.NestedRoot.Schema()
	read := plan.ColumnExpr{Info: expr.Info{Usage: schema[0].Usage}, Scope: 1, Frame: 8, Slot: 0}
	var output expr.Expr = read
	if phase == "projection" {
		output = queryProbe(schema[0].Usage, func(*exec.Context) (sqltype.Value, error) { return sqltype.Value{}, primary })
	}
	if phase == "producer" {
		producerProbe.failure = primary
	}
	project := &plan.ProjectNode{QuerySchema: schema, Child: &plan.SourceNode{QuerySchema: schema}, Expressions: []expr.Expr{output}, Frame: 8}
	node := &plan.DerivedNode{QuerySchema: schema, Query: &plan.Query{Root: project}, Lateral: true, ScanFrame: 7}
	stage, err := exec.OpenDerivedNode(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if phase == "cancel" {
		cancel()
	}
	if phase == "close" {
		lateralInvocationPull(t, &stage, 1)
		err = stage.Close()
		if !errors.Is(err, cleanup) {
			t.Fatal("close error suppressed", err)
		}
	} else {
		for {
			_, present, failure := stage.NextOccurrence(ctx)
			if failure != nil {
				err = failure
				break
			}
			if !present {
				t.Fatal("missing child failure", phase)
			}
		}
		want := primary
		if phase == "cancel" {
			want = context.Canceled
		}
		if !errors.Is(err, want) {
			t.Fatal("cleanup replaced primary", err, want)
		}
	}
	if stage.Source != nil || stage.Finish != nil || !producer.Closed || closes != 1 || state.Frame != 7 || state.Invocation.Frame != 7 {
		t.Fatal("child cleanup lost ownership", phase, closes, state)
	}
	if err := stage.Close(); err != nil || closes != 1 {
		t.Fatal("duplicate child close", err, closes)
	}
}

func lateralInvocationType(t *testing.T, id sqltype.ID) *sqltype.Descriptor {
	t.Helper()
	typ, ok := engine.New().CatalogRead().LookupType(id)
	if !ok {
		t.Fatal("missing builtin type", id)
	}
	return typ
}

func lateralInvocationPull(t *testing.T, stage *exec.Stage, want int64) {
	t.Helper()
	i, present, err := stage.NextOccurrence(context.Background())
	if err != nil || !present {
		t.Fatal("missing derived row", present, err)
	}
	if got, err := stage.Source.Row(stage.Index(i))[0].Integer(); err != nil || got != want {
		t.Fatal("wrong invocation row", got, want, err)
	}
}

type lateralInvocationProducer struct {
	expr.Expr
	calls   int
	failure error
}

func (p *lateralInvocationProducer) Eval(c expr.Context) (sqltype.Value, error) {
	p.calls++
	if p.failure != nil && p.calls == 2 {
		return sqltype.Value{}, p.failure
	}
	return p.Expr.Eval(c)
}

func lateralInvocationFixture(t *testing.T) (*exec.Source, *exec.State, *lateralInvocationProducer) {
	t.Helper()
	integer := lateralInvocationType(t, sqltype.Int4)
	schema := plan.QuerySchema{{Name: "id", Usage: integer.Usage()}}
	rows := make([][]sqltype.Value, 2)
	for i, n := range []int64{1, 3} {
		value, err := sqltype.Integer(integer, n)
		if err != nil {
			t.Fatal(err)
		}
		rows[i] = []sqltype.Value{value}
	}
	probe := &lateralInvocationProducer{Expr: plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 0}}
	project := &plan.ProjectNode{QuerySchema: schema, Child: &plan.SourceNode{QuerySchema: schema}, Expressions: []expr.Expr{probe}, Frame: 1}
	inputs := &exec.Source{Anonymous: rows}
	source := &exec.Source{Materialized: true, FlatWidth: 1, NestedRoot: project, NestedInputs: inputs, Lineage: &exec.QueryLockLineage{Root: project, Inputs: inputs, Stage: &exec.Source{Materialized: true}}}
	return source, &exec.State{Source: source, SourceMode: exec.SourceBorrowed}, probe
}

func lateralInvocationPrefix(t *testing.T) {
	integer := lateralInvocationType(t, sqltype.Int4)
	value := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(integer, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	column := func(slot int) plan.ColumnExpr {
		return plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 7, Slot: slot}
	}
	for _, mixed := range []bool{false, true} {
		state := &exec.State{Source: &exec.Source{}, SourceMode: exec.SourceBorrowed, Frame: 7}
		prefix := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 7, Row: []sqltype.Value{value(100)}}}
		state.Invocation = prefix
		leftSchema := plan.QuerySchema{{Usage: integer.Usage()}}
		left := &plan.ValuesNode{QuerySchema: leftSchema, Rows: [][]expr.Expr{
			{plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: value(1)}},
			{plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: value(2)}},
		}}
		captures := []plan.FunctionCorrelation{{Frame: 7, Slot: 0}}
		output := queryProbe(integer.Usage(), func(c *exec.Context) (sqltype.Value, error) {
			ancestor, err := column(0).Eval(c)
			if err != nil {
				return sqltype.Value{}, err
			}
			n, err := ancestor.Integer()
			if err != nil {
				return sqltype.Value{}, err
			}
			if mixed {
				local, err := column(1).Eval(c)
				if err != nil {
					return sqltype.Value{}, err
				}
				v, err := local.Integer()
				if err != nil {
					return sqltype.Value{}, err
				}
				n += v
			}
			return sqltype.Integer(integer, n)
		})
		if mixed {
			captures = append(captures, plan.FunctionCorrelation{Frame: 7, Slot: 1})
		}
		rightSchema := plan.QuerySchema{{Usage: integer.Usage()}}
		child := &plan.ProjectNode{QuerySchema: rightSchema, Frame: 8, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{output}}
		right := &plan.DerivedNode{QuerySchema: rightSchema, Query: &plan.Query{Root: child}, ScanFrame: 7, Lateral: true, Captures: captures}
		join := &plan.JoinNode{QuerySchema: append(append(plan.QuerySchema{}, leftSchema...), rightSchema...), Left: left, Right: right, LeftWidth: 1, RightWidth: 1, Frame: 7, CorrelatedRight: mixed, CorrelationPrefix: 1, Captures: captures[:1]}
		stage, err := exec.OpenJoinNode(context.Background(), state, join)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []int64{1, 2} {
			i, present, err := stage.NextOccurrence(context.Background())
			if err != nil || !present {
				t.Fatal("missing prefix invocation", mixed, present, err)
			}
			row := stage.Source.Row(stage.Index(i))
			want := int64(100)
			if mixed {
				want += id
			}
			if got, err := row[1].Integer(); err != nil || got != want {
				t.Fatal("wrong inherited/local frame", mixed, got, want, err)
			}
		}
		if _, present, err := stage.NextOccurrence(context.Background()); present || err != nil {
			t.Fatal(present, err)
		}
		if err := stage.Close(); err != nil || state.Invocation != prefix || len(prefix.Row) != 1 || prefix.Row[0] != value(100) || child.Frame != 8 || right.Captures[0].Slot != 0 {
			t.Fatal("invocation mutated accepted plan or borrowed prefix", err)
		}
	}
}

func lateralInvocationPresence(t *testing.T) {
	record, err := sqltype.RecordShape(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, present := range []bool{false, true} {
		local := &exec.Source{Anonymous: [][]sqltype.Value{{}, {}}}
		inputs := &exec.Source{Sources: []*exec.Source{local}}
		state := &exec.State{Source: inputs, SourceMode: exec.SourceBorrowed, Frame: 7}
		prefix := &exec.Context{EvalFrame: exec.EvalFrame{Scope: 1, Frame: 7, PresenceSlots: []int{1}, Presence: []bool{present}}}
		state.Invocation = prefix
		row := func(slot int) expr.Expr {
			return plan.RowExpr{Info: expr.Info{Usage: record.Usage(), Property: expr.Properties{RowDependent: true}}, PresenceFrame: 7, PresenceSlot: slot}
		}
		schema := plan.QuerySchema{{Usage: record.Usage()}, {Usage: record.Usage()}}
		child := &plan.ProjectNode{QuerySchema: schema, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{row(1), row(2)}, Frame: 8}
		right := &plan.DerivedNode{QuerySchema: schema, Query: &plan.Query{Root: child}, ScanFrame: 7, Lateral: true, Captures: []plan.FunctionCorrelation{{Frame: 7, PresenceSlot: 1}, {Frame: 7, PresenceSlot: 2}}}
		join := &plan.JoinNode{QuerySchema: schema, Left: &plan.SourceNode{PresenceSlot: 2}, Right: right, RightWidth: 2, Frame: 7, CorrelatedRight: true, CorrelationIdentityPrefix: true}
		stage, err := exec.OpenJoinNode(context.Background(), state, join)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			i, found, err := stage.NextOccurrence(context.Background())
			if err != nil || !found {
				t.Fatal("missing zero-width presence invocation", found, err)
			}
			output := stage.Source.Row(stage.Index(i))
			if output[0].IsNull() == present || output[1].IsNull() || output[0].Type() != record || output[1].Type() != record {
				t.Fatal("inherited/local packed presence confused absence with empty tuple", present, output)
			}
		}
		if _, found, err := stage.NextOccurrence(context.Background()); found || err != nil {
			t.Fatal(found, err)
		}
		if err := stage.Close(); err != nil || state.Invocation != prefix || prefix.Presence[0] != present || len(prefix.PresenceSlots) != 1 {
			t.Fatal("borrowed presence backing escaped", err)
		}
	}
}
