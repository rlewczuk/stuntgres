package functions

import (
	"context"
	"encoding/hex"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPgcryptoAESCarrier(t *testing.T) {
	types := formatUnitTypes()
	text, binary, integer := types[sqltype.Text].Usage(), types[sqltype.Bytea].Usage(), types[sqltype.Int4].Usage()
	for _, name := range []string{"encrypt", "decrypt", "encrypt_iv", "decrypt_iv"} {
		count := 3
		if name == "encrypt_iv" || name == "decrypt_iv" {
			count = 4
		}
		args := make([]Argument, count)
		input, resolution := PgcryptoInput(name, args, text, binary, integer)
		if resolution != Resolved || input.Type.ID() != sqltype.Bytea {
			t.Fatal(name, input, resolution)
		}
		for i := range count - 1 {
			args[i].Usage = text
			if _, resolution := PgcryptoInput(name, args, text, binary, integer); resolution != Missing {
				t.Fatal("TEXT raw overload admitted", name, i)
			}
			args[i].Usage = binary
		}
	}
	var order []int
	value := func(s string) sqltype.Value {
		v, err := sqltype.Binary(binary.Type, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	args := []expr.Expr{
		binaryUnitArgument{Info: expr.Info{Usage: binary}, value: sqltype.Null(binary.Type), order: &order, index: 0},
		binaryUnitArgument{Info: expr.Info{Usage: binary}, value: value("0123456"), order: &order, index: 1},
		binaryUnitArgument{Info: expr.Info{Usage: binary}, value: value("abcd"), order: &order, index: 2},
		binaryUnitArgument{Info: expr.Info{Usage: text}, value: sqltype.String(text.Type, "NO_SUCH_CIPHER"), order: &order, index: 3},
	}
	call, err := NewPgcrypto(expr.Info{Usage: binary}, "encrypt_iv", args)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &binaryUnitContext{Context: context.Background()}
	got, err := call.Eval(ctx)
	if err != nil || !got.IsNull() || !reflect.DeepEqual(order, []int{0, 1, 2, 3}) {
		t.Fatal(got, order, err)
	}
	failure := errors.New("demanded IV")
	args[2] = binaryUnitArgument{Info: expr.Info{Usage: binary}, err: failure, order: &order, index: 2}
	order = nil
	if _, err := call.Eval(ctx); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1, 2}) {
		t.Fatal(order, err)
	}
	args[0] = binaryUnitArgument{Info: expr.Info{Usage: binary}, value: value("foo")}
	args[2] = binaryUnitArgument{Info: expr.Info{Usage: binary}, value: value("abcd")}
	args[3] = binaryUnitArgument{Info: expr.Info{Usage: text}, value: sqltype.String(text.Type, "aes")}
	got, err = call.Eval(ctx)
	raw, _ := got.Binary()
	if err != nil || hex.EncodeToString([]byte(raw)) != "2c24cb7da91d6d5699801268b0f5adad" {
		t.Fatal(raw, err)
	}
	rebuilt, err := call.Reconstruct(call.Info, args)
	if err != nil || rebuilt.Plan != call.Plan || rebuilt.Validate() != nil {
		t.Fatal("four-slot reconstruction", err)
	}
	bad := append([]expr.Expr(nil), args...)
	bad[2] = binaryUnitArgument{Info: expr.Info{Usage: text}, value: sqltype.String(text.Type, "abcd")}
	if _, err := call.Reconstruct(call.Info, bad); err == nil {
		t.Fatal("invalid IV type admitted")
	}
	if _, err := call.Reconstruct(call.Info, args[:3]); err == nil {
		t.Fatal("missing IV child admitted")
	}
}
