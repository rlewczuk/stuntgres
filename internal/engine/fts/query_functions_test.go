package fts

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestFTSQueryFunctionsInspect(t *testing.T) {
	for _, tc := range []struct {
		input, tree string
		nodes       int32
	}{
		{"", "", 0}, {"(fat&rat)|cat", "'fat' & 'rat' | 'cat'", 5},
		{"a:*AB<2>!b", "'a':*AB", 4}, {"!!a", "T", 3},
		{"a&(b|!c)", "'a'", 6}, {"(a|!b)&(c|d)", "'c' | 'd'", 8},
		{"foo&!bar", "'foo'", 4}, {"!a<2>b", "'b'", 4},
		{"(a<2>b)&!(c|d)", "'a' <2> 'b'", 8},
	} {
		q := opsQuery(t, tc.input)
		before := append([]queryNode(nil), q.nodes...)
		got, err := q.IndexableTree(context.Background())
		if err != nil || got != tc.tree || q.NodeCount() != tc.nodes {
			t.Fatal(tc, got, q.NodeCount(), err)
		}
		if len(before) != len(q.nodes) || len(before) > 0 && !reflect.DeepEqual(before, q.nodes) {
			t.Fatal("inspection changed source")
		}
	}
}

func TestFTSQueryFunctionsRewrite(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ input, target, substitute, want string }{
		{"a&b", "a", "foo|bar", "'b' & ( 'foo' | 'bar' )"},
		{"a&(b&c)", "c&a", "x", "'b' & 'x'"},
		{"a&b&c&d", "absent", "x", "'c' & 'b' & 'a' & 'd'"},
		{"a&(b|c)&!d", "absent", "x", "'a' & !'d' & ( 'b' | 'c' )"},
		{"a:*A", "a:D", "x", "'x'"}, {"abc", "a:*", "x", "'abc'"},
		{"a&a", "a", "x", "'x' & 'x'"}, {"a", "a", "a|b", "'a' | 'b'"},
		{"5&(6|5)", "5", "", "'6'"}, {"!5", "5", "", ""},
		{"a<2>b", "b<2>a", "x", "'a' <2> 'b'"},
		{"a<->(b<->c)", "a<->b", "x", "'a' <-> ( 'b' <-> 'c' )"},
	} {
		q, target, substitute := opsQuery(t, tc.input), opsQuery(t, tc.target), opsQuery(t, tc.substitute)
		before := append([]queryNode(nil), q.nodes...)
		got, err := q.Rewrite(ctx, target, substitute)
		if err != nil || got.String() != tc.want {
			t.Fatal(tc, got, err)
		}
		if !reflect.DeepEqual(before, q.nodes) {
			t.Fatal("rewrite changed original")
		}
	}
	q := opsQuery(t, "a&a&a&a")
	got, err := q.Rewrite(ctx, opsQuery(t, "a&a"), opsQuery(t, "x"))
	if err != nil || got.NodeCount() != 5 {
		t.Fatal("one subset per node", got, err)
	}
	if got, err := q.Rewrite(ctx, opsQuery(t, ""), opsQuery(t, "x")); err != nil || got != q {
		t.Fatal("empty target must bypass normalization")
	}
	w, err := NewRewriteWorkspace(ctx, opsQuery(t, "a"))
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range [][2]string{{"a", "b"}, {"b", "c"}} {
		if err := w.ApplyRule(opsQuery(t, rule[0]), opsQuery(t, rule[1])); err != nil {
			t.Fatal(err)
		}
	}
	result, err := w.Finish()
	if err != nil || result.String() != "'c'" {
		t.Fatal("SQL row boundary", result, err)
	}
	// Generated depth is independent of the text parser's pending-NOT bound.
	deep := opsQuery(t, "a")
	for range 1024 {
		deep, err = deep.Not(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}
	result, err = deep.Rewrite(ctx, opsQuery(t, "a"), opsQuery(t, "b"))
	if err != nil || result.String() != strings.Repeat("!", 1024)+"'b'" {
		t.Fatal("deep generated rewrite", err)
	}
}

