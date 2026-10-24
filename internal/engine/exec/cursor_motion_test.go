package exec

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func cursorTestRows(t *testing.T, n int) []sqltype.Value {
	t.Helper()
	var int8 *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Int8 {
			int8 = d
		}
	}
	values := make([]sqltype.Value, n)
	for i := range values {
		v, err := sqltype.Integer(int8, int64(i+1))
		if err != nil {
			t.Fatal(err)
		}
		values[i] = v
	}
	return values
}

func cursorTestFetched(t *testing.T, rows *FinalRows) []int64 {
	t.Helper()
	if rows == nil {
		return nil
	}
	got := []int64{}
	for _, v := range rows.Values {
		n, err := v.Integer()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, n)
	}
	return got
}

// A promoted (HOLD) cursor moves over its store without a producer. Counts and
// places follow PostgreSQL FETCH/MOVE semantics over rows 1, 2 and 3.
func TestCursorStoreMotion(t *testing.T) {
	in := CursorInput{Store: cursorTestRows(t, 3), Count: 3, Width: 1, Scroll: true}
	var p CursorPosition
	next := &parser.FetchCursor{Direction: parser.CursorForward, Count: 1}
	prior := &parser.FetchCursor{Direction: parser.CursorBackward, Count: 1}
	for _, step := range []struct {
		name  string
		fetch *parser.FetchCursor
		count int64
		rows  []int64
		place CursorPlace
	}{
		{"NEXT", next, 1, []int64{1}, CursorOn},
		{"ALL", &parser.FetchCursor{Direction: parser.CursorForward, All: true}, 2, []int64{2, 3}, CursorAfter},
		{"PRIOR from end", prior, 1, []int64{3}, CursorOn},
		{"ABSOLUTE -1", &parser.FetchCursor{Direction: parser.CursorAbsolute, Count: -1}, 1, []int64{3}, CursorOn},
		{"ABSOLUTE 2", &parser.FetchCursor{Direction: parser.CursorAbsolute, Count: 2}, 1, []int64{2}, CursorOn},
		{"RELATIVE -1", &parser.FetchCursor{Direction: parser.CursorRelative, Count: -1}, 1, []int64{1}, CursorOn},
		{"FETCH 0", &parser.FetchCursor{Direction: parser.CursorForward}, 1, []int64{1}, CursorOn},
		{"MOVE BACKWARD ALL", &parser.FetchCursor{Direction: parser.CursorBackward, All: true, Move: true}, 0, nil, CursorBefore},
		{"PRIOR from start", prior, 0, []int64{}, CursorBefore},
		{"ABSOLUTE 4", &parser.FetchCursor{Direction: parser.CursorAbsolute, Count: 4}, 0, []int64{}, CursorAfter},
		{"ABSOLUTE 0", &parser.FetchCursor{Direction: parser.CursorAbsolute}, 0, []int64{}, CursorBefore},
		{"MOVE ALL", &parser.FetchCursor{Direction: parser.CursorForward, All: true, Move: true}, 3, nil, CursorAfter},
	} {
		var rows *FinalRows
		if !step.fetch.Move {
			rows = NewFinalRows(nil, 0)
		}
		count, err := p.Move(step.fetch, rows, in)
		if got := cursorTestFetched(t, rows); err != nil || count != step.count || !slices.Equal(got, step.rows) || p.Place != step.place || p.CurrentSource != nil {
			t.Fatal(step.name, count, got, p, err)
		}
	}
}

type cursorTestProducer struct {
	source           *Source
	stage            *Stage
	opens, exhausted int
}

func (p *cursorTestProducer) Context() context.Context { return context.Background() }
func (p *cursorTestProducer) Open() (*Stage, error) {
	if p.stage == nil {
		p.opens++
		p.stage = &Stage{Source: p.source}
	}
	return p.stage, nil
}
func (p *cursorTestProducer) Exhausted() error {
	p.exhausted++
	return nil
}

