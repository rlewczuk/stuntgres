package core

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine/core/explain"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func TestAutoExplainPolicy(t *testing.T) {
	for _, tc := range []struct {
		elapsed time.Duration
		want    bool
	}{{999999, false}, {1000000, true}, {1000001, true}} {
		if got := autoExplainThreshold(tc.elapsed, time.Millisecond); got != tc.want {
			t.Fatal("inclusive unrounded threshold", tc, got)
		}
	}
	for _, rate := range []float64{0, 1} {
		if got := autoExplainSample(rate, func() float64 { t.Fatal("extreme sampled RNG"); return 0 }); got != (rate == 1) {
			t.Fatal(rate, got)
		}
	}
	for _, tc := range []struct {
		draw float64
		want bool
	}{{0, true}, {0.499999, true}, {0.5, false}} {
		if got := autoExplainSample(0.5, func() float64 { return tc.draw }); got != tc.want {
			t.Fatal("interior sample boundary", tc, got)
		}
	}
	var text *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Text {
			text = typ
			break
		}
	}
	if text == nil {
		t.Fatal("missing TEXT descriptor")
	}
	for _, tc := range []struct {
		input string
		limit int
		want  string
	}{{"éx", 1, "$1 = '...'"}, {"éx", 2, "$1 = 'é...'"}, {"éx", 3, "$1 = 'éx'"}, {"a'b", 2, "$1 = 'a''...'"}, {"", -1, "$1 = ''"}} {
		value := sqltype.String(text, tc.input)
		got, err := autoExplainParameters(context.Background(), []sqltype.Value{value}, tc.limit, sqltype.OutputEnvironment{})
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
}

// Pure terminal-state supplement. Composed tests below exercise real accepted
// executors, provider cleanup and portals; this retains the retired frame itself.
func TestAutoExplainFrameRelease(t *testing.T) {
	// Lexical restoration also serves scopes with no admitted operation.
	// An absent hook must not inspect operation-owned diagnostic storage.
	scope := &StatementScope{}
	scope.finishAutoExplain(false)
	scope.finishAutoExplain(true)
	if scope.autoExplain != nil || scope.autoExplainNoticeContexts != nil {
		t.Fatal("absent hook manufactured terminal ownership")
	}
	for _, success := range []bool{false, true} {
		_, observation := exec.Observe(context.Background(), false)
		f := &autoExplainFrame{options: explain.Options{Format: "json"}, source: "owned SQL", parameters: "owned current parameters", observation: observation, executed: false}
		op := &session.Operation{NoticeThreshold: session.LevelNotice}
		f.finish(op, success)
		f.finish(op, true)
		if !f.finished || f.observation != nil || f.renderers != nil || f.source != "" || f.parameters != "" || len(op.Notices) != 0 {
			t.Fatal("terminal owner retained graph or fabricated completion", f, op.Notices)
		}
	}
}
