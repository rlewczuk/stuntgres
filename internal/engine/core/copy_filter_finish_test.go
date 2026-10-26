package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
)

func TestCopyFilterFinishVeto(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		filter      bool
	}{{"accepted", "1\nbad\n", false}, {"all_rejected", "bad\nworse\n", false}, {"all_filtered", "1\n2\n", true}} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, op, scope := copyUnitStart(t, false)
			defer op.Done()
			syntax := copyUnitSyntax()
			syntax.Options = []parser.CopyOption{{Name: "on_error", Value: "ignore", HasValue: true, Argument: parser.CopyArgumentScalar}}
			if tc.filter {
				syntax = enginetest.Statement(t, "COPY t(v) FROM STDIN WITH(ON_ERROR ignore) WHERE FALSE").(*parser.Copy)
			}
			veto := errors.New("COPY filter terminal veto")
			count, err := core.ScopeCopyProtocol(scope, syntax, strings.NewReader(tc.input), nil, core.CopyProtocolHooks{FinishInput: func() error { return veto }})
			if count != 0 || !errors.Is(err, veto) {
				t.Fatal(count, err)
			}
			if got := op.DetachNotices(); len(got) != 0 {
				t.Fatal("failure summary", got)
			}
			if err := scope.Finish(err); !errors.Is(err, veto) {
				t.Fatal("terminal identity", err)
			}
			if got := op.DetachNotices(); len(got) != 0 {
				t.Fatal("final failure summary", got)
			}
			if scope.Stage.Rows != nil || scope.Owner.CopyLease != nil {
				t.Fatal("veto leaked private rows or reservation")
			}
		})
	}
	t.Run("final_publication_cancel", func(t *testing.T) {
		_, _, op, scope := copyUnitStart(t, false)
		defer op.Done()
		syntax := copyUnitSyntax()
		syntax.Options = []parser.CopyOption{{Name: "on_error", Value: "ignore", HasValue: true, Argument: parser.CopyArgumentScalar}}
		count, err := scope.Copy(syntax, strings.NewReader("1\nbad\n"), nil)
		if count != 1 || err != nil {
			t.Fatal(count, err)
		}
		if len(op.DetachNotices()) != 0 {
			t.Fatal("summary before final success")
		}
		op.Cancel(context.Canceled)
		if err := scope.Finish(nil); err == nil {
			t.Fatal("canceled publication succeeded")
		}
		if len(op.DetachNotices()) != 0 {
			t.Fatal("summary after canceled publication")
		}
	})
}
