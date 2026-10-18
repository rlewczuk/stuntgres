package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBinaryHashRuntimeDemand(t *testing.T) {
	types := formatUnitTypes()
	source := types[sqltype.Bytea]
	value, _ := sqltype.Binary(source, "abc")
	for _, name := range []string{"crc32", "crc32c", "md5", "sha224", "sha256", "sha384", "sha512"} {
		t.Run(name, func(t *testing.T) {
			id := sqltype.Bytea
			if name == "crc32" || name == "crc32c" {
				id = sqltype.Int8
			}
			if name == "md5" {
				id = sqltype.Text
			}
			usage := types[id].Usage()
			plan, err := sqltype.BindBinary(name, usage, source.Usage())
			if err != nil {
				t.Fatal(err)
			}
			var order []int
			node := Binary{Info: expr.Info{Usage: usage}, Plan: plan, Args: []expr.Expr{
				binaryUnitArgument{Info: expr.Info{Usage: source.Usage()}, value: value, order: &order, index: 0},
			}}
			got, err := node.Eval(&binaryUnitContext{Context: context.Background()})
			if err != nil || !reflect.DeepEqual(order, []int{0}) || got.Type() != usage.Type {
				t.Fatal(got, order, err)
			}
			failure := errors.New("hash child failure")
			node.Args[0] = binaryUnitArgument{Info: expr.Info{Usage: source.Usage()}, err: failure, order: &order, index: 0}
			order = nil
			if _, err := node.Eval(&binaryUnitContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0}) {
				t.Fatal(order, err)
			}
		})
	}
}
