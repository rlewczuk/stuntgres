package core

import (
	"context"
	"fmt"
	"reflect"
	"stuntgres/internal/engine/exec"
)

// ScopeAggregateSQLGate returns the actual statement-owned SQL gate so external
// composed tests can retain its token and observe a canceled production waiter.
func ScopeAggregateSQLGate(s *StatementScope) chan struct{} {
	s.aggregateGateOnce.Do(func() { s.aggregateGate = make(chan struct{}, 1) })
	return s.aggregateGate
}

// ScopeAggregateInvocationContext observes restoration of the worker context.
func ScopeAggregateInvocationContext(s *StatementScope) context.Context {
	return s.invocationContext
}

// CaptureAggregateScratchOwnership records lexical owners, not copies of the
// mutable graphs behind them. It is used only before entry and after join.
func CaptureAggregateScratchOwnership(s *StatementScope) func() error {
	snapshot := func() map[string]string {
		out := make(map[string]string)
		var capture func(string, reflect.Value)
		capture = func(path string, v reflect.Value) {
			switch v.Kind() {
			case reflect.Struct:
				for i := range v.NumField() {
					capture(path+"."+v.Type().Field(i).Name, v.Field(i))
				}
			case reflect.Interface:
				if v.IsNil() {
					out[path] = "nil"
				} else {
					capture(path+"."+v.Elem().Type().String(), v.Elem())
				}
			case reflect.Pointer, reflect.Chan, reflect.Func, reflect.UnsafePointer:
				out[path] = fmt.Sprintf("%x", v.Pointer())
			case reflect.Map:
				out[path] = fmt.Sprintf("%p", v.UnsafePointer())
			case reflect.Slice:
				out[path] = fmt.Sprintf("%x/%d/%d", v.Pointer(), v.Len(), v.Cap())
			case reflect.Bool:
				out[path] = fmt.Sprint(v.Bool())
			case reflect.String:
				out[path] = v.String()
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				out[path] = fmt.Sprint(v.Int())
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				out[path] = fmt.Sprint(v.Uint())
			case reflect.Array:
				for i := range v.Len() {
					capture(fmt.Sprintf("%s[%d]", path, i), v.Index(i))
				}
			}
		}
		capture("scratch", reflect.ValueOf(s.sqlRoutineScratch()))
		capture("presentation", reflect.ValueOf(s.presentationOrZero()))
		capture("journal", reflect.ValueOf(s.Catalog))
		capture("ownerJournal", reflect.ValueOf(s.Owner.Catalog))
		capture("stageRows", reflect.ValueOf(s.Stage.Rows))
		capture("stageDeletes", reflect.ValueOf(s.Stage.Deletes))
		capture("effectiveUser", reflect.ValueOf(s.Op.EffectiveUser))
		capture("restricted", reflect.ValueOf(s.Op.Restricted))
		capture("invocation", reflect.ValueOf(s.invocationContext))
		capture("routineLive", reflect.ValueOf(s.routineLive))
		capture("foreignActor", reflect.ValueOf(s.ForeignActionAuth))
		out["depth"] = fmt.Sprint(s.activeSQLRoutineDepth)
		out["atomic"] = fmt.Sprint(s.plAtomic)
		out["rowSecurity"] = fmt.Sprint(s.RowSecurity)
		out["stage"] = fmt.Sprintf("%p", &s.Stage)
		out["operation"] = fmt.Sprintf("%p", s.Op)
		out["owner"] = fmt.Sprintf("%p", s.Owner)
		return out
	}
	before := snapshot()
	return func() error {
		after := snapshot()
		for key, value := range before {
			if after[key] != value {
				return fmt.Errorf("aggregate scratch owner %s: got %s, want %s", key, after[key], value)
			}
		}
		if !reflect.DeepEqual(before, after) {
			return fmt.Errorf("aggregate scratch added lexical owners")
		}
		return nil
	}
}

// CheckAggregatePresentationOwners verifies shared immutable facts, a single
// warmed evaluator, then independent lazy-cache backing after warming both.
func CheckAggregatePresentationOwners(a, b *exec.EvaluationCapture) error {
	p, pok := a.Presentation.(*parallelPresentation)
	q, qok := b.Presentation.(*parallelPresentation)
	if !pok || !qok || p == q {
		return fmt.Errorf("missing private production formatter owners")
	}
	if p.view != q.view || !reflect.DeepEqual(p.roles, q.roles) || reflect.ValueOf(p.path).Pointer() != reflect.ValueOf(q.path).Pointer() || reflect.ValueOf(p.collations).Pointer() != reflect.ValueOf(q.collations).Pointer() || p.quoteAll != q.quoteAll {
		return fmt.Errorf("workers copied or replaced immutable presentation facts")
	}
	left, right := reflect.ValueOf(p.spellings), reflect.ValueOf(q.spellings)
	if left.FieldByName("regproc").IsNil() == right.FieldByName("regproc").IsNil() {
		return fmt.Errorf("one evaluator's lazy warm escaped into its sibling")
	}
	for _, owner := range []*parallelPresentation{p, q} {
		for _, format := range []func(uint32) (string, error){owner.FormatRegproc, owner.FormatRegprocedure, owner.FormatRegoper, owner.FormatRegoperator, owner.FormatRegconfig, owner.FormatRegdictionary} {
			if _, err := format(0); err != nil {
				return err
			}
		}
	}
	left, right = reflect.ValueOf(p.spellings), reflect.ValueOf(q.spellings)
	for i := range left.NumField() {
		x, y := left.Field(i), right.Field(i)
		switch x.Kind() {
		case reflect.Map:
			if x.Len() != 0 && (y.Len() == 0 || x.UnsafePointer() == y.UnsafePointer()) {
				return fmt.Errorf("shared spelling cache %s", left.Type().Field(i).Name)
			}
		case reflect.Slice:
			if x.Len() != 0 && (y.Len() == 0 || x.Pointer() == y.Pointer()) {
				return fmt.Errorf("shared spelling cache %s", left.Type().Field(i).Name)
			}
		}
	}
	return nil
}
