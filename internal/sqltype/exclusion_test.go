package sqltype

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestExclusionOperatorFamily(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, hash := range []bool{false, true} {
		for _, tc := range []struct {
			id                 ID
			class, left, right string
			equal              bool
		}{
			{Int4, "int4_ops", "1", "1", true},
			{Int4, "int4_ops", "1", "2", false},
			{Oid, "oid_ops", "4294967295", "4294967295", true},
			{Text, "text_ops", "a", "a ", false},
			{Text, "text_pattern_ops", "a", "a", true},
			{VarChar, "varchar_pattern_ops", "a", "a ", false},
			{BPChar, "bpchar_ops", "a", "a ", true},
			{BPChar, "bpchar_pattern_ops", "a", "a ", true},
			{Float8, "float8_ops", "NaN", "NaN", true},
			{Float8, "float8_ops", "-0", "0", true},
			{Numeric, "numeric_ops", "1.00", "1", true},
			{Date, "date_ops", "2001-02-03", "2001-02-03", true},
			{Timestamp, "timestamp_ops", "2001-02-03 04:05:06", "2001-02-03 04:05:06", true},
			{Interval, "interval_ops", "1 day", "24 hours", true},
		} {
			method := "btree"
			if hash {
				method = "hash"
			}
			t.Run(method+"/"+types[tc.id].Name()+"/"+tc.class+"/"+tc.left+"/"+tc.right, func(t *testing.T) {
				for _, domain := range []bool{false, true} {
					typ := types[tc.id]
					if domain {
						var err error
						typ, err = Domain(20000+tc.id, "ex_domain", "public.ex_domain", typ.Usage())
						if err != nil {
							t.Fatal(err)
						}
					}
					class, ok := LookupIndexClass(tc.class)
					if hash {
						class, ok = LookupHashIndexClass(tc.class)
					}
					if !ok || !class.Accepts(typ) {
						t.Fatal("missing actual class", class)
					}
					operator, err := bindTestExclusionOperator(typ.Usage(), class, "=")
					if err != nil {
						t.Fatal(err)
					}
					left, err := typ.Input(ctx, tc.left, -1)
					if err != nil {
						t.Fatal(err)
					}
					right, err := typ.Input(ctx, tc.right, -1)
					if err != nil {
						t.Fatal(err)
					}
					got, err := operator.Matches(ctx, left, right)
					if err != nil || got != tc.equal {
						t.Fatal(got, tc.equal, err)
					}
					for _, pair := range [][2]Value{{Null(typ), right}, {left, Null(typ)}, {Null(typ), Null(typ)}} {
						if match, err := operator.Matches(ctx, pair[0], pair[1]); err != nil || match {
							t.Fatal("NULL exclusion comparison", match, err)
						}
					}
					if hash && tc.equal {
						provider, err := bindTestIndexHash(typ.Usage(), class)
						if err != nil {
							t.Fatal(err)
						}
						a, err := provider.Hash(ctx, left)
						if err != nil {
							t.Fatal(err)
						}
						b, err := provider.Hash(ctx, right)
						if err != nil || a != b {
							t.Fatal("equality/hash disagreement", a, b, err)
						}
					}
					canceled, cancel := context.WithCancel(ctx)
					cancel()
					if _, err := operator.Matches(canceled, left, right); !errors.Is(err, context.Canceled) {
						t.Fatal("provider cancellation", err)
					}
				}
			})
		}
		for _, tc := range []struct {
			id    ID
			class string
		}{{Int4, "oid_ops"}, {Text, "bpchar_ops"}, {VarChar, "bpchar_pattern_ops"}} {
			class, _ := LookupIndexClass(tc.class)
			if hash {
				class, _ = LookupHashIndexClass(tc.class)
			}
			if !class.Accepts(types[tc.id]) {
				t.Fatal("counterexample must pass broad class admission")
			}
			_, err := bindTestExclusionOperator(types[tc.id].Usage(), class, "=")
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42809" {
				t.Fatal("original operator family identity lost", tc, err)
			}
		}
	}
	class, _ := LookupIndexClass("int4_ops")
	for _, tc := range []struct{ operator, code string }{{"<", "42809"}, {"<>", "42809"}, {"!=", "42809"}, {"&&", "42883"}, {"^", "42883"}} {
		_, err := bindTestExclusionOperator(types[Int4].Usage(), class, tc.operator)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
			t.Fatal(tc, err)
		}
	}
}
