package tablecmds_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func TestCheckRetainedTemporaryBuiltinReplacement(t *testing.T) {
	for _, mode := range []string{"ReplaceCatalog", "Restore"} {
		t.Run(mode, func(t *testing.T) {
			target := engine.New()
			session := enginetest.StartSession(t, target, nil)
			enginetest.SessionSQL(t, session, "CREATE TEMP SEQUENCE kept START 30")
			sequenceScalar(t, session, "SELECT nextval('kept')", 30)
			_, description := sequencePreparedDescription(t, target, session, "SELECT nextval('kept')")
			tempRef := description.Sequences.Constants[0].Ref
			temp := target.Catalog.State.Get(tempRef.Address)
			const script = "CREATE TABLE imported(n BIGINT DEFAULT CAST(1 AS BIGINT),b BOOL DEFAULT TRUE,CONSTRAINT c CHECK(n=CAST(1 AS BIGINT) AND b))"
			if mode == "Restore" {
				if err := engine.Restore(context.Background(), target, strings.NewReader(script)); err != nil {
					t.Fatal(err)
				}
			} else {
				source := engine.New()
				peer := enginetest.StartSession(t, source, nil)
				enginetest.SessionSQL(t, peer, script)
				ref := enginetest.CatalogRef(t, source, source.CatalogRead(), "imported")
				old := source.Catalog.State.Get(ref.Address)
				if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
					t.Fatal(err)
				}
				if source.Catalog.State.Get(ref.Address) != old {
					t.Fatal("replacement mutated source")
				}
			}
			view := target.CatalogRead()
			ref := enginetest.CatalogRef(t, target, view, "imported")
			descriptor := view.MustRelation(ref.Address)
			if err := table.ValidateChecks(view, descriptor, map[string]bool{}); err != nil {
				t.Fatal(err)
			}
			if target.Catalog.State.Get(tempRef.Address) != temp {
				t.Fatal("replacement changed pinned TEMP descriptor")
			}
			enginetest.SessionSQL(t, session, "INSERT INTO imported DEFAULT VALUES")
			sequenceScalar(t, session, "SELECT nextval('kept')", 31)
		})
	}
}

func TestCheckFailedRestorePreservesEpochAndStage(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE sentinel(a INT)")
	enginetest.SessionSQL(t, s, "INSERT INTO sentinel VALUES(7)")
	before := e.CatalogRead()
	ref := enginetest.CatalogRef(t, e, before, "sentinel")
	storage := e.Storage.Relations[ref.Address]
	stage := e.BeginCatalogStage(before)
	for _, script := range []string{"CREATE TABLE replacement(a INT CHECK(a))", "CREATE TABLE replacement(a INT CHECK(a=1));INSERT INTO replacement VALUES(2)"} {
		if err := engine.Restore(context.Background(), e, strings.NewReader(script)); err == nil {
			t.Fatal("bad Restore succeeded")
		}
		after := e.CatalogRead()
		if after.State() != before.State() || after.Epoch() != before.Epoch() || e.Storage.Relations[ref.Address] != storage {
			t.Fatal("failed Restore replaced destination")
		}
		if err := enginetest.ValidateObjectRef(e, after, ref); err != nil {
			t.Fatal("old reference invalidated", err)
		}
	}
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err != nil {
		t.Fatal("failed Restore invalidated prior stage", err)
	}
	e.DiscardCatalogStage(stage)
	enginetest.TransactionRows(t, s, "SELECT a FROM sentinel", 7)
}

func TestCheckAbsentValidatorHasNoAllocation(t *testing.T) {
	tbl := &catalog.Relation{}
	eval := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	if allocations := testing.AllocsPerRun(100, func() {
		constraint, err := table.ValidateCheckRow(tbl, nil, eval)
		if constraint != nil || err != nil {
			panic("unexpected validator failure")
		}
	}); allocations != 0 {
		t.Fatal("no-CHECK validator allocations", allocations)
	}
}

func sequencePreparedDescription(t *testing.T, e *core.Engine, s *session.Session, query string) (parser.Statement, core.StatementDescription) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	ast := enginetest.Statement(t, query)
	d, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal(err)
	}
	return ast, d
}
