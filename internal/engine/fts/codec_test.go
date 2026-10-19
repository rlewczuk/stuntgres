package fts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestFTSVectorCanonical(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"a fat cat sat on a mat and ate a fat rat", "'a' 'and' 'ate' 'cat' 'fat' 'mat' 'on' 'rat' 'sat'"},
		{"The Fat Rats", "'Fat' 'Rats' 'The'"}, {"a:3B,1D,3A a:2c b:16384D", "'a':1,2C,3A 'b':16383"},
		{"", ""}, {" \t\n", ""}, {"'space word' a aa A é b", "'A' 'a' 'aa' 'b' 'space word' 'é'"},
		{"'a'b a'c 'x\\\\y' 'a''q'", "'a' 'a''c' 'a''q' 'b' 'x\\\\y'"},
		{"a a:2c a:1d", "'a':1,2C"}, {"a:1DDa a:1A2", "'a':1A"},
		{"a:1" + strings.Repeat(",1", 4096) + ",1A", "'a':1A"},
	} {
		v, err := ParseVector(context.Background(), tc.input)
		if err != nil || v.String() != tc.want {
			t.Fatalf("input=%q output=%v err=%v want=%q", tc.input, v, err, tc.want)
		}
		again, err := ParseVector(context.Background(), v.String())
		if err != nil {
			t.Fatal(err)
		}
		same, err := v.Equal(context.Background(), again)
		if err != nil || !same {
			t.Fatal("canonical structure", err)
		}
	}
}

func codecError(t *testing.T, err error, code, message string) {
	t.Helper()
	var d *sqlstate.Error
	if !errors.As(err, &d) || *d != (sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}) {
		t.Fatalf("diagnostic=%#v err=%v want %s %q", d, err, code, message)
	}
}

func TestFTSVectorLimits(t *testing.T) {
	ctx := context.Background()
	for _, pos := range []struct{ input, want string }{{"2147483647", "16383"}, {"4294967295", "16383"}, {"18446744073709551616", "16383"}} {
		v, err := ParseVector(ctx, "a:"+pos.input)
		if err != nil || v.String() != "'a':"+pos.want {
			t.Fatal(v, err)
		}
	}
	for _, pos := range []string{"0", "2147483648", "4294967296"} {
		_, err := ParseVector(ctx, "a:"+pos)
		codecError(t, err, "42601", "wrong position info in tsvector: \"a:"+pos+"\"")
	}
	positions := make([]string, 257)
	for i := range positions {
		positions[i] = fmt.Sprint(257 - i)
	}
	v, err := ParseVector(ctx, "a:"+strings.Join(positions, ","))
	if err != nil || len(v.lexemes[0].positions) != 256 || v.lexemes[0].positions[255].number != 256 {
		t.Fatal(v, err)
	}
	positions = positions[:255]
	for i := range positions {
		positions[i] = fmt.Sprint(i + 1)
	}
	for _, tail := range []struct {
		input  string
		weight byte
	}{{"256D,256A", 0}, {"256A,256D", 3}} {
		v, err := ParseVector(ctx, "a:"+strings.Join(positions, ",")+","+tail.input)
		if err != nil || v.lexemes[0].positions[255].weight != tail.weight {
			t.Fatal(v, err)
		}
	}
	_, err = ParseVector(ctx, strings.Repeat("x", 2047))
	codecError(t, err, "54000", "word is too long (2047 bytes, max 2046 bytes)")
	_, err = ParseVector(ctx, strings.Repeat(strings.Repeat("x", 2046)+" ", 514))
	codecError(t, err, "54000", "string is too long for tsvector (1049598 bytes, max 1048575 bytes)")
	words := make([]string, 512)
	for i := range words {
		words[i] = fmt.Sprintf("%04d", i) + strings.Repeat("x", 2036) + ":1,2,3"
	}
	// Each positioned word uses2040+2+6 bytes;512*2048=1048576.
	_, err = ParseVector(ctx, strings.Join(words, " "))
	codecError(t, err, "54000", "string is too long for tsvector (1048576 bytes, max 1048575 bytes)")
}