func legacyCRCIndependent(word string) int32 {
	crc := uint32(0xffffffff)
	for i := range len(word) {
		entry := uint32(byte(crc>>24) ^ word[i])
		for range 8 {
			if entry&1 != 0 {
				entry = (entry >> 1) ^ 0xedb88320
			} else {
				entry >>= 1
			}
		}
		crc = (crc << 8) ^ entry
	}
	return int32(crc ^ 0xffffffff)
}
func TestFTSQueryFunctionsComparator(t *testing.T) {
	w := &RewriteWorkspace{ctx: context.Background()}
	positive, negative := false, false
	for _, word := range []string{"a", "b", "c", "d", "aa", "Z", "é", "space word"} {
		root, err := w.importQuery(opsQuery(t, "'"+word+"'"))
		if err != nil {
			t.Fatal(err)
		}
		want := legacyCRCIndependent(word)
		if w.nodes[root].crc != want {
			t.Fatal("legacy CRC", word, w.nodes[root].crc, want)
		}
		positive = positive || want >= 0
		negative = negative || want < 0
	}
	if !positive || !negative {
		t.Fatal("word inventory must exercise signed CRC boundary")
	}
	for _, tc := range []struct {
		left, right string
		equal       bool
	}{
		{"a:*A", "a:D", true}, {"a", "A", false},
		{"a<2>b", "a<->b", false}, {"a<->b", "b<->a", false},
	} {
		a, err := w.importQuery(opsQuery(t, tc.left))
		if err != nil {
			t.Fatal(err)
		}
		b, err := w.importQuery(opsQuery(t, tc.right))
		if err != nil {
			t.Fatal(err)
		}
		c, err := w.cmp(a, b)
		if err != nil || (c == 0) != tc.equal {
			t.Fatal(tc, c, err)
		}
	}
	// Force a hash tie to prove lexeme bytes, not CRC alone, establish identity.
	a, _ := w.importQuery(opsQuery(t, "a"))
	b, _ := w.importQuery(opsQuery(t, "b"))
	w.nodes[b].crc = w.nodes[a].crc
	if c, err := w.cmp(a, b); err != nil || c >= 0 {
		t.Fatal("CRC collision byte fallback", c, err)
	}
}

type queryFunctionsCancelContext struct {
	context.Context
	remaining int
	observed  int
}

func (c *queryFunctionsCancelContext) Err() error {
	c.observed++
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}
func TestFTSQueryFunctionsCancellation(t *testing.T) {
	q := opsQuery(t, strings.Repeat("a&", 1024)+"a")
	before := append([]queryNode(nil), q.nodes...)
	for _, stage := range []string{"import", "crc", "normalize", "sort", "compare", "match", "finish", "render"} {
		t.Run(stage, func(t *testing.T) {
			w, err := NewRewriteWorkspace(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			target, err := w.importQuery(opsQuery(t, strings.Repeat("a&", 512)+"a"))
			if err != nil {
				t.Fatal(err)
			}
			if err := w.normalize(target); err != nil {
				t.Fatal(err)
			}
			if stage == "sort" {
				for i, child := range w.nodes[w.root].children {
					w.nodes[child].crc = int32(i)
				}
			}
			c := &queryFunctionsCancelContext{Context: context.Background(), remaining: 2}
			w.ctx, w.work = c, 0
			switch stage {
			case "import":
				_, err = w.importQuery(q)
			case "crc":
				_, err = w.importQuery(opsQuery(t, strings.Repeat("x", 2046)))
			case "normalize":
				err = w.normalize(w.root)
			case "sort":
				err = w.sortChildren(w.root)
			case "compare":
				_, err = w.cmp(w.root, w.root)
			case "match":
				_, err = w.match(w.root, target, opsQuery(t, "x"))
			case "finish":
				_, err = w.Finish()
			case "render":
				_, err = q.render(c, false)
			}
			if !errors.Is(err, context.Canceled) || c.observed == 0 {
				t.Fatal("stage cancellation", stage, err)
			}
			if !reflect.DeepEqual(before, q.nodes) {
				t.Fatal("canceled rewrite changed source")
			}
		})
	}
}

func TestFTSQueryFunctionsWorkspaceEmpty(t *testing.T) {
	ctx := context.Background()
	initial, target, substitute := opsQuery(t, "a"), opsQuery(t, "a"), opsQuery(t, "")
	before := append([]queryNode(nil), initial.nodes...)
	workspace, err := NewRewriteWorkspace(ctx, initial)
	if err != nil || workspace.Empty() {
		t.Fatal("initial accumulator", err)
	}
	if err := workspace.ApplyRule(target, substitute); err != nil {
		t.Fatal(err)
	}
	if !workspace.Empty() || !workspace.Empty() {
		t.Fatal("empty state requires no finalization")
	}
	if !reflect.DeepEqual(before, initial.nodes) || initial.String() != "'a'" {
		t.Fatal("empty inspection mutated original")
	}
}
