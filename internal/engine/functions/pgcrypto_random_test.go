package functions

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type pgcryptoRandomContext struct {
	unitCapabilities
	context.Context
	fill     byte
	requests []int
	order    []int
	failure  error
	cancel   context.CancelFunc
	mode     bool
}

func (c *pgcryptoRandomContext) FunctionContext() context.Context { return c.Context }
func (c *pgcryptoRandomContext) FunctionPgcryptoEntropy(dst []byte) error {
	c.requests = append(c.requests, len(dst))
	for i := range dst {
		dst[i] = c.fill
	}
	if c.cancel != nil {
		c.cancel()
	}
	return c.failure
}
func (c *pgcryptoRandomContext) FunctionUUIDEntropy(dst []byte) error {
	return c.FunctionPgcryptoEntropy(dst)
}
func (c *pgcryptoRandomContext) FunctionUUIDReady() error { return c.Err() }
func (c *pgcryptoRandomContext) FunctionUUIDTime() int64  { panic("unexpected UUID alias clock demand") }
func (c *pgcryptoRandomContext) FunctionInput() sqltype.InputEnvironment {
	panic("unexpected UUID alias calendar demand")
}
func (c *pgcryptoRandomContext) FunctionPgcryptoFIPSMode() bool { return c.mode }

type pgcryptoRandomArgument struct {
	expr.Info
	value   sqltype.Value
	failure error
}

func (a pgcryptoRandomArgument) Eval(ec expr.Context) (sqltype.Value, error) {
	c := ec.(*pgcryptoRandomContext)
	c.order = append(c.order, 0)
	return a.value, a.failure
}

func TestPgcryptoRandomResolution(t *testing.T) {
	types := formatUnitTypes()
	for _, v := range []struct {
		name string
		ids  []sqltype.ID
		want Resolution
	}{
		{"gen_random_bytes", []sqltype.ID{0}, Resolved}, {"gen_random_bytes", []sqltype.ID{sqltype.Int2}, Resolved}, {"gen_random_bytes", []sqltype.ID{sqltype.Int4}, Resolved},
		{"gen_random_bytes", nil, Missing}, {"gen_random_bytes", []sqltype.ID{sqltype.Int4, sqltype.Int4}, Missing},
		{"gen_random_bytes", []sqltype.ID{sqltype.Int8}, Missing}, {"gen_random_bytes", []sqltype.ID{sqltype.Numeric}, Missing}, {"gen_random_bytes", []sqltype.ID{sqltype.Text}, Missing}, {"gen_random_bytes", []sqltype.ID{sqltype.Bool}, Missing},
		{"gen_random_uuid", nil, Resolved}, {"fips_mode", nil, Resolved}, {"gen_random_uuid", []sqltype.ID{0}, Missing}, {"fips_mode", []sqltype.ID{0}, Missing},
	} {
		args := make([]Argument, len(v.ids))
		for i, id := range v.ids {
			if id != 0 {
				args[i].Usage = types[id].Usage()
			}
		}
		input, got := PgcryptoInput(v.name, args, types[sqltype.Text].Usage(), types[sqltype.Bytea].Usage(), types[sqltype.Int4].Usage())
		if got != v.want || got == Resolved && input.Type == nil || got == Resolved && v.name == "gen_random_bytes" && input.Type.ID() != sqltype.Int4 {
			t.Fatal(v, input, got)
		}
	}
}

func pgcryptoRandomCall(t *testing.T, name string, count int64, null bool) (Pgcrypto, *sqltype.Descriptor) {
	t.Helper()
	types := formatUnitTypes()
	result := types[sqltype.Bytea]
	var args []pgcryptoRandomArgument
	switch name {
	case "gen_random_bytes":
		n, err := sqltype.Integer(types[sqltype.Int4], count)
		if err != nil {
			t.Fatal(err)
		}
		if null {
			n = sqltype.Null(types[sqltype.Int4])
		}
		args = []pgcryptoRandomArgument{{Info: expr.Info{Usage: types[sqltype.Int4].Usage()}, value: n}}
	case "gen_random_uuid":
		result = types[sqltype.UUID]
	case "fips_mode":
		result = types[sqltype.Bool]
	}
	call, err := NewPgcrypto(expr.Info{Usage: result.Usage()}, name, unitArgs(args))
	if err != nil {
		t.Fatal(err)
	}
	if err := call.Validate(); err != nil {
		t.Fatal(err)
	}
	return call, result
}