func TestFTSQueryGrammar(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"fat & (rat | cat)", "'fat' & ( 'rat' | 'cat' )"}, {"a <-> (b <-> c)", "'a' <-> ( 'b' <-> 'c' )"},
		{"!!b", "!!'b'"}, {"a & a", "'a' & 'a'"}, {"a | b & c <-> !d", "'a' | 'b' & 'c' <-> !'d'"},
		{"a:* & nbb:*ac | doo:a* | goo", "'a':* & 'nbb':*AC | 'doo':*A | 'goo'"}, {"a:", "'a'"}, {"a <0> b", "'a' <0> 'b'"}, {"a <16384> b", "'a' <16384> 'b'"}, {" \t\n", ""},
	} {
		q, err := ParseQuery(context.Background(), tc.input)
		if err != nil || q.String() != tc.want {
			t.Fatalf("input=%q output=%v err=%v want=%q", tc.input, q, err, tc.want)
		}
	}
	for _, tc := range []struct{ input, code, message string }{{"a &", "42601", "no operand in tsquery: \"a &\""}, {"a b", "42601", "syntax error in tsquery: \"a b\""}, {"a <16385> b", "22023", "distance in phrase operator must be an integer value between zero and 16384 inclusive"}} {
		_, err := ParseQuery(context.Background(), tc.input)
		codecError(t, err, tc.code, tc.message)
	}
}

