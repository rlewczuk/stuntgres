package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func soundexInventory(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	var ext, ns, extOwner int64
	var version, namespace, owner, currentUser string
	var reloc bool
	if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&currentUser); err != nil {
		t.Fatal(err)
	}
	err := c.QueryRowContext(p.Ctx, "SELECT e.oid,extnamespace,extowner,extversion,extrelocatable,nspname,rolname FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_roles r ON r.oid=e.extowner WHERE extname='fuzzystrmatch'").Scan(&ext, &ns, &extOwner, &version, &reloc, &namespace, &owner)
	if err != nil || ext <= 0 || ns <= 0 || extOwner <= 0 || version != "1.2" || !reloc || namespace != "sx_addon" || owner != currentUser {
		t.Fatal("extension identity", ext, ns, extOwner, version, reloc, namespace, owner, err)
	}
	want := map[string]struct { arity, result int64 }{
		"sx_addon.soundex(text)": {1, 25},
		"sx_addon.difference(text,text)": {2, 23},
	}
	rows, err := c.QueryContext(p.Ctx, "SELECT p.oid,p.oid::regprocedure::text,pronargs,prorettype,provolatile,proisstrict,proparallel,p.proowner FROM pg_proc p WHERE pronamespace=$1 AND (proname='soundex' OR proname='difference')", ns)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := map[int64]bool{}
	for rows.Next() {
		var id, arity, result, memberOwner int64
		var signature, volatility, parallel string
		var strict bool
		if err := rows.Scan(&id, &signature, &arity, &result, &volatility, &strict, &parallel, &memberOwner); err != nil {
			t.Fatal(err)
		}
		w, ok := want[signature]
		if !ok || id <= 0 || ids[id] || arity != w.arity || result != w.result || volatility != "i" || !strict || parallel != "s" || memberOwner != extOwner {
			t.Fatal("selected procedure identity", id, signature, arity, result, volatility, strict, parallel, memberOwner)
		}
		ids[id] = true
		delete(want, signature)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(want) != 0 {
		t.Fatal("missing signatures", want)
	}
	for id := range ids {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_depend WHERE classid=1255 AND objid=$1 AND refclassid=3079 AND refobjid=$2 AND deptype='e'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, id, ext))
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_depend WHERE classid=3079 AND objid=$1 AND refclassid=2615 AND refobjid=$2 AND deptype='n'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, ext, ns))
	if !b.PG {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_depend WHERE refclassid=3079 AND refobjid=$1 AND deptype='e'", []string{"count"}, []string{"INT8"}, [][]any{{int64(10)}}, ext))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_proc WHERE pronamespace=$1 AND proname='text_soundex'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, ns))
	}
}

func TestReferenceSoundexInstallation(t *testing.T) {
	soundexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE sx_sentinel(id int)", 0))
		run(fixture.Exec("INSERT INTO sx_sentinel VALUES(17)", 1))
		run(fixture.Exec("CREATE SCHEMA sx_addon", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE EXTENSION fuzzystrmatch SCHEMA sx_addon VERSION '1.2'", 0))
		run(soundexSelect(b, "SELECT sx_addon.soundex('Ann')", "soundex", "A500"))
		run(soundexSelect(b, "SELECT sx_addon.difference('Ann','Anne')", "difference", int64(4)))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='fuzzystrmatch'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE nspname='sx_addon' AND (proname='soundex' OR proname='difference')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT id FROM sx_sentinel ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}}))
		run(fixture.Exec("CREATE EXTENSION fuzzystrmatch SCHEMA sx_addon VERSION '1.2'", 0))
		soundexInventory(t, p, b, c)
		for _, name := range []string{"soundex", "difference"} {
			args, types := "'Ann'", "unknown"
			if name == "difference" {
				args, types = "'Ann','Anne'", "unknown, unknown"
			}
			s := extensionMissing("SELECT "+name+"("+args+")", name, types)
			s.SelectRows, s.ErrorStage = true, "execute"
			run(s)
			s.Prepared, s.ErrorStage = true, "prepare"
			run(s)
		}
		run(soundexSelect(b, "SELECT sx_addon.soundex('Ann')", "soundex", "A500"))
		run(soundexSelect(b, "SELECT sx_addon.difference('Ann','Anne')", "difference", int64(4)))
		run(fixture.Exec("SET search_path TO sx_addon,public", 0))
		soundexFrames(t, p, b, c, "soundex", []struct { args []any; want any }{{[]any{"Ann"}, "A500"}, {[]any{nil}, nil}, {[]any{""}, ""}, {[]any{"Anne"}, "A500"}})
		soundexFrames(t, p, b, c, "difference", []struct { args []any; want any }{{[]any{"Ann", "Anne"}, int64(4)}, {[]any{nil, "Ann"}, nil}, {[]any{"", ""}, int64(4)}, {[]any{"Anne", "Andrew"}, int64(2)}})
		run(fixture.Exec("RESET search_path", 0))
	})
}
