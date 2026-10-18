package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type pgcryptoEntropyUnitContext struct {
	unitCapabilities
	context.Context
	calls   int
	failure error
	cancel  context.CancelFunc
	order   []int
}

func (c *pgcryptoEntropyUnitContext) FunctionContext() context.Context { return c.Context }
func (c *pgcryptoEntropyUnitContext) FunctionPgcryptoEntropy(dst []byte) error {
	c.calls++
	if len(dst) != 16 {
		return errors.New("wrong entropy size")
	}
	if c.cancel != nil {
		c.cancel()
	}
	clear(dst)
	return c.failure
}

type pgcryptoEntropyArgument struct {
	expr.Info
	value   sqltype.Value
	failure error
	index   int
}

func (a pgcryptoEntropyArgument) Eval(ec expr.Context) (sqltype.Value, error) {
	c := ec.(*pgcryptoEntropyUnitContext)
	c.order = append(c.order, a.index)
	return a.value, a.failure
}

func TestPgcryptoPasswordEntropy(t *testing.T) {
	types := formatUnitTypes()
	text, integer := types[sqltype.Text], types[sqltype.Int4]
	for _, v := range []struct {
		name          string
		count         int64
		null          bool
		calls         int
		code, message string
	}{
		{"bf", 4, false, 1, "", ""}, {"BF", 0, false, 1, "", ""},
		{"bf", 3, false, 0, "22023", "gen_salt: Incorrect number of rounds"},
		{"unknown", 4, false, 0, "22023", "gen_salt: Unknown salt algorithm"},
		{"bf", 3, true, 0, "", ""},
	} {
		count, err := sqltype.Integer(integer, v.count)
		if err != nil {
			t.Fatal(err)
		}
		name := sqltype.String(text, v.name)
		if v.null {
			name = sqltype.Null(text)
		}
		args := []pgcryptoEntropyArgument{{Info: expr.Info{Usage: text.Usage()}, value: name, index: 0}, {Info: expr.Info{Usage: integer.Usage()}, value: count, index: 1}}
		call, err := NewPgcrypto(expr.Info{Usage: text.Usage()}, "gen_salt", unitArgs(args))
		if err != nil {
			t.Fatal(err)
		}
		ctx := &pgcryptoEntropyUnitContext{Context: context.Background()}
		got, err := call.Eval(ctx)
		if ctx.calls != v.calls || !reflect.DeepEqual(ctx.order, []int{0, 1}) {
			t.Fatal(v, ctx)
		}
		if v.code != "" {
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != v.code || diagnostic.Message != v.message || got.Type() != nil {
				t.Fatal(v, got, err)
			}
		} else if err != nil || got.Type() != text || got.IsNull() != v.null {
			t.Fatal(v, got, err)
		} else if !v.null {
			want := "$2a$04$......................"
			if v.count == 0 {
				want = "$2a$06$......................"
			}
			value, _ := got.Text()
			if value != want {
				t.Fatal(value, want)
			}
		}
		if v.calls == 0 {
			continue
		}
		ctx.failure = errors.New("entropy unavailable")
		got, err = call.Eval(ctx)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Message != "gen_salt: Failed to generate strong random bits" || got.Type() != nil {
			t.Fatal(got, err)
		}
		canceled, cancel := context.WithCancel(context.Background())
		ctx.Context, ctx.cancel = canceled, cancel
		got, err = call.Eval(ctx)
		cancel()
		if !errors.Is(err, context.Canceled) || got.Type() != nil {
			t.Fatal(got, err)
		}
	}
	count, _ := sqltype.Integer(integer, 4)
	failure := errors.New("reached sibling")
	call, err := NewPgcrypto(expr.Info{Usage: text.Usage()}, "gen_salt", unitArgs([]pgcryptoEntropyArgument{
		{Info: expr.Info{Usage: text.Usage()}, value: sqltype.Null(text), index: 0},
		{Info: expr.Info{Usage: integer.Usage()}, value: count, failure: failure, index: 1},
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := &pgcryptoEntropyUnitContext{Context: context.Background()}
	if _, err := call.Eval(ctx); !errors.Is(err, failure) || ctx.calls != 0 || !reflect.DeepEqual(ctx.order, []int{0, 1}) {
		t.Fatal(ctx, err)
	}
}
