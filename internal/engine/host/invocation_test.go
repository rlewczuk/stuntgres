package host

import (
	"context"
	"testing"
)

func TestHostCallbackContinuationOwnership(t *testing.T) {
	owner := new(int)
	call := &invocation{context: context.WithValue(context.Background(), struct{}{}, owner), admit: func() error { *owner++; return nil }, program: func(_ context.Context, args []Object) (Object, error) {
		return Object{Value: args[0].Value.(int64) + 1}, nil
	}}
	held := call.run
	call.close()
	if call.context != nil || call.admit != nil || call.program != nil {
		t.Fatal("closed holder retains private continuation backing")
	}
	if _, err := held(context.Background(), []Object{{Value: int64(7)}}); err == nil || *owner != 0 {
		t.Fatal("ended holder reentered private owner", err)
	}
	for _, panicking := range []bool{false, true} {
		t.Run(map[bool]string{false: "return", true: "panic"}[panicking], func(t *testing.T) {
			var retained ProgramFunc
			var arguments []Object
			calls := 0
			d := LanguageDefinition{Provider: "continuation-owner", ABIVersion: "1", Name: "continuation_host", HandlerName: Name{Schema: "public", Name: "continuation_handler"}, Handler: func(ctx context.Context, p ProgramFunc, args []Object) (Object, error) {
				retained = p
				arguments = args
				return p(ctx, args)
			}, Programs: []ProgramDefinition{{ID: "increment", Version: "1", Inputs: []Type{Int8}, Result: Int8, Execute: func(_ context.Context, args []Object) (Object, error) {
				calls++
				if panicking {
					panic("application panic")
				}
				return Object{Value: args[0].Value.(int64) + 1}, nil
			}}}, Conversions: []ConversionDefinition{{ID: "integer", Type: Int8, Bootstrap: true, FromName: Name{Schema: "public", Name: "continuation_from"}, ToName: Name{Schema: "public", Name: "continuation_to"}, FromSQL: func(_ context.Context, v SQLValue) (Object, error) { return Object{Value: v.Int64}, nil }, ToSQL: func(_ context.Context, v Object) (SQLValue, error) {
				return SQLValue{Type: Int8, Int64: v.Value.(int64)}, nil
			}}}}
			runtime, err := New(d)
			if err != nil {
				t.Fatal(err)
			}
			token := runtime.Conversion("integer")
			recovered := false
			func() {
				defer func() {
					if failure := recover(); failure != nil {
						if !panicking || failure != "application panic" {
							t.Fatal(failure)
						}
						recovered = true
					}
				}()
				result, err := runtime.Program("increment").Invoke(context.Background(), []SQLValue{{Type: Int8, Int64: 7}}, []*Conversion{token}, token, func() error { return nil })
				if err != nil || result.Int64 != 8 {
					t.Fatal(result, err)
				}
			}()
			if recovered != panicking || calls != 1 || retained == nil || len(arguments) != 1 || arguments[0].Value != nil {
				t.Fatal("actual Invoke retained callback slots on unwind", recovered, calls, arguments)
			}
			if _, err := retained(context.Background(), []Object{{Value: int64(99)}}); err == nil || calls != 1 {
				t.Fatal("retained callback executed after actual Invoke unwind", calls, err)
			}
		})
	}
}
