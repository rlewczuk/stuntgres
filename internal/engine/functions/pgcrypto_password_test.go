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
	sizes   []int
	notices []sqlstate.Error
}

func (c *pgcryptoEntropyUnitContext) FunctionContext() context.Context { return c.Context }
func (c *pgcryptoEntropyUnitContext) FunctionPgcryptoEntropy(dst []byte) error {
	c.calls++
	c.sizes = append(c.sizes, len(dst))
	if c.cancel != nil {
		c.cancel()
	}
	clear(dst)
	return c.failure
}

func (c *pgcryptoEntropyUnitContext) FunctionPgcryptoNotice(n sqlstate.Error) {
	c.notices = append(c.notices, n)
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
		size          int
		want          string
	}{
		{"bf", 4, false, 1, "", "", 16, "$2a$04$......................"},
		{"BF", 0, false, 1, "", "", 16, "$2a$06$......................"},
		{"des", 0, false, 1, "", "", 2, ".."},
		{"DES", 25, false, 1, "", "", 2, ".."},
		{"xdes", 0, false, 1, "", "", 3, "_J9......"},
		{"xdes", 16777215, false, 1, "", "", 3, "_zzzz...."},
		{"md5", 1000, false, 1, "", "", 6, "$1$........"},
		{"sha256crypt", 0, false, 1, "", "", 16, "$5$rounds=5000$................"},
		{"SHA512CRYPT", 1000, false, 1, "", "", 16, "$6$rounds=1000$................"},
		{"sha256crypt", 999999999, false, 1, "", "", 16, "$5$rounds=999999999$................"},
		{"sha512crypt", 999999999, false, 1, "", "", 16, "$6$rounds=999999999$................"},
		{"sha256crypt", 999, false, 0, "22023", "gen_salt: Incorrect number of rounds", 0, ""},
		{"sha512crypt", 1000000000, false, 0, "22023", "gen_salt: Incorrect number of rounds", 0, ""},
		{"bf", 3, false, 0, "22023", "gen_salt: Incorrect number of rounds", 0, ""},
		{"des", 1, false, 0, "22023", "gen_salt: Incorrect number of rounds", 0, ""},
		{"xdes", 2, false, 0, "22023", "gen_salt: Incorrect number of rounds", 0, ""},
		{"md5", 999, false, 0, "22023", "gen_salt: Incorrect number of rounds", 0, ""},
		{"unknown", 4, false, 0, "22023", "gen_salt: Unknown salt algorithm", 0, ""},
		{"bf", 3, true, 0, "", "", 0, ""},
		{"des", 1, true, 0, "", "", 0, ""},
		{"xdes", 2, true, 0, "", "", 0, ""},
		{"md5", 999, true, 0, "", "", 0, ""},
		{"sha256crypt", -1, true, 0, "", "", 0, ""},
		{"sha512crypt", 1000000000, true, 0, "", "", 0, ""},
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
			if !reflect.DeepEqual(ctx.sizes, []int{v.size}) {
				t.Fatal("entropy request sizes", ctx.sizes, v.size)
			}
			want := v.want
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

func TestPgcryptoPasswordNotices(t *testing.T) {
	text := formatUnitTypes()[sqltype.Text]
	args := []pgcryptoEntropyArgument{
		{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, "p"), index: 0},
		{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, "$5$rounds=10$!"), index: 1},
	}
	call, err := NewPgcrypto(expr.Info{Usage: text.Usage()}, "crypt", unitArgs(args))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		ctx := &pgcryptoEntropyUnitContext{Context: context.Background()}
		got, err := call.Eval(ctx)
		want := sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "22003", Message: "rounds=10 is below supported value (1000), using 1000 instead"}
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || got.Type() != nil || !reflect.DeepEqual(ctx.notices, []sqlstate.Error{want}) || !reflect.DeepEqual(ctx.order, []int{0, 1}) || ctx.calls != 0 {
			t.Fatal(got, err, ctx)
		}
	}
	args[0].value = sqltype.Null(text)
	strict, err := NewPgcrypto(expr.Info{Usage: text.Usage()}, "crypt", unitArgs(args))
	if err != nil {
		t.Fatal(err)
	}
	ctx := &pgcryptoEntropyUnitContext{Context: context.Background()}
	got, err := strict.Eval(ctx)
	if err != nil || !got.IsNull() || len(ctx.notices) != 0 || !reflect.DeepEqual(ctx.order, []int{0, 1}) {
		t.Fatal(got, err, ctx)
	}
}
