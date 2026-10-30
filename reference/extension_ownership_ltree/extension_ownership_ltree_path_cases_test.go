package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Independent expectations reuse the existing path values/slices and scalar
// lquery/ltxtquery matching corpora. Their package-local helpers are not importable.
type ownershipLtreePathValue struct {
	args []any
	want any
}

type ownershipLtreePathOperation struct {
	name, symbol, result, operator string
	types                          []string
	cases                          []ownershipLtreePathValue
}

func ownershipLtreePathOperations() []ownershipLtreePathOperation {
	ops := []ownershipLtreePathOperation{
		{"subltree", "subltree", "text", "", []string{"ltree", "integer", "integer"}, []ownershipLtreePathValue{{[]any{"Top.Child1.Child2", 1, 2}, "Child1"}, {[]any{"Top.Child1.Child2", 1, 1}, ""}}},
		{"subpath", "subpath", "text", "", []string{"ltree", "integer", "integer"}, []ownershipLtreePathValue{{[]any{"Top.Child1.Child2", -1, 1}, "Child2"}, {[]any{"Top.Child1.Child2", 0, -2}, "Top"}}},
		{"subpath", "subpath", "text", "", []string{"ltree", "integer"}, []ownershipLtreePathValue{{[]any{"Top.Child1.Child2", 1}, "Child1.Child2"}, {[]any{"A.B.C", -4}, "C"}}},
		{"index", "ltree_index", "integer", "", []string{"ltree", "ltree"}, []ownershipLtreePathValue{{[]any{"A.B.A.B", "A.B"}, int64(0)}, {[]any{"Topical", "Top"}, int64(-1)}}},
		{"index", "ltree_index", "integer", "", []string{"ltree", "ltree", "integer"}, []ownershipLtreePathValue{{[]any{"A.B.A.B", "A.B", 1}, int64(2)}, {[]any{"A.B.A.B", "A.B", -2}, int64(2)}}},
		{"nlevel", "nlevel", "integer", "", []string{"ltree"}, []ownershipLtreePathValue{{[]any{"Top.Child1.Child2"}, int64(3)}, {[]any{""}, int64(0)}}},
		{"ltree2text", "ltree2text", "text", "", []string{"ltree"}, []ownershipLtreePathValue{{[]any{"AbC.a_b.-.42"}, "AbC.a_b.-.42"}, {[]any{""}, ""}}},
		{"text2ltree", "text2ltree", "text", "", []string{"text"}, []ownershipLtreePathValue{{[]any{"AbC.a_b.-.42"}, "AbC.a_b.-.42"}, {[]any{""}, ""}}},
		{"lca", "_lca", "text", "", []string{"ltree[]"}, []ownershipLtreePathValue{{[]any{"[7:8]={A.B.C,A.B.D}"}, "A.B"}, {[]any{"{A.B.C}"}, "A.B"}, {[]any{"{}"}, nil}}},
	}
	for n := 2; n <= 8; n++ {
		types, same, branch := make([]string, n), make([]any, n), make([]any, n)
		for i := range n {
			types[i], same[i], branch[i] = "ltree", "A.B.C", "A.B.C"
		}
		branch[n-1] = "A.X.Y"
		ops = append(ops, ownershipLtreePathOperation{"lca", "lca", "text", "", types, []ownershipLtreePathValue{{same, "A.B"}, {branch, "A"}}})
	}
	for _, tc := range []struct{ name, left, right, a, b string }{
		{"ltree_addltree", "ltree", "ltree", "A", "B.C"},
		{"ltree_addtext", "ltree", "text", "A", "B.C"},
		{"ltree_textadd", "text", "ltree", "A.B", "C"},
	} {
		ops = append(ops, ownershipLtreePathOperation{tc.name, tc.name, "text", "||", []string{tc.left, tc.right}, []ownershipLtreePathValue{
			{[]any{tc.a, tc.b}, "A.B.C"}, {[]any{"", tc.b}, tc.b}, {[]any{tc.a, ""}, tc.a}, {[]any{"", ""}, ""},
		}})
	}
	for direction, name := range []string{"ltq_regex", "ltq_rregex"} {
		types := []string{"ltree", "lquery"}
		values := []ownershipLtreePathValue{
			{[]any{"Top.A.Child", "Top.*{1,2}.Child"}, true},
			{[]any{"Top.Child", "Top.*{1,2}.Child"}, false},
			{[]any{"Top.A.Child.Tail", "Top.*{1,2}.Child"}, false},
			{[]any{"FOO1_BAR2_baz", "foo_bar%@*"}, true}, {[]any{"", "*"}, true},
		}
		if direction == 1 {
			types[0], types[1] = types[1], types[0]
			for i := range values {
				values[i].args[0], values[i].args[1] = values[i].args[1], values[i].args[0]
			}
		}
		ops = append(ops, ownershipLtreePathOperation{name, name, "boolean", "~", types, values})
	}
	for direction, name := range []string{"ltxtq_exec", "ltxtq_rexec"} {
		types := []string{"ltree", "ltxtquery"}
		values := []ownershipLtreePathValue{
			{[]any{"Russia.x.Europe", "Europe & Russia"}, true},
			{[]any{"Europe.x", "Europe & Russia"}, false},
			{[]any{"Top.Blocked", "Top | Europe & !Blocked"}, true},
			{[]any{"Top.Blocked", "(Top | Europe) & !Blocked"}, false},
			{[]any{"Europe", "!Top & Europe"}, true},
			{[]any{"Top.Other", "!Top"}, false},
			{[]any{"", "foo"}, false}, {[]any{"", "!foo"}, true},
			{[]any{"Foo", "foo"}, false}, {[]any{"Foo", "foo@"}, true},
			{[]any{"food", "foo*"}, true},
			{[]any{"FOO1_BAR2_baz", "foo_bar%@*"}, true},
			{[]any{"foo.bar", "foo_bar%"}, false},
		}
		if direction == 1 {
			types[0], types[1] = types[1], types[0]
			for i := range values {
				values[i].args[0], values[i].args[1] = values[i].args[1], values[i].args[0]
			}
		}
		ops = append(ops, ownershipLtreePathOperation{name, name, "boolean", "@", types, values})
	}
	return ops
}