func TestPgcryptoRandomEvaluation(t *testing.T) {
	for _, count := range []int64{-2147483648, -1, 0, 1, 16, 1023, 1024, 1025, 2147483647} {
		for _, fill := range []byte{0, 128, 255} {
			call, typ := pgcryptoRandomCall(t, "gen_random_bytes", count, false)
			ctx := &pgcryptoRandomContext{Context: context.Background(), fill: fill}
			got, err := call.Eval(ctx)
			if !reflect.DeepEqual(ctx.order, []int{0}) {
				t.Fatal("demand order", ctx.order)
			}
			if count < 1 || count > 1024 {
				var e *sqlstate.Error
				if !errors.As(err, &e) || e.Code != "39000" || e.Message != "Length not in range" || len(ctx.requests) != 0 || got.Type() != nil {
					t.Fatal(count, got, err, ctx.requests)
				}
				continue
			}
			if err != nil || got.Type() != typ || !reflect.DeepEqual(ctx.requests, []int{int(count)}) {
				t.Fatal(count, got, err, ctx.requests)
			}
			raw, err := got.Binary()
			if err != nil || !bytes.Equal([]byte(raw), bytes.Repeat([]byte{fill}, int(count))) {
				t.Fatal(count, raw, err)
			}
		}
	}
	call, typ := pgcryptoRandomCall(t, "gen_random_bytes", 0, true)
	ctx := &pgcryptoRandomContext{Context: context.Background()}
	got, err := call.Eval(ctx)
	if err != nil || got.Type() != typ || !got.IsNull() || len(ctx.requests) != 0 {
		t.Fatal(got, err, ctx)
	}
	childError := errors.New("demanded count failure")
	failing := call.Args[0].(pgcryptoRandomArgument)
	failing.failure = childError
	call.Args[0] = failing
	if _, err := call.Eval(ctx); !errors.Is(err, childError) || len(ctx.requests) != 0 {
		t.Fatal(err, ctx)
	}
	for _, v := range []struct {
		fill byte
		want string
	}{{0, "00000000000040008000000000000000"}, {255, "ffffffffffff4fffbfffffffffffffff"}} {
		call, typ := pgcryptoRandomCall(t, "gen_random_uuid", 0, false)
		ctx := &pgcryptoRandomContext{Context: context.Background(), fill: v.fill}
		got, err := call.Eval(ctx)
		if err != nil || got.Type() != typ || !reflect.DeepEqual(ctx.requests, []int{16}) {
			t.Fatal(got, err, ctx.requests)
		}
		raw, err := got.UUID()
		if err != nil || hex.EncodeToString(raw[:]) != v.want {
			t.Fatal(raw, err)
		}
		core := UUIDGeneration{Info: call.Info, Name: "gen_random_uuid"}
		coreContext := &pgcryptoRandomContext{Context: context.Background(), fill: v.fill}
		coreValue, err := core.Eval(coreContext)
		if err != nil || coreValue.Type() != typ || !reflect.DeepEqual(coreContext.requests, []int{16}) {
			t.Fatal("core V4", coreValue, err, coreContext.requests)
		}
		coreRaw, err := coreValue.UUID()
		if err != nil || hex.EncodeToString(coreRaw[:]) != v.want {
			t.Fatal("core V4 bits", coreRaw, err)
		}
	}
	for _, mode := range []bool{false, true} {
		call, typ := pgcryptoRandomCall(t, "fips_mode", 0, false)
		ctx := &pgcryptoRandomContext{Context: context.Background(), mode: mode}
		got, err := call.Eval(ctx)
		if err != nil || got.Type() != typ {
			t.Fatal(got, err)
		}
		value, err := got.Boolean()
		if err != nil || value != mode || len(ctx.requests) != 0 {
			t.Fatal(value, err, ctx.requests)
		}
	}
	for _, v := range []struct{ name, message string }{{"gen_random_bytes", "could not generate a random number"}, {"gen_random_uuid", "could not generate random values"}} {
		call, _ := pgcryptoRandomCall(t, v.name, 16, false)
		ctx := &pgcryptoRandomContext{Context: context.Background(), failure: errors.New("entropy unavailable")}
		got, err := call.Eval(ctx)
		var e *sqlstate.Error
		if !errors.As(err, &e) || e.Code != "XX000" || e.Message != v.message || got.Type() != nil {
			t.Fatal(v, got, err)
		}
	}
}

func TestPgcryptoRandomCancellation(t *testing.T) {
	for _, name := range []string{"gen_random_bytes", "gen_random_uuid", "fips_mode"} {
		call, _ := pgcryptoRandomCall(t, name, 16, false)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c := &pgcryptoRandomContext{Context: ctx}
		got, err := call.Eval(c)
		if !errors.Is(err, context.Canceled) || got.Type() != nil || len(c.order) != 0 || len(c.requests) != 0 {
			t.Fatal(name, got, err, c)
		}
		if name == "fips_mode" {
			continue
		}
		for _, failure := range []error{nil, errors.New("failed during cancellation")} {
			ctx, cancel := context.WithCancel(context.Background())
			c := &pgcryptoRandomContext{Context: ctx, cancel: cancel, failure: failure}
			got, err := call.Eval(c)
			cancel()
			if !errors.Is(err, context.Canceled) || got.Type() != nil {
				t.Fatal(name, got, err)
			}
		}
	}
}

func TestPgcryptoRandomBinding(t *testing.T) {
	types := formatUnitTypes()
	for _, v := range []struct {
		name   string
		result sqltype.ID
		args   []sqltype.ID
		valid  bool
	}{
		{"gen_random_bytes", sqltype.Bytea, []sqltype.ID{sqltype.Int4}, true},
		{"gen_random_bytes", sqltype.Text, []sqltype.ID{sqltype.Int4}, false},
		{"gen_random_bytes", sqltype.Bytea, nil, false}, {"gen_random_bytes", sqltype.Bytea, []sqltype.ID{sqltype.Int8}, false},
		{"gen_random_uuid", sqltype.UUID, nil, true}, {"gen_random_uuid", sqltype.Bytea, nil, false}, {"gen_random_uuid", sqltype.UUID, []sqltype.ID{sqltype.Int4}, false},
		{"fips_mode", sqltype.Bool, nil, true}, {"fips_mode", sqltype.UUID, nil, false}, {"fips_mode", sqltype.Bool, []sqltype.ID{sqltype.Bool}, false},
	} {
		args := make([]sqltype.Usage, len(v.args))
		for i, id := range v.args {
			args[i] = types[id].Usage()
		}
		_, err := sqltype.BindPgcrypto(v.name, types[v.result].Usage(), args...)
		if (err == nil) != v.valid {
			t.Fatal(v, err)
		}
	}
}
