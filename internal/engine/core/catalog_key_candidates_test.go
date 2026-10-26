package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// catalogKeyDrain opens the top-level filter of q over the captured source,
// reports whether it took key candidates and returns its row count.
func catalogKeyDrain(t *testing.T, query *plan.Query, source *exec.Source, parameters []sqltype.Value) (keyed bool, rows int, err error) {
	t.Helper()
	stage := hashFilterStage(t, query, source, parameters)
	defer stage.Close()
	keyed = stage.HashCandidates != nil
	if keyed && stage.HashCandidates.Occurrences == nil {
		t.Fatal("key candidates must be a non-nil list")
	}
	if err = stage.Drain(context.Background()); err != nil {
		return keyed, 0, err
	}
	return keyed, stage.Count(), nil
}

func catalogKeyOID(t *testing.T, source *exec.Source, n uint32) sqltype.Value {
	t.Helper()
	v, err := sqltype.OIDValue(source.Anonymous[0][0].Type(), n)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCatalogKeyCandidatesProbesAndFallbacks(t *testing.T) {
	e := engine.New()
	for _, tc := range []struct {
		row   int // Probe the oid of this captured row; -1 probes 0.
		null  bool
		count int
	}{{7, false, 1}, {-1, false, 0}, {-1, true, 0}} {
		query, source, release := hashImplicitQuery(t, e, "SELECT proname FROM pg_proc WHERE oid=$1")
		if source.CatalogKeys == nil {
			t.Fatal("published pg_proc capture has no key index")
		}
		probe := catalogKeyOID(t, source, 0)
		if tc.row >= 0 {
			probe = source.Anonymous[tc.row][0]
		}
		if tc.null {
			probe = sqltype.Null(probe.Type())
		}
		keyed, rows, err := catalogKeyDrain(t, query, source, []sqltype.Value{probe})
		if !keyed || err != nil || rows != tc.count {
			t.Fatal("parameter probe", tc, keyed, rows, err)
		}
		release()
	}
	query, source, release := hashImplicitQuery(t, e, "SELECT proname FROM pg_proc WHERE pronamespace=$1")
	full, fullSource, scanRelease := hashImplicitQuery(t, e, "SELECT proname FROM pg_proc WHERE pronamespace::int8=11")
	keyed, rows, err := catalogKeyDrain(t, query, source, []sqltype.Value{catalogKeyOID(t, source, 11)})
	scanned, want, scanErr := catalogKeyDrain(t, full, fullSource, nil)
	if !keyed || scanned || err != nil || scanErr != nil || rows != want || rows < 100 {
		t.Fatal("non-unique oid column", keyed, rows, want, err, scanErr)
	}
	release()
	scanRelease()
	for _, tc := range []struct {
		q     string
		keyed bool
		count int
	}{
		{"SELECT typname FROM pg_type WHERE oid=23", true, 1},
		{"SELECT typname FROM pg_type WHERE oid='int4'::regtype::oid", true, 1},
		{"SELECT proname FROM pg_proc WHERE oid='upper(text)'::regprocedure::oid", true, 1},
		{"SELECT proname FROM pg_proc WHERE oid='upper(text)'::regprocedure::oid AND proname='upper'", false, 1},
		{"SELECT proname FROM pg_proc WHERE oid<>'upper(text)'::regprocedure::oid AND oid='upper(text)'::regprocedure::oid", false, 0},
		{"SELECT proname FROM pg_proc WHERE oid::int8='upper(text)'::regprocedure::oid::int8", false, 1},
	} {
		query, source, release := hashImplicitQuery(t, e, tc.q)
		keyed, rows, err := catalogKeyDrain(t, query, source, nil)
		if keyed != tc.keyed || err != nil || rows != tc.count {
			t.Fatal(tc.q, keyed, rows, err)
		}
		release()
	}
	// A wrongly typed probe keeps the full scan, which raises the comparison
	// error on its first row.
	query, source, release = hashImplicitQuery(t, e, "SELECT proname FROM pg_proc WHERE oid=$1")
	wrong, err := sqltype.Integer(enginetest.Usage(e, sqltype.Int4).Type, 16)
	if err != nil {
		t.Fatal(err)
	}
	if keyed, _, err := catalogKeyDrain(t, query, source, []sqltype.Value{wrong}); keyed || err == nil {
		t.Fatal("wrong probe type must keep the full scan and its error", keyed, err)
	}
	release()
}

func TestCatalogKeyCandidatesFollowPublishedCaptures(t *testing.T) {
	e := engine.New()
	_, first, release := hashImplicitQuery(t, e, "SELECT typname FROM pg_type WHERE oid=23")
	keys := first.CatalogKeys
	release()
	_, again, release := hashImplicitQuery(t, e, "SELECT typname FROM pg_type WHERE oid=25")
	if keys == nil || again.CatalogKeys != keys {
		t.Fatal("an unchanged catalog must share its capture's key index")
	}
	release()
	enginetest.Run(t, e, "CREATE DOMAIN ck_dom AS integer")
	query, after, release := hashImplicitQuery(t, e, "SELECT typname FROM pg_type WHERE oid='ck_dom'::regtype::oid")
	if after.CatalogKeys == nil || after.CatalogKeys == keys {
		t.Fatal("a new catalog state must index its own capture")
	}
	if keyed, rows, err := catalogKeyDrain(t, query, after, nil); !keyed || err != nil || rows != 1 {
		t.Fatal("new domain probe", keyed, rows, err)
	}
	release()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	enginetest.SessionSQL(t, s, "CREATE DOMAIN ck_private AS integer")
	op := enginetest.TransactionOperation(t, s)
	query, private, release := hashCaptureQuery(t, e, op, "SELECT typname FROM pg_type WHERE oid='ck_private'::regtype::oid")
	if private.CatalogKeys != nil {
		t.Fatal("a transaction-private capture must not be indexed")
	}
	if keyed, rows, err := catalogKeyDrain(t, query, private, nil); keyed || err != nil || rows != 1 {
		t.Fatal("private domain probe", keyed, rows, err)
	}
	release()
}