func ownershipLtreePathType(typ string) string {
	if strings.HasPrefix(typ, "ltree") || typ == "lquery" || typ == "ltxtquery" {
		return "eo_new." + typ
	}
	return typ
}

func (op ownershipLtreePathOperation) signature(renamed bool) string {
	name := op.name
	if renamed {
		name = "eo_path_" + name
	}
	types := make([]string, len(op.types))
	for i, typ := range op.types {
		types[i] = ownershipLtreePathType(typ)
	}
	return "eo_new." + name + "(" + strings.Join(types, ",") + ")"
}

func (op ownershipLtreePathOperation) operatorSignature() string {
	return "eo_new." + op.operator + "(" + ownershipLtreePathType(op.types[0]) + "," + ownershipLtreePathType(op.types[1]) + ")"
}

func (op ownershipLtreePathOperation) step(value ownershipLtreePathValue, renamed, operator, prepared bool) fixture.Step {
	args := make([]string, len(op.types))
	for i, typ := range op.types {
		literal := "NULL"
		if value.args[i] != nil {
			literal = "'" + strings.ReplaceAll(fmt.Sprint(value.args[i]), "'", "''") + "'"
		}
		if prepared {
			literal = fmt.Sprintf("$%d", i+1)
		}
		args[i] = literal + "::" + ownershipLtreePathType(typ)
	}
	expression := strings.Split(op.signature(renamed), "(")[0] + "(" + strings.Join(args, ",") + ")"
	if operator {
		expression = args[0] + " OPERATOR(eo_new." + op.operator + ") " + args[1]
	}
	family := "TEXT"
	if op.result == "integer" {
		family = "INT4"
	}
	if op.result == "boolean" {
		family = "BOOL"
	}
	// Explicit text presentation is portable; pg_proc separately proves the
	// genuine nominal result identity rather than relying on driver type labels.
	if family == "TEXT" {
		expression = "(" + expression + ")::text"
	}
	s := fixture.Select("SELECT "+expression+" AS v", []string{"v"}, []string{family}, [][]any{{value.want}})
	if prepared {
		s.Args, s.Prepared = value.args, true
	}
	return s
}

// Partition only values, never operation identities or restore destinations.
// Generated strict-NULL operands belong to the same disjoint inventory.
func (op ownershipLtreePathOperation) values() []ownershipLtreePathValue {
	values := append([]ownershipLtreePathValue(nil), op.cases...)
	for operand := range op.types {
		args := append([]any(nil), op.cases[0].args...)
		args[operand] = nil
		values = append(values, ownershipLtreePathValue{args, nil})
	}
	return values
}

func TestReferenceExtensionOwnershipLtreePathQuerySurvivors(t *testing.T) {
	for index, op := range ownershipLtreePathOperations() {
		t.Run(fmt.Sprintf("%02d_%s", index, op.name), func(t *testing.T) {
			for _, foreign := range []bool{false, true} {
				state := "detached"
				if foreign {
					state = "foreign"
				}
				t.Run(state, func(t *testing.T) {
					values := op.values()
					// Each bounded value inventory shares one genuine source graph
					// across both destinations. Its stored views also bound the work
					// of each independently checked pg_depend projection.
					if len(values) <= 6 {
						ownershipLtreePathQuerySurvivor(t, op, values, foreign, state, "")
						return
					}
					for start := 0; start < len(values); start += 6 {
						t.Run(fmt.Sprintf("inventory_%02d", start), func(t *testing.T) {
							ownershipLtreePathQuerySurvivor(t, op, values[start:min(start+6, len(values))], foreign, state, "")
						})
					}
				})
			}
		})
	}
}
