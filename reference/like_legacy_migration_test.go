package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
)

// This migration preserves the public assertion union of TestLikeSmoke and
// TestLikeStoredAndStageConsumers. Exact existing paths are not repeated:
// TestReferenceLikeMatching owns BPCHAR padding/BYTEA octets and custom ESCAPE;
// TestReferenceLikeCheckAtomicity owns the failing multirow INSERT and unchanged
// guarded rows; TestReferenceLikeWrapperDemand owns invalid-pattern view
// admission; TestReferenceLikeIntegratedExplicitCollation owns btrim and explicit
// conflict binding. The scenarios below preserve the missing combined routes,
// successful CHECK UPDATE, wrappers, held ILIKE recovery and replay lifecycle.
// Private Engine.SetObjectACL witnesses stay internal: native SQL GRANT has no
// public implementation. TestReferenceLikeOracleSecurity independently owns the
// PostgreSQL column-grant and denied-schema public contracts.
func TestReferenceLikeLegacyMigration(t *testing.T) {
	p := newReferenceScenario(t)
	run := func(s referenceStep) {
		s.checkMetadata = likeReferenceMetadata
		p.step(t, s)
	}
	both := func(s referenceStep) {
		run(s)
		run(referencePrepared(s))
	}
	run(referenceExec(`CREATE TABLE t(id INTEGER,v TEXT,p TEXT,e TEXT,c CHAR(3),b BYTEA,cc TEXT COLLATE "C",pp TEXT COLLATE "POSIX")`, 0))
	run(referenceExec("INSERT INTO t VALUES(1,'AbC','A%','!','a','\\xc3a9','a','a'),(2,'É','é','!','a','\\xc3a9','a','a'),(3,NULL,NULL,NULL,NULL,NULL,NULL,NULL)", 3))
	both(referenceSelect("SELECT v LIKE p,v NOT LIKE p,v ILIKE p,v NOT ILIKE p,v ~~ p,v !~~ p,v ~~* p,v !~~* p,v OPERATOR(pg_catalog.~~) p FROM t ORDER BY id",
		[]string{"?column?", "?column?", "?column?", "?column?", "?column?", "?column?", "?column?", "?column?", "?column?"},
		[]string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"},
		[][]any{{true, false, true, false, true, false, true, false, true}, {false, true, false, true, false, true, false, true, false}, {nil, nil, nil, nil, nil, nil, nil, nil, nil}}))
	both(referenceSelect("SELECT 'x' LIKE '%__\\','' LIKE '%_\\',CAST('\\x78' AS BYTEA) LIKE CAST('\\x255f5f5c' AS BYTEA) FROM t WHERE id=1",
		[]string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, false, false}}))
	for _, q := range []string{"SELECT 'x' LIKE '%_\\' FROM t WHERE id=1", "SELECT CAST('\\x78' AS BYTEA) LIKE CAST('\\x255f5c' AS BYTEA) FROM t WHERE id=1"} {
		s := referenceImplicitError(q, referenceDiagnostic{Code: "22025", Message: "LIKE pattern must not end with escape character"})
		s.selectRows, s.errorStage = true, "execute"
		both(s)
	}
	for _, backend := range p.backends() {
		q := "SELECT v ILIKE $1 ESCAPE $2 FROM t WHERE id=1"
		stmt, err := backend.db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{backend.db, stmt, q}
		s := referenceImplicitError(q, referenceDiagnostic{Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."}, "a%", "ab")
		s.selectRows, s.errorStage = true, "execute"
		referenceRunStep(t, p.ctx, held, backend.recorder, s)
		for _, tc := range []struct {
			escape any
			want   any
		}{{"!", true}, {nil, nil}, {"!", true}} {
			s := referenceSelect(q, []string{"?column?"}, []string{"BOOL"}, [][]any{{tc.want}}, "a%", tc.escape)
			s.checkMetadata = likeReferenceMetadata
			referenceRunStep(t, p.ctx, held, backend.recorder, s)
		}
	}
	for _, q := range []string{
		"SELECT cc LIKE pp FROM t WHERE id=1",
		"SELECT cc LIKE CAST(pp AS TEXT) FROM t WHERE id=1",
		"SELECT cc LIKE COALESCE('a'::text,pp) FROM t WHERE id=1",
		"SELECT 'a' LIKE COALESCE(pg_catalog.like_escape(cc,pp),'a') FROM t WHERE id=1",
		"SELECT cc LIKE btrim(pp) FROM t WHERE id=1",
	} {
		s := referenceImplicitError(q, referenceDiagnostic{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."})
		s.selectRows, s.errorStage = true, "execute"
		both(s)
	}
	for _, tc := range []struct {
		q    string
		want referenceDiagnostic
	}{
		{`SELECT cc COLLATE "C" LIKE pp COLLATE "POSIX" FROM t`, referenceDiagnostic{Code: "42P21", Message: `collation mismatch between explicit collations "C" and "POSIX"`, Position: "31"}},
		{"SELECT id LIKE 'x' FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer ~~ unknown", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "11"}},
	} {
		s := referenceImplicitError(tc.q, tc.want)
		s.selectRows, s.errorStage = true, "execute"
		run(s)
		s.errorStage = "prepare"
		run(referencePrepared(s))
	}
	run(referenceExec("CREATE TABLE guarded(v TEXT CONSTRAINT starts_a CHECK(v LIKE 'a%' ESCAPE '!'))", 0))
	run(referenceExec("INSERT INTO guarded VALUES('a'),(NULL)", 2))
	run(referenceExec("UPDATE guarded SET v='ab' WHERE v LIKE 'a%' ESCAPE '!'", 1))
	both(referenceSelect("SELECT v LIKE 'a%' FROM guarded WHERE v LIKE 'a%'", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}}))
	both(referenceSelect("SELECT COUNT(*) FROM guarded", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
	run(referenceExec("CREATE VIEW matched AS SELECT id,v ILIKE p ESCAPE e AS result FROM t", 0))
	run(referenceExec("CREATE VIEW wrapped AS SELECT id,cc LIKE CAST(pp AS TEXT) AS result FROM t", 0))
	run(referenceExec(`CREATE VIEW annotated_match AS SELECT id,cc LIKE btrim(pp COLLATE "C") AS result FROM t`, 0))
	// View admission must fail at Execute, not Prepare, and expose no result.
	invalid := referenceImplicitError("CREATE VIEW invalid_pattern AS SELECT COALESCE(pg_catalog.like_escape(cc,pp),'a') AS pattern FROM t", referenceDiagnostic{Code: "42P22", Message: `could not determine which collation to use for view column "pattern"`, Hint: "Use the COLLATE clause to set the collation explicitly."})
	invalid.errorStage = "execute"
	run(referencePrepared(invalid))
	matched := referenceSelect("SELECT result FROM matched ORDER BY id", []string{"result"}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}})
	annotated := referenceSelect("SELECT result FROM annotated_match ORDER BY id", []string{"result"}, []string{"BOOL"}, [][]any{{true}, {true}, {nil}})
	explicit := referenceSelect(`SELECT cc COLLATE "default" LIKE pp,cc LIKE COALESCE(pp,cc) COLLATE "C",cc LIKE btrim(pp COLLATE "C") FROM t ORDER BY id`, []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}, {true, true, true}, {nil, nil, nil}})
	both(matched)
	both(annotated)
	both(explicit)
	stmt, err := p.native.PrepareContext(p.ctx, explicit.query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if t.Failed() {
		t.FailNow()
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	// Native controls have no PostgreSQL API. Their SQL witnesses use the same
	// independently expected body; real mode also replays the actual native SQL.
	if p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, "DROP VIEW matched,wrapped,annotated_match; DROP TABLE guarded,t"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
	}
	both(matched)
	both(annotated)
	explicit.checkMetadata = likeReferenceMetadata
	referenceRunStep(t, p.ctx, referenceHeldStatement{p.native, stmt, explicit.query}, p.memoryRecord, explicit)
	s := referenceImplicitError("SELECT result FROM wrapped WHERE id=1", referenceDiagnostic{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."})
	s.selectRows, s.errorStage = true, "execute"
	both(s)
	both(referenceSelect("SELECT c,b FROM t WHERE id=1", []string{"c", "b"}, []string{"BPCHAR", "BYTEA"}, [][]any{{"a  ", "\xc3\xa9"}}))
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("unstable LIKE legacy redump", err)
	}
}

func TestReferenceLikeLegacyStageConsumers(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INTEGER,v TEXT,p TEXT,e TEXT,CHECK(v LIKE 'a%' ESCAPE '!'))", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,'ab','A%','!'),(2,NULL,NULL,NULL)", 2))
	for _, s := range []referenceStep{
		referenceSelect("SELECT v ILIKE p ESCAPE e FROM t ORDER BY id", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}, {nil}}),
		referenceSelect("SELECT CASE WHEN v LIKE 'a%' THEN TRUE ELSE FALSE END FROM t ORDER BY id", []string{"case"}, []string{"BOOL"}, [][]any{{true}, {false}}),
		referenceSelect("SELECT COUNT(*) FROM t WHERE v ILIKE p ESCAPE e", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}),
		referenceSelect("SELECT v LIKE 'a%',COUNT(*) FROM t GROUP BY v LIKE 'a%' ORDER BY v LIKE 'a%'", []string{"?column?", "count"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(1)}, {nil, int64(1)}}),
	} {
		s.checkMetadata = likeReferenceMetadata
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
}

func TestReferenceLikeLegacyMissingNamespace(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(v TEXT)", 0))
	s := referenceImplicitError("SELECT v OPERATOR(missing_like.~~) 'a%' FROM t", referenceDiagnostic{Code: "3F000", Message: `schema "missing_like" does not exist`, Position: "10"})
	s.selectRows, s.errorStage = true, "execute"
	p.step(t, s)
	s.errorStage = "prepare"
	p.step(t, referencePrepared(s))
}
