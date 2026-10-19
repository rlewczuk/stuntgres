package sqltype

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestJSONBPathOpsSignatures(t *testing.T) {
	d, foreign := testTypes(), testTypes()
	for _, name := range []string{"@?", "@@"} {
		args := []Usage{d[JSONB].Usage(), d[JSONPath].Usage()}
		p, err := BindBinary(name, d[Bool].Usage(), args...)
		if err != nil || p.arity != 2 || !p.Immutable() || p.args[2] != args[0] || p.args[3] != d[Bool].Usage() {
			t.Fatal("consumer authority", p, err)
		}
		for _, bad := range [][]Usage{nil, args[:1], append(append([]Usage(nil), args...), d[JSONB].Usage()), {args[0], d[Text].Usage()}} {
			if _, err := BindBinary(name, d[Bool].Usage(), bad...); err == nil {
				t.Fatal("invalid signature accepted", bad)
			}
		}
		for slot := range 2 {
			bad := append([]Usage(nil), args...)
			bad[slot].Typmod = 1
			if _, err := BindBinary(name, d[Bool].Usage(), bad...); err == nil {
				t.Fatal("typmod accepted", slot)
			}
			values := navigateValues(t, d, `true`, `$`, `{}`, true)[:2]
			values[slot] = Null(foreign[args[slot].Type.ID()])
			if _, err := p.Evaluate(context.Background(), values...); err == nil {
				t.Fatal("foreign value accepted", slot)
			}
		}
		values := []Value{Null(d[JSONB]), Null(d[JSONPath])}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := p.Evaluate(ctx, values...); !errors.Is(err, context.Canceled) {
			t.Fatal("context before NULL", err)
		}
		v, err := p.Evaluate(context.Background(), values...)
		if err != nil || !v.null || v.typ != d[Bool] {
			t.Fatal("typed NULL", v, err)
		}
		if _, err := BindBinary(name, d[JSONB].Usage(), args...); err == nil {
			t.Fatal("wrong result accepted")
		}
	}
}

func TestJSONBPathOpsConsumers(t *testing.T) {
	d := testTypes()
	ctx := context.Background()
	for _, name := range []string{"@?", "@@"} {
		p, err := BindBinary(name, d[Bool].Usage(), d[JSONB].Usage(), d[JSONPath].Usage())
		if err != nil {
			t.Fatal(err)
		}
		original := p
		// Every call must retain the prebound descriptor authority and obtain
		// invocation-local consumer state, including concurrent reuse.
		var inputs [][]Value
		for _, path := range []string{`$`, `strict $.missing`, `$v`, `$`} {
			inputs = append(inputs, navigateValues(t, d, `false`, path, `{}`, true))
		}
		var workers sync.WaitGroup
		for range 4 {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for _, values := range inputs {
					got, gotErr := p.Evaluate(ctx, values[:2]...)
					var want Value
					var wantErr error
					if name == "@?" {
						plan, err := BindJSONBPathExists(d[Bool].Usage(), p.args[:]...)
						if err != nil {
							t.Error(err)
							return
						}
						want, wantErr = plan.Evaluate(ctx, values)
					} else {
						plan, err := BindJSONBPathMatch(d[Bool].Usage(), p.args[:]...)
						if err != nil {
							t.Error(err)
							return
						}
						want, wantErr = plan.Evaluate(ctx, values)
					}
					if (gotErr == nil) != (wantErr == nil) || gotErr != nil && gotErr.Error() != wantErr.Error() || gotErr == nil && (got.typ != want.typ || got.null != want.null || got.boolean != want.boolean) {
						t.Errorf("adapter diverged from completed consumer: %v/%v, %v/%v", got, gotErr, want, wantErr)
					}
				}
			}()
		}
		workers.Wait()
		if p != original || len(jsonbPathOperatorVars.root.fields) != 0 || jsonbPathOperatorVars.root.kind != jsonbObject {
			t.Fatal("invocation mutated shared authority")
		}
	}
}

func TestJSONBPathOpsResolution(t *testing.T) {
	d := testTypes()
	path := OperatorInputs{JSONB, JSONPath}
	for _, tc := range []struct {
		candidates  []OperatorInputs
		left, right *Descriptor
		want        OperatorInputs
	}{
		{[]OperatorInputs{path}, nil, nil, path},
		{[]OperatorInputs{path, {Text, Text}, {Text, 3615}}, nil, nil, OperatorInputs{Text, Text}},
		{[]OperatorInputs{path, {Text, Text}, {Text, 3615}}, d[JSONB], nil, path},
		{[]OperatorInputs{path, {Text, Text}, {Text, 3615}}, nil, d[JSONPath], path},
		{[]OperatorInputs{path, {Text, Text}, {Text, 3615}}, d[Text], nil, OperatorInputs{Text, Text}},
	} {
		i, err := ResolveOperator(tc.candidates, tc.left, tc.right, false)
		if err != nil || i < 0 || tc.candidates[i] != tc.want {
			t.Fatalf("selected index=%d err=%v want=%v", i, err, tc.want)
		}
	}
}
