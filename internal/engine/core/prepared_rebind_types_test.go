package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestPreparedRebindPublication(t *testing.T) {
	t.Run("TemporalCandidatePreservesLogicalCompounds", func(t *testing.T) {
		engine := rootengine.New()
		for _, query := range []string{"CREATE TYPE mood AS ENUM('q')", "CREATE TYPE mixed_capture AS(m mood,ts TIMESTAMP)"} {
			enginetest.Run(t, engine, query)
		}
		op, done, err := enginetest.Operation(engine, context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer done()
		ast := enginetest.Statement(t, "SELECT 'q'::mood,'(q,now)'::mixed_capture,'{now}'::timestamp[],TIMESTAMP 'now'")
		bind := func(input sqltype.InputEnvironment, expected *core.Description) analyze.Binding {
			t.Helper()
			engine.Mu.Lock()
			defer engine.Mu.Unlock()
			auth, err := engine.AuthorizationLocked(op)
			if err != nil {
				t.Fatal(err)
			}
			bound, err := core.Describe(op.Ctx, engine.AnalysisHost(op), ast, auth, ids.ObjectRef{}, input, expected, nil)
			if err != nil {
				t.Fatal(err)
			}
			return bound
		}
		first := bind(sqltype.InputEnvironment{TransactionStart: time.Date(2003, 7, 7, 4, 5, 6, 0, time.UTC)}, nil)
		if len(first.Prepared.Temporals) != 4 {
			t.Fatal("missing logical and genuine temporal captures", first.Prepared.Temporals)
		}
		description := core.Description{Parameters: first.Parameters, Results: first.Query.Root.Schema(), Temporals: first.Prepared.Temporals, CatalogOwner: &engine.Catalog.Owner, CatalogEpoch: engine.CatalogRead().Epoch()}
		frozen := append([]analyze.TemporalConstant(nil), first.Prepared.Temporals...)
		enginetest.Run(t, engine, "ALTER TYPE mood RENAME VALUE 'q' TO 'end'")
		description.RefreshTemporals = true
		fresh := bind(sqltype.InputEnvironment{TransactionStart: time.Date(2004, 8, 8, 12, 13, 14, 0, time.UTC)}, &description)
		if len(fresh.Prepared.Temporals) != len(frozen) {
			t.Fatal("candidate lost captures", fresh.Prepared.Temporals)
		}
		for i, old := range frozen {
			same := reflect.DeepEqual(old, fresh.Prepared.Temporals[i])
			if same == analyze.RefreshableTemporal(old) {
				t.Fatal("incorrect temporal/logical candidate boundary", i, old, fresh.Prepared.Temporals[i])
			}
		}
		if !reflect.DeepEqual(description.Temporals, frozen) {
			t.Fatal("candidate mutated original logical/temporal group")
		}
	})
	t.Run("ValuesRetainsInitialCaptures", func(t *testing.T) {
		engine := rootengine.New()
		session := enginetest.StartSession(t, engine, nil)
		ast, description := enginetest.PreparedDescription(t, engine, session, "VALUES(TIMESTAMP 'now',('{now}'::timestamp[])[1])")
		if len(description.Temporals) != 2 || description.Analysis == nil {
			t.Fatal("missing initial VALUES analysis", description.Temporals)
		}
		original := description
		for range 2 {
			result, err := enginetest.PreparedExecute(engine, session, ast, &description)
			if err != nil {
				t.Fatal(err)
			}
			result.Rows.Close()
			if description.Analysis != original.Analysis || len(description.Temporals) != 2 || &description.Temporals[0] != &original.Temporals[0] {
				t.Fatal("unchanged VALUES use replaced retained capture state")
			}
		}
	})
	e := rootengine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(v INT)", "CREATE SEQUENCE q"} {
		enginetest.SessionSQL(t, s, q)
	}
	ast, d := enginetest.PreparedDescription(t, e, s, "SELECT t.*,TIMESTAMP 'now','{now}'::timestamp[],nextval('q'),'integer'::regtype FROM t")
	if len(d.Temporals) < 2 || d.Sequences == nil || d.Regtypes == nil || d.Analysis == nil {
		t.Fatal("missing genuine capture groups")
	}
	before := d
	sequenceBefore, regtypeBefore := *d.Sequences, *d.Regtypes
	assertUnchanged := func() {
		t.Helper()
		if d.Analysis != before.Analysis || len(d.Temporals) != len(before.Temporals) || &d.Temporals[0] != &before.Temporals[0] || d.Sequences != before.Sequences || d.Regtypes != before.Regtypes || !reflect.DeepEqual(*d.Sequences, sequenceBefore) || !reflect.DeepEqual(*d.Regtypes, regtypeBefore) || !reflect.DeepEqual(d.Parameters, before.Parameters) || !reflect.DeepEqual(d.Results, before.Results) {
			t.Fatal("failed analysis published retained state")
		}
	}
	enginetest.SessionSQL(t, s, "ALTER TABLE t ADD other INT")
	result, err := enginetest.PreparedExecute(e, s, ast, &d)
	if result.Rows != nil {
		result.Rows.Close()
		t.Fatal("shape failure exposed rows")
	}
	var diagnostic interface{ SQLState() string }
	if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "0A000" {
		t.Fatal("missing fixed-shape error", err)
	}
	assertUnchanged()
	enginetest.SessionSQL(t, s, "DROP TABLE t")
	_, err = enginetest.PreparedExecute(e, s, ast, &d)
	if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "42P01" {
		t.Fatal("missing analysis error", err)
	}
	assertUnchanged()
	enginetest.SessionSQL(t, s, "CREATE TABLE t(v INT)")
	result, err = enginetest.PreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	result.Rows.Close()
	if d.Analysis == before.Analysis || len(d.Temporals) != len(before.Temporals) || &d.Temporals[0] == &before.Temporals[0] || d.Sequences != before.Sequences || d.Regtypes != before.Regtypes {
		t.Fatal("complete group did not publish together")
	}
	if err := core.CheckStatementSignature(before, d); err != nil {
		t.Fatal(err)
	}
	stable := d
	result, err = enginetest.PreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	result.Rows.Close()
	if d.Analysis != stable.Analysis || len(d.Temporals) != len(stable.Temporals) || &d.Temporals[0] != &stable.Temporals[0] || d.Sequences != stable.Sequences || d.Regtypes != stable.Regtypes {
		t.Fatal("valid use allocated retained state")
	}

	// Successful analysis publication precedes importing execution values.
	parameterAST, parameterDescription := enginetest.PreparedDescription(t, e, s, "SELECT $1::int,TIMESTAMP 'now' FROM t")
	old := parameterDescription.Analysis
	enginetest.SessionSQL(t, s, "ALTER TABLE t ADD unused INT")
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	scope.Expected = &parameterDescription
	importFailure := errors.New("unit importer failure")
	_, err = scope.Execute(parameterAST, 1, func(context.Context, analyze.ParameterInfo, []sqltype.Value, sqltype.InputEnvironment) error {
		return importFailure
	})
	err = scope.Finish(err)
	if !errors.Is(err, importFailure) || parameterDescription.Analysis == old {
		t.Fatal("analysis publication incorrectly depended on importer success", err)
	}
	t.Run("NativeProvenancePublishesNothing", func(t *testing.T) {
		engine := rootengine.New()
		session := enginetest.StartSession(t, engine, nil)
		for _, q := range []string{"CREATE TABLE source(v INT)", "CREATE SEQUENCE source_q"} {
			enginetest.SessionSQL(t, session, q)
		}
		ast, description := enginetest.PreparedDescription(t, engine, session, "SELECT TIMESTAMP 'now','{now}'::timestamp[],nextval('source_q'),'integer'::regtype FROM source")
		old := description
		sequence, regtype := *description.Sequences, *description.Regtypes
		if err := rootengine.Reset(context.Background(), engine); err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{"CREATE TABLE source(v INT)", "CREATE SEQUENCE source_q"} {
			enginetest.SessionSQL(t, session, q)
		}
		result, err := enginetest.PreparedExecute(engine, session, ast, &description)
		if result.Rows != nil {
			result.Rows.Close()
			t.Fatal("retired provenance exposed rows")
		}
		if !errors.Is(err, ids.ErrCatalogReplaced) || description.Analysis != old.Analysis || len(description.Temporals) != len(old.Temporals) || &description.Temporals[0] != &old.Temporals[0] || !reflect.DeepEqual(*description.Sequences, sequence) || !reflect.DeepEqual(*description.Regtypes, regtype) {
			t.Fatal("native lifetime rejection published capture/identity state", err)
		}
	})
}
