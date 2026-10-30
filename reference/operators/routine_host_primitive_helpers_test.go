package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Each call owns its definition slices. Callbacks capture only immutable type IDs.
func hostPrimitiveDefinition() stuntgres.HostLanguageDefinition {
	d := stuntgres.HostLanguageDefinition{Provider: "reference-go-host-primitives", ABIVersion: "primitives-v1", Name: "go_host_primitive", HandlerName: stuntgres.HostName{Schema: "public", Name: "go_host_primitive_call_handler"}, Handler: func(ctx context.Context, program stuntgres.HostProgramFunc, args []stuntgres.HostObject) (stuntgres.HostObject, error) {
		return program(ctx, args)
	}}
	for _, typ := range []stuntgres.HostType{stuntgres.HostBool, stuntgres.HostInt4, stuntgres.HostInt8, stuntgres.HostText} {
		id := string(typ)
		d.Conversions = append(d.Conversions, stuntgres.HostConversionDefinition{ID: id, Type: typ, Bootstrap: true, FromName: stuntgres.HostName{Schema: "public", Name: "go_host_primitive_from_" + id}, ToName: stuntgres.HostName{Schema: "public", Name: "go_host_primitive_to_" + id}, FromSQL: func(_ context.Context, v stuntgres.HostSQLValue) (stuntgres.HostObject, error) {
			if v.Type != typ {
				return stuntgres.HostObject{}, errors.New("primitive: wrong input SQL type")
			}
			if v.Null {
				return stuntgres.HostObject{Null: true}, nil
			}
			switch typ {
			case stuntgres.HostBool:
				return stuntgres.HostObject{Value: v.Bool}, nil
			case stuntgres.HostInt4, stuntgres.HostInt8:
				return stuntgres.HostObject{Value: v.Int64}, nil
			case stuntgres.HostText:
				return stuntgres.HostObject{Value: v.Text}, nil
			}
			return stuntgres.HostObject{}, errors.New("primitive: unsupported input")
		}, ToSQL: func(_ context.Context, v stuntgres.HostObject) (stuntgres.HostSQLValue, error) {
			out := stuntgres.HostSQLValue{Type: typ, Null: v.Null}
			if v.Null {
				if v.Value != nil {
					return out, errors.New("primitive: null object payload")
				}
				return out, nil
			}
			switch typ {
			case stuntgres.HostBool:
				x, ok := v.Value.(bool)
				if !ok {
					return out, errors.New("primitive: expected bool object")
				}
				out.Bool = x
			case stuntgres.HostInt4, stuntgres.HostInt8:
				x, ok := v.Value.(int64)
				if !ok {
					return out, errors.New("primitive: expected int64 object")
				}
				out.Int64 = x
			case stuntgres.HostText:
				x, ok := v.Value.(string)
				if !ok {
					return out, errors.New("primitive: expected string object")
				}
				out.Text = x
			}
			return out, nil
		}})
		d.Defaults = append(d.Defaults, stuntgres.HostDefaultDefinition{Type: typ, FromID: id, ToID: id})
	}
	add := func(id string, inputs []stuntgres.HostType, result stuntgres.HostType, f stuntgres.HostProgramFunc) {
		d.Programs = append(d.Programs, stuntgres.HostProgramDefinition{ID: id, Version: "1", Inputs: inputs, Result: result, Execute: f})
	}
	unary := func(id string, typ stuntgres.HostType, f func(any) (any, error)) {
		add(id, []stuntgres.HostType{typ}, typ, func(_ context.Context, args []stuntgres.HostObject) (stuntgres.HostObject, error) {
			if len(args) != 1 {
				return stuntgres.HostObject{}, errors.New("primitive: unary arity")
			}
			if args[0].Null {
				return stuntgres.HostObject{Null: true}, nil
			}
			v, err := f(args[0].Value)
			return stuntgres.HostObject{Value: v}, err
		})
	}
	unary("bool-not-v1", stuntgres.HostBool, func(v any) (any, error) {
		x, ok := v.(bool)
		if !ok {
			return nil, errors.New("primitive: expected bool object")
		}
		return !x, nil
	})
	unary("int4-offset-v1", stuntgres.HostInt4, func(v any) (any, error) {
		x, ok := v.(int64)
		if !ok {
			return nil, errors.New("primitive: expected int64 object")
		}
		return x + 1, nil
	})
	unary("int4-object-v1", stuntgres.HostInt4, func(v any) (any, error) {
		x, ok := v.(int64)
		if !ok {
			return nil, errors.New("primitive: expected int64 object")
		}
		if x == 7 {
			return "not-an-integer", nil
		}
		return x + 1, nil
	})
	unary("int4-decrement-v1", stuntgres.HostInt4, func(v any) (any, error) {
		x, ok := v.(int64)
		if !ok {
			return nil, errors.New("primitive: expected int64 object")
		}
		return x - 1, nil
	})
	unary("int8-offset-v1", stuntgres.HostInt8, func(v any) (any, error) {
		x, ok := v.(int64)
		if !ok {
			return nil, errors.New("primitive: expected int64 object")
		}
		return hostPrimitiveAdd(x, 5)
	})
	unary("text-mark-v1", stuntgres.HostText, func(v any) (any, error) {
		x, ok := v.(string)
		if !ok {
			return nil, errors.New("primitive: expected string object")
		}
		return x + "!", nil
	})
	unary("text-strip-v1", stuntgres.HostText, func(v any) (any, error) {
		x, ok := v.(string)
		if !ok {
			return nil, errors.New("primitive: expected string object")
		}
		return strings.TrimSuffix(x, "!"), nil
	})
	unary("text-invalid-v1", stuntgres.HostText, func(v any) (any, error) {
		x, ok := v.(string)
		if !ok {
			return nil, errors.New("primitive: expected string object")
		}
		if x == "nul" {
			return "a\x00b", nil
		}
		if x == "utf8" {
			return string([]byte{0xff}), nil
		}
		return x + "!", nil
	})
	unary("text-error-v1", stuntgres.HostText, func(v any) (any, error) {
		x, ok := v.(string)
		if !ok {
			return nil, errors.New("primitive: expected string object")
		}
		if x == "bad" {
			return nil, errors.New("primitive: program rejected text")
		}
		return x + "!", nil
	})
	add("zero-v1", nil, stuntgres.HostInt8, func(_ context.Context, args []stuntgres.HostObject) (stuntgres.HostObject, error) {
		if len(args) != 0 {
			return stuntgres.HostObject{}, errors.New("primitive: zero arity")
		}
		return stuntgres.HostObject{Value: int64(17)}, nil
	})
	for _, n := range []int{2, 5} {
		id := "int8-add-v1"
		if n == 5 {
			id = "int8-sum5-v1"
		}
		inputs := make([]stuntgres.HostType, n)
		for i := range inputs {
			inputs[i] = stuntgres.HostInt8
		}
		add(id, inputs, stuntgres.HostInt8, func(_ context.Context, args []stuntgres.HostObject) (stuntgres.HostObject, error) {
			if len(args) != n {
				return stuntgres.HostObject{}, errors.New("primitive: sum arity")
			}
			var sum int64
			for _, arg := range args {
				if arg.Null {
					return stuntgres.HostObject{Null: true}, nil
				}
				x, ok := arg.Value.(int64)
				if !ok {
					return stuntgres.HostObject{}, errors.New("primitive: expected int64 object")
				}
				var err error
				sum, err = hostPrimitiveAdd(sum, x)
				if err != nil {
					return stuntgres.HostObject{}, err
				}
			}
			return stuntgres.HostObject{Value: sum}, nil
		})
	}
	add("mixed-v1", []stuntgres.HostType{stuntgres.HostBool, stuntgres.HostInt4, stuntgres.HostInt8, stuntgres.HostText}, stuntgres.HostText, func(_ context.Context, args []stuntgres.HostObject) (stuntgres.HostObject, error) {
		if len(args) != 4 {
			return stuntgres.HostObject{}, errors.New("primitive: mixed arity")
		}
		for _, arg := range args {
			if arg.Null {
				return stuntgres.HostObject{Null: true}, nil
			}
		}
		b, bok := args[0].Value.(bool)
		i, iok := args[1].Value.(int64)
		j, jok := args[2].Value.(int64)
		s, sok := args[3].Value.(string)
		if !bok || !iok || !jok || !sok {
			return stuntgres.HostObject{}, errors.New("primitive: ordered mixed objects")
		}
		return stuntgres.HostObject{Value: strconv.FormatBool(b) + ":" + strconv.FormatInt(i, 10) + ":" + strconv.FormatInt(j, 10) + ":" + s}, nil
	})
	add("text-null-v1", []stuntgres.HostType{stuntgres.HostText}, stuntgres.HostText, func(_ context.Context, args []stuntgres.HostObject) (stuntgres.HostObject, error) {
		if len(args) != 1 {
			return stuntgres.HostObject{}, errors.New("primitive: null arity")
		}
		if args[0].Null {
			return stuntgres.HostObject{Value: "handled-null"}, nil
		}
		x, ok := args[0].Value.(string)
		if !ok {
			return stuntgres.HostObject{}, errors.New("primitive: expected string object")
		}
		return stuntgres.HostObject{Value: x + "!"}, nil
	})
	defaults := d.Programs[0]
	defaults.ID = "bool-default-v1"
	defaults.Inputs = []stuntgres.HostType{stuntgres.HostBool}
	d.Programs = append(d.Programs, defaults)
	return d
}

func hostPrimitiveAdd(a, b int64) (int64, error) {
	if b > 0 && a > math.MaxInt64-b || b < 0 && a < math.MinInt64-b {
		return 0, errors.New("primitive: int8 overflow")
	}
	return a + b, nil
}

func hostPrimitiveScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error { return d.RegisterHostLanguage(hostPrimitiveDefinition()) })
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if b.PG {
				defer routineLanguagesCleanup(t, c, "DROP EXTENSION IF EXISTS plpython3u CASCADE")()
				hostExec(t, p, b, c, "CREATE EXTENSION plpython3u")
			}
			body(t, p, b, c)
		})
	}
}

func hostPrimitiveFunction(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name, signature, result, program, python, options string) {
	t.Helper()
	language, source := "go_host_primitive", program
	if b.PG {
		language, source = "plpython3u", python
	}
	hostExec(t, p, b, c, fmt.Sprintf("CREATE OR REPLACE FUNCTION %s(%s) RETURNS %s LANGUAGE %s %s AS '%s'", name, signature, result, language, options, hostQuote(source)))
}