func TestFTSQueryStructure(t *testing.T) {
	ctx := context.Background()
	q, err := ParseQuery(ctx, "a & (b | c:ac*) <2> !!d")
	if err != nil {
		t.Fatal(err)
	}
	root := q.nodes[q.root]
	if root.op != '&' || q.nodes[root.left].word != "a" {
		t.Fatal(q.nodes)
	}
	phrase := q.nodes[root.right]
	if phrase.op != '<' || phrase.distance != 2 || q.nodes[phrase.left].op != '|' {
		t.Fatal(q.nodes)
	}
	operand := q.nodes[q.nodes[phrase.left].right]
	if operand.word != "c" || operand.mask != 10 || !operand.prefix {
		t.Fatal(operand)
	}
	not := q.nodes[phrase.right]
	if not.op != '!' || q.nodes[not.right].op != '!' {
		t.Fatal(q.nodes)
	}
	left, _ := ParseQuery(ctx, "(a & b) & c")
	right, _ := ParseQuery(ctx, "a & (b & c)")
	if left.String() != right.String() {
		t.Fatal("fixture must witness same text")
	}
	if same, _ := left.Equal(ctx, right); same {
		t.Fatal("association erased")
	}
	again, _ := ParseQuery(ctx, "a & b & c")
	if same, _ := left.Equal(ctx, again); !same {
		t.Fatal("equivalent independent arena differs")
	}
	for _, input := range []string{"a & (b & c)", "a | (b | c)"} {
		before, err := ParseQuery(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := before.InputText(ctx)
		if err != nil {
			t.Fatal(err)
		}
		after, err := ParseQuery(ctx, encoded)
		if err != nil {
			t.Fatal(err)
		}
		if same, err := before.Equal(ctx, after); err != nil || !same {
			t.Fatal("lossless structural encoding", encoded, err)
		}
	}
}

func TestFTSQueryLimits(t *testing.T) {
	ctx := context.Background()
	if _, err := ParseQuery(ctx, strings.Repeat("!", 32)+"a"); err != nil {
		t.Fatal(err)
	}
	_, err := ParseQuery(ctx, strings.Repeat("!", 33)+"a")
	codecError(t, err, "XX000", "tsquery stack too small")
	if _, err := ParseQuery(ctx, "a | ("+strings.Repeat("!", 32)+"b)"); err != nil {
		t.Fatal(err)
	}
	_, err = ParseQuery(ctx, "a | "+strings.Repeat("!", 32)+"b")
	codecError(t, err, "XX000", "tsquery stack too small")
	q, err := ParseQuery(ctx, strings.Repeat("a & ", 16384)+"a")
	if err != nil || len(q.nodes) != 32769 {
		t.Fatal("flat chain", err)
	}
	const max = uint64(1<<30 - 1)
	n := (max - 8) / 12
	if queryTooBig(n, 0) || !queryTooBig(n+1, 0) || !queryTooBig(0, max) || queryTooBig(0, max-8) {
		t.Fatal("allocation arithmetic boundary")
	}
	word := strings.Repeat("x", 2046)
	words := make([]string, 512)
	for i := range words {
		words[i] = word
	}
	input := strings.Join(append(words, strings.Repeat("y", 510), "z"), " & ")
	//512*(2046+1)+(510+1)=1048575: final operand start, not total.
	_, err = ParseQuery(ctx, input)
	codecError(t, err, "54000", "value is too big in tsquery: \""+input+"\"")
}

type countingContext struct {
	context.Context
	calls, after int
}

func (c *countingContext) Err() error {
	c.calls++
	if c.calls >= c.after {
		return context.Canceled
	}
	return nil
}
func TestFTSCodecCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ParseVector(ctx, "a"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := ParseQuery(ctx, "a"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, query := range []bool{false, true} {
		ctx := &countingContext{Context: context.Background(), after: 5}
		var err error
		if query {
			_, err = ParseQuery(ctx, strings.Repeat("a & ", 2048)+"b")
		} else {
			_, err = ParseVector(ctx, strings.Repeat("a ", 2048))
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal("active parse", err)
		}
	}
	v, _ := ParseVector(context.Background(), "a")
	if _, err := v.Equal(ctx, v); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	q, _ := ParseQuery(context.Background(), "a")
	if _, err := q.Equal(ctx, q); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	long := strings.Repeat("a & ", 1024) + "b"
	left, _ := ParseQuery(context.Background(), long)
	right, _ := ParseQuery(context.Background(), long)
	active := &countingContext{Context: context.Background(), after: 3}
	if _, err := left.Equal(active, right); !errors.Is(err, context.Canceled) {
		t.Fatal("active arena traversal", err)
	}
}

func TestFTSVectorCanonicalCancellation(t *testing.T) {
	t.Run("lexeme-sort", func(t *testing.T) {
		entries := make([]lexeme, 4096)
		for i := range entries {
			entries[i].word = fmt.Sprintf("%04d", len(entries)-i)
		}
		ctx := &countingContext{Context: context.Background(), after: 10}
		// Enter the same post-lexing stage as ParseVector, with no lexer work
		// able to consume the cancellation budget.
		v, err := canonicalVector(ctx, entries)
		if v != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("post-lexing lexeme sort", v, err)
		}
		if entries[0].word == "4096" {
			t.Fatal("cancellation did not reach sorting work")
		}
	})
	t.Run("position-sort", func(t *testing.T) {
		positions := make([]position, 4096)
		for i := range positions {
			positions[i].number = uint16(len(positions) - i)
		}
		ctx := &countingContext{Context: context.Background(), after: 10}
		got, err := uniquePositions(ctx, positions)
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("post-lexing position sort", got, err)
		}
		if positions[0].number == 4096 {
			t.Fatal("cancellation did not reach sorting work")
		}
	})
	t.Run("duplicate-scan", func(t *testing.T) {
		positions := make([]position, 4096)
		for i := range positions {
			positions[i].number = 1
		}
		positions[1].weight = 2
		// Measure only the sort's checks on this already ordered input, then
		// cancel after reduction has begun. No timing, goroutine or lexer
		// scheduling determines which canonicalization phase is exercised.
		sorting := &countingContext{Context: context.Background(), after: int(^uint(0) >> 1)}
		if err := stableSort(sorting, positions, comparePositions); err != nil {
			t.Fatal(err)
		}
		ctx := &countingContext{Context: context.Background(), after: sorting.calls + 3}
		got, err := uniquePositions(ctx, positions)
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("post-lexing duplicate reduction", got, err)
		}
		if positions[0].weight != 2 {
			t.Fatal("cancellation did not reach duplicate reduction")
		}
	})
	t.Run("merge", func(t *testing.T) {
		positions := make([]position, 4096)
		for i := range positions {
			positions[i].number = uint16((i+2048)%4096 + 1)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		err := stableSort(ctx, positions, func(a, b position) int {
			// This pair first occurs when merging the two ordered halves,
			// not in the initial order check or bounded library-sort runs.
			if a.number == 1 && b.number == 2049 {
				cancel()
			}
			return comparePositions(a, b)
		})
		if !errors.Is(err, context.Canceled) || positions[0].number != 1 {
			t.Fatal("post-lexing merge cancellation", err, positions[0])
		}
		if positions[256].number != 2305 {
			t.Fatal("merge continued beyond its cancellation-check interval")
		}
	})
}
