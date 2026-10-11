package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
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
	p := fixture.NewScenario(t)
	run := func(s fixture.Step) {
		s.CheckMetadata = likeReferenceMetadata
		p.Step(t, s)
	}
	both := func(s fixture.Step) {
		run(s)
		run(fixture.Prepared(s))
	}
	run(fixture.Exec(`CREATE TABLE t(id INTEGER,v TEXT,p TEXT,e TEXT,c CHAR(3),b BYTEA,cc TEXT COLLATE "C",pp TEXT COLLATE "POSIX")`, 0))
	run(fixture.Exec("INSERT INTO t VALUES(1,'AbC','A%','!','a','\\xc3a9','a','a'),(2,'É','é','!','a','\\xc3a9','a','a'),(3,NULL,NULL,NULL,NULL,NULL,NULL,NULL)", 3))
	both(fixture.Select("SELECT v LIKE p,v NOT LIKE p,v ILIKE p,v NOT ILIKE p,v ~~ p,v !~~ p,v ~~* p,v !~~* p,v OPERATOR(pg_catalog.~~) p FROM t ORDER BY id",
		[]string{"?column?", "?column?", "?column?", "?column?", "?column?", "?column?", "?column?", "?column?", "?column?"},
		[]string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"},
		[][]any{{true, false, true, false, true, false, true, false, true}, {false, true, false, true, false, true, false, true, false}, {nil, nil, nil, nil, nil, nil, nil, nil, nil}}))
	both(fixture.Select("SELECT 'x' LIKE '%__\\','' LIKE '%_\\',CAST('\\x78' AS BYTEA) LIKE CAST('\\x255f5f5c' AS BYTEA) FROM t WHERE id=1",
		[]string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, false, false}}))
	for _, q := range []string{"SELECT 'x' LIKE '%_\\' FROM t WHERE id=1", "SELECT CAST('\\x78' AS BYTEA) LIKE CAST('\\x255f5c' AS BYTEA) FROM t WHERE id=1"} {
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22025", Message: "LIKE pattern must not end with escape character"})
		s.SelectRows, s.ErrorStage = true, "execute"
		both(s)
	}
	for _, backend := range p.Backends() {
		q := "SELECT v ILIKE $1 ESCAPE $2 FROM t WHERE id=1"
		stmt, err := backend.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{backend.DB, stmt, q}
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."}, "a%", "ab")
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
		for _, tc := range []struct {
			escape any
			want   any
		}{{"!", true}, {nil, nil}, {"!", true}} {
			s := fixture.Select(q, []string{"?column?"}, []string{"BOOL"}, [][]any{{tc.want}}, "a%", tc.escape)
			s.CheckMetadata = likeReferenceMetadata
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
		}
	}
	for _, q := range []string{
		"SELECT cc LIKE pp FROM t WHERE id=1",
		"SELECT cc LIKE CAST(pp AS TEXT) FROM t WHERE id=1",
		"SELECT cc LIKE COALESCE('a'::text,pp) FROM t WHERE id=1",
		"SELECT 'a' LIKE COALESCE(pg_catalog.like_escape(cc,pp),'a') FROM t WHERE id=1",
		"SELECT cc LIKE btrim(pp) FROM t WHERE id=1",
	} {
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."})
		s.SelectRows, s.ErrorStage = true, "execute"
		both(s)
	}
	for _, tc := range []struct {
		q    string
		want fixture.Diagnostic
	}{
		{`SELECT cc COLLATE "C" LIKE pp COLLATE "POSIX" FROM t`, fixture.Diagnostic{Code: "42P21", Message: `collation mismatch between explicit collations "C" and "POSIX"`, Position: "31"}},
		{"SELECT id LIKE 'x' FROM t", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer ~~ unknown", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "11"}},
	} {
		s := fixture.ImplicitError(tc.q, tc.want)
		s.SelectRows, s.ErrorStage = true, "execute"
		run(s)
		s.ErrorStage = "prepare"
		run(fixture.Prepared(s))
	}
	run(fixture.Exec("CREATE TABLE guarded(v TEXT CONSTRAINT starts_a CHECK(v LIKE 'a%' ESCAPE '!'))", 0))
	run(fixture.Exec("INSERT INTO guarded VALUES('a'),(NULL)", 2))
	run(fixture.Exec("UPDATE guarded SET v='ab' WHERE v LIKE 'a%' ESCAPE '!'", 1))
	both(fixture.Select("SELECT v LIKE 'a%' FROM guarded WHERE v LIKE 'a%'", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}}))
	both(fixture.Select("SELECT COUNT(*) FROM guarded", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
	run(fixture.Exec("CREATE VIEW matched AS SELECT id,v ILIKE p ESCAPE e AS result FROM t", 0))
	run(fixture.Exec("CREATE VIEW wrapped AS SELECT id,cc LIKE CAST(pp AS TEXT) AS result FROM t", 0))
	run(fixture.Exec(`CREATE VIEW annotated_match AS SELECT id,cc LIKE btrim(pp COLLATE "C") AS result FROM t`, 0))
	// View admission must fail at Execute, not Prepare, and expose no result.
	invalid := fixture.ImplicitError("CREATE VIEW invalid_pattern AS SELECT COALESCE(pg_catalog.like_escape(cc,pp),'a') AS pattern FROM t", fixture.Diagnostic{Code: "42P22", Message: `could not determine which collation to use for view column "pattern"`, Hint: "Use the COLLATE clause to set the collation explicitly."})
	invalid.ErrorStage = "execute"
	run(fixture.Prepared(invalid))
	matched := fixture.Select("SELECT result FROM matched ORDER BY id", []string{"result"}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}})
	annotated := fixture.Select("SELECT result FROM annotated_match ORDER BY id", []string{"result"}, []string{"BOOL"}, [][]any{{true}, {true}, {nil}})
	explicit := fixture.Select(`SELECT cc COLLATE "default" LIKE pp,cc LIKE COALESCE(pp,cc) COLLATE "C",cc LIKE btrim(pp COLLATE "C") FROM t ORDER BY id`, []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}, {true, true, true}, {nil, nil, nil}})
	both(matched)
	both(annotated)
	both(explicit)
	stmt, err := p.Native.PrepareContext(p.Ctx, explicit.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if t.Failed() {
		t.FailNow()
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	// Native controls have no PostgreSQL API. Their SQL witnesses use the same
	// independently expected body; real mode also replays the actual native SQL.
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP VIEW matched,wrapped,annotated_match; DROP TABLE guarded,t"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
	}
	both(matched)
	both(annotated)
	explicit.CheckMetadata = likeReferenceMetadata
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{p.Native, stmt, explicit.Query}, p.MemoryRecord, explicit)
	s := fixture.ImplicitError("SELECT result FROM wrapped WHERE id=1", fixture.Diagnostic{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."})
	s.SelectRows, s.ErrorStage = true, "execute"
	both(s)
	both(fixture.Select("SELECT c,b FROM t WHERE id=1", []string{"c", "b"}, []string{"BPCHAR", "BYTEA"}, [][]any{{"a  ", "\xc3\xa9"}}))
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("unstable LIKE legacy redump", err)
	}
}

func TestReferenceLikeLegacyStageConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INTEGER,v TEXT,p TEXT,e TEXT,CHECK(v LIKE 'a%' ESCAPE '!'))", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,'ab','A%','!'),(2,NULL,NULL,NULL)", 2))
	for _, s := range []fixture.Step{
		fixture.Select("SELECT v ILIKE p ESCAPE e FROM t ORDER BY id", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}, {nil}}),
		fixture.Select("SELECT CASE WHEN v LIKE 'a%' THEN TRUE ELSE FALSE END FROM t ORDER BY id", []string{"case"}, []string{"BOOL"}, [][]any{{true}, {false}}),
		fixture.Select("SELECT COUNT(*) FROM t WHERE v ILIKE p ESCAPE e", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}),
		fixture.Select("SELECT v LIKE 'a%',COUNT(*) FROM t GROUP BY v LIKE 'a%' ORDER BY v LIKE 'a%'", []string{"?column?", "count"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(1)}, {nil, int64(1)}}),
	} {
		s.CheckMetadata = likeReferenceMetadata
		p.Step(t, s)
		p.Step(t, fixture.Prepared(s))
	}
}

func TestReferenceLikeLegacyMissingNamespace(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(v TEXT)", 0))
	s := fixture.ImplicitError("SELECT v OPERATOR(missing_like.~~) 'a%' FROM t", fixture.Diagnostic{Code: "3F000", Message: `schema "missing_like" does not exist`, Position: "10"})
	s.SelectRows, s.ErrorStage = true, "execute"
	p.Step(t, s)
	s.ErrorStage = "prepare"
	p.Step(t, fixture.Prepared(s))
}
