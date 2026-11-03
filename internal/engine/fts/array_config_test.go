package fts

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func arrayConfigBuild(ctx context.Context, words []string) (*Vector, error) {
	return ArrayVector(ctx, len(words), func(i int) (string, bool) { return words[i], true })
}

func TestFTSArrayConfigVectorConstruction(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		words []string
		want  string
	}{
		{[]string{"b", "a", "a"}, "'a' 'b'"},
		{[]string{}, ""},
		{[]string{"The", "rats", "cat:1A", "space word"}, "'The' 'cat:1A' 'rats' 'space word'"},
		{[]string{"é", "a", "Z", "a"}, "'Z' 'a' 'é'"},
		{[]string{"a'b", `x\y`}, `'a''b' 'x\\y'`},
	} {
		before := append([]string{}, tc.words...)
		out, err := arrayConfigBuild(ctx, tc.words)
		if err != nil || !out.Valid() || out.String() != tc.want {
			t.Fatal(tc.words, out, err)
		}
		for _, entry := range out.lexemes {
			if entry.positions != nil {
				t.Fatal("invented position or weight", entry)
			}
		}
		if !reflect.DeepEqual(tc.words, before) {
			t.Fatal("mutated input")
		}
		if len(tc.words) != 0 {
			tc.words[0] = "replacement"
			if out.String() != tc.want || out.lexemes[0].word == "replacement" {
				t.Fatal("retained mutable input backing")
			}
		}
	}
	for _, word := range []string{strings.Repeat("x", 2046), strings.Repeat("é", 1023)} {
		out, err := arrayConfigBuild(ctx, []string{word})
		if err != nil || out.LexemeCount() != 1 || out.LexemeAt(0) != word {
			t.Fatal("word byte boundary", err)
		}
	}
	for _, word := range []string{strings.Repeat("x", 2047), strings.Repeat("é", 1024)} {
		out, err := arrayConfigBuild(ctx, []string{word})
		arrayConfigFailure(t, out, err, "54000", fmt.Sprintf("word is too long (%d bytes, max 2046 bytes)", len(word)))
	}
	words := make([]string, 513)
	for i := range 512 {
		words[i] = fmt.Sprintf("%04d", i) + strings.Repeat("x", 2042)
	}
	words[512] = "0512" + strings.Repeat("y", 1019)
	for _, input := range [][]string{words, append(append([]string{}, words...), words[0])} {
		out, err := arrayConfigBuild(ctx, input)
		if err != nil || out.LexemeCount() != 513 {
			t.Fatal("deduplicated total boundary", err)
		}
		for i, word := range words {
			if out.LexemeAt(i) != word || len(out.lexemes[i].positions) != 0 {
				t.Fatal("boundary payload", i)
			}
		}
	}
	words[512] += "y"
	out, err := arrayConfigBuild(ctx, words)
	arrayConfigFailure(t, out, err, "54000", "string is too long for tsvector (1048576 bytes, max 1048575 bytes)")
	// Even a late invalid element wins over the final combined-size failure.
	out, err = arrayConfigBuild(ctx, append(words, ""))
	arrayConfigFailure(t, out, err, "2200F", "lexeme array may not contain empty strings")
	long := strings.Repeat("x", 2047)
	for _, order := range [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
		out, err := ArrayVector(ctx, 3, func(i int) (string, bool) {
			switch order[i] {
			case 0:
				return "", true
			case 1:
				return "", false
			default:
				return long, true
			}
		})
		code, message := "2200F", "lexeme array may not contain empty strings"
		if order[0] == 1 {
			code, message = "22004", "lexeme array may not contain nulls"
		}
		if order[0] == 2 {
			code, message = "54000", "word is too long (2047 bytes, max 2046 bytes)"
		}
		arrayConfigFailure(t, out, err, code, message)
	}
}

func arrayConfigFailure(t *testing.T, out *Vector, err error, code, message string) {
	t.Helper()
	var diagnostic *sqlstate.Error
	if out != nil || !errors.As(err, &diagnostic) || diagnostic.Code != code || diagnostic.Message != message {
		t.Fatal("constructor failure", out, err)
	}
	if marked, ok := err.(*sqlstate.InputError); ok && marked.Recoverable() {
		t.Fatal("function failure became a soft-input cast failure", err)
	}
}

type arrayConfigPhaseContext struct {
	context.Context
	cancel    context.CancelFunc
	phase     string
	remaining int
	entered   bool
}

func (c *arrayConfigPhaseContext) Err() error {
	var pcs [24]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if strings.HasPrefix(frame.Function, "stuntgres/internal/engine/fts.") {
			if strings.Contains(frame.Function, c.phase) {
				c.remaining--
				if c.remaining == 0 {
					c.entered = true
					c.cancel()
				}
			}
			break
		}
		if !more {
			break
		}
	}
	return c.Context.Err()
}
func TestFTSArrayConfigVectorCancellation(t *testing.T) {
	words := make([]string, 2048)
	for i := range words {
		words[i] = fmt.Sprintf("w%04d", len(words)-i)
	}
	before := append([]string{}, words...)
	for _, phase := range []string{"ArrayVector", "stableMergeSort", "canonicalVector", "renderVector"} {
		ctx, cancel := context.WithCancel(context.Background())
		c := &arrayConfigPhaseContext{Context: ctx, cancel: cancel, phase: phase, remaining: 4}
		out, err := arrayConfigBuild(c, words)
		cancel()
		if !c.entered || out != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("interior cancellation", phase, c.entered, out, err)
		}
		if !reflect.DeepEqual(before, words) {
			t.Fatal("cancellation mutated source", phase)
		}
	}
	out, err := arrayConfigBuild(context.Background(), words)
	if err != nil || out.LexemeCount() != len(words) {
		t.Fatal("recovery", err)
	}
}