// A forward-only live cursor opens its producer once, reports exhaustion once
// after closing the stage, and rejects backward motion.
func TestCursorForwardProducerMotion(t *testing.T) {
	producer := &cursorTestProducer{source: &Source{Materialized: true, Flat: cursorTestRows(t, 2), FlatWidth: 1, FlatCount: 2}}
	in := CursorInput{Producer: producer, Width: 1}
	var p CursorPosition
	rows := NewFinalRows(nil, 0)
	count, err := p.Move(&parser.FetchCursor{Direction: parser.CursorForward, Count: 1}, rows, in)
	if err != nil || count != 1 || p.Place != CursorOn || p.CurrentSource != producer.source || p.CurrentIndex != 0 || producer.opens != 1 || producer.exhausted != 0 {
		t.Fatal("first row", count, p, producer, err)
	}
	in.Stage = producer.stage
	rows = NewFinalRows(nil, 0)
	count, err = p.Move(&parser.FetchCursor{Direction: parser.CursorForward, All: true}, rows, in)
	if got := cursorTestFetched(t, rows); err != nil || count != 1 || !slices.Equal(got, []int64{2}) || p.Place != CursorAfter || !p.AtEnd || p.CurrentSource != nil || producer.opens != 1 || producer.exhausted != 1 || !producer.stage.Closed {
		t.Fatal("exhaustion", count, got, p, producer, err)
	}
	count, err = p.Move(&parser.FetchCursor{Direction: parser.CursorForward, All: true, Move: true}, nil, in)
	if err != nil || count != 0 || producer.exhausted != 1 {
		t.Fatal("motion after the end", count, producer, err)
	}
	var diagnostic *sqlstate.Error
	_, err = p.Move(&parser.FetchCursor{Direction: parser.CursorBackward, Count: 1}, nil, in)
	if !errors.As(err, &diagnostic) || diagnostic.Code != "55000" || diagnostic.Message != "cursor can only scan forward" || diagnostic.Hint != "Declare it with SCROLL option to enable backward scan." {
		t.Fatal("backward motion of a forward-only cursor", err)
	}
	if err := p.Rewind(in); !errors.As(err, &diagnostic) || diagnostic.Code != "55000" {
		t.Fatal("rewind of a forward-only cursor", err)
	}
}

func TestCursorRetainedProducerReset(t *testing.T) {
	source := &Source{Singleton: true}
	child := Stage{Source: source, Selection: []int{}, Done: true, Position: 1}
	resets := 0
	child.Reset = func(ctx context.Context, s *Stage) (bool, error) {
		resets++
		s.Selection = s.Selection[:0]
		s.Done = false
		return true, nil
	}
	out := ProducingStage(&child, nil)
	out.Selection = append(out.Selection, 0)
	out.Done = true
	out.Position = 1
	if changed, err := out.Rewind(context.Background()); err != nil || !changed || resets != 1 || child.Position != 0 || out.Position != 0 || out.Done || len(out.Selection) != 0 {
		t.Fatal("producer demand was not reset", out, child, resets)
	}
	// A materialized ORDER boundary replaces reset with position-only rewind.
	out.Reset = nil
	out.Selection = append(out.Selection, 0)
	out.Done = true
	if changed, err := out.Rewind(context.Background()); err != nil || changed || resets != 1 || !out.Done || len(out.Selection) != 1 {
		t.Fatal("cached boundary invalidates child", out, resets)
	}
	t.Run("cached_parent_frontier", func(t *testing.T) {
		source := &Source{Materialized: true, FlatCount: 3}
		child := Stage{Source: source, Position: 1}
		out := ProducingStage(&child, nil)
		out.Selection = append(out.Selection, 0)
		out.Position = 1
		out.Produce = func(ctx context.Context, out *Stage) (bool, error) {
			i, present, err := child.NextOccurrence(ctx)
			if present && err == nil {
				RetainOccurrence(out, &child, i)
			}
			return present, err
		}
		if changed, err := out.Rewind(context.Background()); err != nil || changed || child.Position != 1 || out.Position != 0 || len(out.Selection) != 1 {
			t.Fatal("cached rewind loses child demand frontier", out, child)
		}
		if err := out.Drain(context.Background()); err != nil {
			t.Fatal(err)
		}
		if out.Count() != 3 || out.Index(0) != 0 || out.Index(1) != 1 || out.Index(2) != 2 || child.Position != 3 {
			t.Fatal("cached rewind appends captured prefix again", out, child)
		}
	})
}
