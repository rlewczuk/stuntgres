package reference_test

import (
	"bytes"
	"database/sql"
	"regexp"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceHstoreConstructPresentationReplay(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "hs_construct_replay")
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		hstoreRun(t, p, b, c, fixture.Exec("CREATE SCHEMA hs_construct_replay", 0))
		hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_construct_replay_data(id integer,k text,w text,v hstore)", 0))
		hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_construct_replay_data VALUES (1,'a','1',hstore('a','1')),(2,'a',NULL,hstore('a',NULL::text)),(3,'a','NULL',hstore('a','NULL'))", 3))
		// Capture every selected role in a stored consumer before changing its presentation.
		hstoreRun(t, p, b, c, fixture.Exec("CREATE VIEW hs_construct_replay_view AS SELECT id,hstore(k,w)::text AS pair,hstore(ARRAY[k,w])::text AS single,hstore(ARRAY[k],ARRAY[w])::text AS paired,fetchval(v,k) AS fetched,slice_array(v,ARRAY[k,'missing'])::text AS sliced,v->k AS scalar,(v->ARRAY[k,'missing'])::text AS requested,exist(v,k) AS present,defined(v,k) AS valued FROM hs_construct_replay_data", 0))
		hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_construct_replay_boundaries(id integer,k text,w text,keys text[],vals text[],single text[],matrix text[],v hstore,request text[])", 0))
		hstoreRun(t, p, b, c, fixture.Exec(`INSERT INTO hs_construct_replay_boundaries VALUES
			(1,$$a"b\c$$,$$x"y\z$$,ARRAY[$$a"b\c$$],ARRAY[$$x"y\z$$],ARRAY[$$a"b\c$$,$$x"y\z$$],ARRAY[[$$a"b\c$$,$$x"y\z$$]],hstore($$a"b\c$$,$$x"y\z$$),ARRAY[$$a"b\c$$,$$a"b\c$$,NULL,'missing']),
			(2,'a',NULL,NULL,NULL,NULL,NULL,NULL,ARRAY['a']),
			(3,'a',NULL,ARRAY['a','b'],NULL,ARRAY['a',NULL,'b',NULL],ARRAY[['a',NULL],['b',NULL]],hstore(ARRAY['a','b'],NULL::text[]),ARRAY['a','b']),
			(4,'a','1',ARRAY['a','b'],ARRAY['1',NULL],ARRAY['a','1','b',NULL],ARRAY[['a','1'],['b',NULL]],hstore(ARRAY['a','b'],ARRAY['1',NULL]),'[0:1][-1:1]={{b,a,a},{NULL,missing,b}}'::text[]),
			(5,NULL,'1',NULL,ARRAY['1'],NULL,NULL,hstore('a','1'),NULL)`, 5))
		hstoreRun(t, p, b, c, fixture.Exec("CREATE VIEW hs_construct_replay_boundary_view AS SELECT id,hstore(k,w)::text AS pair,hstore(single)::text AS single,hstore(keys,vals)::text AS paired,hstore(matrix)::text AS matrix,fetchval(v,k) AS fetched,slice_array(v,request) AS sliced,v->k AS scalar,v->request AS requested,exist(v,k) AS present,defined(v,k) AS valued FROM hs_construct_replay_boundaries", 0))
		for _, tc := range []struct{ name, signature, renamed string }{
			{"hstore", "text,text", "pair_now"},
			{"hstore", "text[]", "single_now"},
			{"hstore", "text[],text[]", "paired_now"},
			{"fetchval", "public.hstore,text", "fetch_now"},
			{"slice_array", "public.hstore,text[]", "slice_now"},
			{"exist", "public.hstore,text", "exist_now"},
			{"defined", "public.hstore,text", "defined_now"},
		} {
			var before, after int64
			if err := c.QueryRowContext(p.Ctx, "SELECT $1::regprocedure::oid", "public."+tc.name+"("+tc.signature+")").Scan(&before); err != nil {
				t.Fatal(err)
			}
			hstoreRun(t, p, b, c, fixture.Exec("ALTER FUNCTION public."+tc.name+"("+tc.signature+") RENAME TO "+tc.renamed, 0))
			hstoreRun(t, p, b, c, fixture.Exec("ALTER FUNCTION public."+tc.renamed+"("+tc.signature+") SET SCHEMA hs_construct_replay", 0))
			if err := c.QueryRowContext(p.Ctx, "SELECT $1::regprocedure::oid", "hs_construct_replay."+tc.renamed+"("+tc.signature+")").Scan(&after); err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Fatalf("presentation replaced %s(%s): %d -> %d", tc.name, tc.signature, before, after)
			}
		}
		hstoreConstructPresentationReplayVerify(t, p, b, c)
		if b.PG {
			return // Dump/Restore are public native controls, not SQL operations.
		}
		dump := hstoreDump(t, p, p.Native)
		hstoreConstructPresentationReplaySQL(t, dump)
		destination := fixture.NewFixture(t, false, true)
		if _, err := destination.Native.ExecContext(p.Ctx, "CREATE TABLE hs_construct_replay_displace(v integer); CREATE EXTENSION hstore"); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, destination.Native, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		target := fixture.Backend{DB: destination.Memory, Recorder: destination.MemoryRecord}
		hstoreConstructPresentationReplayVerify(t, p, target, destination.Memory)
		// The very script asserted above must also reconstruct the consumers on
		// the pinned fixture; do not synthesize a parallel PostgreSQL replay.
		if p.Postgres != nil {
			pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, string(dump)); err != nil {
				t.Fatal(err)
			}
			hstoreConstructPresentationReplayVerify(t, p, pg, p.Postgres)
			// The next backend executes the same seed and genuine DDL independently.
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; DROP SCHEMA hs_construct_replay CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func hstoreConstructPresentationReplaySQL(t *testing.T, dump []byte) {
	t.Helper()
	script := string(dump)
	// The genuine recipe is created in collision-free coordinates, not
	// necessarily its final public namespace. Capture that SQL identity from
	// the one real CREATE; keep every subsequent rename/move exact.
	installation := regexp.MustCompile(`(?m)^CREATE EXTENSION "hstore" WITH SCHEMA ("(?:[^"]|"")+") VERSION '1\.8';$`).FindAllStringSubmatchIndex(script, -1)
	if len(installation) != 1 {
		t.Fatalf("missing unique released hstore installation:\n%s", script)
	}
	created := installation[0]
	schema := script[created[2]:created[3]]
	consumer := strings.Index(script, `CREATE VIEW "public"."hs_construct_replay_view" `)
	if consumer < 0 {
		t.Fatal("missing actual stored presentation consumer")
	}
	for _, tc := range []struct{ name, arguments, renamed, symbol string }{
		{"hstore", "pg_catalog.text, pg_catalog.text", "pair_now", "hstore_from_text"},
		{"hstore", "pg_catalog.text[]", "single_now", "hstore_from_array"},
		{"hstore", "pg_catalog.text[], pg_catalog.text[]", "paired_now", "hstore_from_arrays"},
		{"fetchval", "public.hstore, pg_catalog.text", "fetch_now", "hstore_fetchval"},
		{"slice_array", "public.hstore, pg_catalog.text[]", "slice_now", "hstore_slice_to_array"},
		{"exist", "public.hstore, pg_catalog.text", "exist_now", "hstore_exists"},
		{"defined", "public.hstore, pg_catalog.text", "defined_now", "hstore_defined"},
	} {
		rename := `ALTER FUNCTION ` + schema + `."` + tc.name + `"(` + tc.arguments + `) RENAME TO "` + tc.renamed + `";` + "\n"
		move := `ALTER FUNCTION ` + schema + `."` + tc.renamed + `"(` + tc.arguments + `) SET SCHEMA "hs_construct_replay";` + "\n"
		if strings.Count(script, rename) != 1 || strings.Count(script, move) != 1 || created[1] >= strings.Index(script, rename) || strings.Index(script, rename) >= strings.Index(script, move) || strings.Index(script, move) >= consumer {
			t.Fatalf("missing exact ordered SQL identity replay:\n%s%s\nactual dump:\n%s", rename, move, script)
		}
		if strings.Contains(script, `."`+tc.symbol+`"(`) {
			t.Fatalf("replay used executable symbol %s as SQL identity:\n%s", tc.symbol, script)
		}
	}
}

func hstoreConstructPresentationReplayVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor) {
	t.Helper()
	names := []string{"id", "pair", "single", "paired", "fetched", "sliced", "scalar", "requested", "present", "valued"}
	types := []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}
	want := [][]any{
		{int64(1), `"a"=>"1"`, `"a"=>"1"`, `"a"=>"1"`, "1", "{1,NULL}", "1", "{1,NULL}", true, true},
		{int64(2), `"a"=>NULL`, `"a"=>NULL`, `"a"=>NULL`, nil, "{NULL,NULL}", nil, "{NULL,NULL}", true, false},
		{int64(3), `"a"=>"NULL"`, `"a"=>"NULL"`, `"a"=>"NULL"`, "NULL", `{"NULL",NULL}`, "NULL", `{"NULL",NULL}`, true, true},
	}
	stored := fixture.Select("SELECT id,pair,single,paired,fetched,sliced,scalar,requested,present,valued FROM hs_construct_replay_view ORDER BY id", names, types, want)
	current := fixture.Select("SELECT id,hs_construct_replay.pair_now(k,w)::text AS pair,hs_construct_replay.single_now(ARRAY[k,w])::text AS single,hs_construct_replay.paired_now(ARRAY[k],ARRAY[w])::text AS paired,hs_construct_replay.fetch_now(v,k) AS fetched,hs_construct_replay.slice_now(v,ARRAY[k,'missing'])::text AS sliced,v->k AS scalar,(v->ARRAY[k,'missing'])::text AS requested,hs_construct_replay.exist_now(v,k) AS present,hs_construct_replay.defined_now(v,k) AS valued FROM hs_construct_replay_data ORDER BY id", names, types, want)
	for _, s := range []fixture.Step{stored, current} {
		hstoreRun(t, p, b, db, s)
		hstoreRun(t, p, b, db, fixture.Prepared(s))
	}
	hstoreRun(t, p, b, db, fixture.Select("SELECT hs_construct_replay.fetch_now(v,'missing') AS missing,hs_construct_replay.exist_now(v,'missing') AS present,hs_construct_replay.defined_now(v,'missing') AS valued FROM hs_construct_replay_data WHERE id=2", []string{"missing", "present", "valued"}, []string{"TEXT", "BOOL", "BOOL"}, [][]any{{nil, false, false}}))
	hstoreRun(t, p, b, db, hstoreSelect(b, "SELECT hs_construct_replay.pair_now(k,w) AS v FROM hs_construct_replay_data ORDER BY id", []string{"v"}, [][]any{{`"a"=>"1"`}, {`"a"=>NULL`}, {`"a"=>"NULL"`}}, nil))
	boundaryNames := []string{"id", "pair", "single", "paired", "matrix", "fetched", "scalar", "present", "valued"}
	boundaryTypes := []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}
	boundaryWant := [][]any{
		{int64(1), `"a\"b\\c"=>"x\"y\\z"`, `"a\"b\\c"=>"x\"y\\z"`, `"a\"b\\c"=>"x\"y\\z"`, `"a\"b\\c"=>"x\"y\\z"`, `x"y\z`, `x"y\z`, true, true},
		{int64(2), `"a"=>NULL`, nil, nil, nil, nil, nil, nil, nil},
		{int64(3), `"a"=>NULL`, `"a"=>NULL, "b"=>NULL`, `"a"=>NULL, "b"=>NULL`, `"a"=>NULL, "b"=>NULL`, nil, nil, true, false},
		{int64(4), `"a"=>"1"`, `"a"=>"1", "b"=>NULL`, `"a"=>"1", "b"=>NULL`, `"a"=>"1", "b"=>NULL`, "1", "1", true, true},
		{int64(5), nil, nil, nil, nil, nil, nil, nil, nil},
	}
	boundaryStored := "SELECT id,pair,single,paired,matrix,fetched,scalar,present,valued FROM hs_construct_replay_boundary_view ORDER BY id"
	boundaryCurrent := "SELECT id,hs_construct_replay.pair_now(k,w)::text AS pair,hs_construct_replay.single_now(single)::text AS single,hs_construct_replay.paired_now(keys,vals)::text AS paired,hs_construct_replay.single_now(matrix)::text AS matrix,hs_construct_replay.fetch_now(v,k) AS fetched,v->k AS scalar,hs_construct_replay.exist_now(v,k) AS present,hs_construct_replay.defined_now(v,k) AS valued FROM hs_construct_replay_boundaries ORDER BY id"
	for _, query := range []string{boundaryStored, boundaryCurrent} {
		s := fixture.Select(query, boundaryNames, boundaryTypes, boundaryWant)
		hstoreRun(t, p, b, db, s)
		hstoreRun(t, p, b, db, fixture.Prepared(s))
	}
	arrayWant := [][]any{
		{int64(1), `{"x\"y\\z","x\"y\\z",NULL,NULL}`, `{"x\"y\\z","x\"y\\z",NULL,NULL}`},
		{int64(2), nil, nil},
		{int64(3), "{NULL,NULL}", "{NULL,NULL}"},
		{int64(4), "[0:1][-1:1]={{NULL,1,1},{NULL,NULL,NULL}}", "[0:1][-1:1]={{NULL,1,1},{NULL,NULL,NULL}}"},
		{int64(5), nil, nil},
	}
	for _, query := range []string{
		"SELECT id,sliced,requested FROM hs_construct_replay_boundary_view ORDER BY id",
		"SELECT id,hs_construct_replay.slice_now(v,request) AS sliced,v->request AS requested FROM hs_construct_replay_boundaries ORDER BY id",
	} {
		s := fixture.ArrayReferenceSelect(query, []string{"id", "sliced", "requested"}, []string{"INT4", "_TEXT", "_TEXT"}, []string{"INT4", "_TEXT", "_TEXT"}, arrayWant, b.PG)
		hstoreRun(t, p, b, db, s)
		hstoreRun(t, p, b, db, fixture.Prepared(s))
	}
	for _, source := range []string{
		"SELECT sliced AS a FROM hs_construct_replay_boundary_view WHERE id=4",
		"SELECT requested AS a FROM hs_construct_replay_boundary_view WHERE id=4",
		"SELECT hs_construct_replay.slice_now(v,request) AS a FROM hs_construct_replay_boundaries WHERE id=4",
		"SELECT v->request AS a FROM hs_construct_replay_boundaries WHERE id=4",
	} {
		query := "SELECT array_ndims(a) AS rank,array_lower(a,1) AS lo1,array_upper(a,1) AS hi1,array_lower(a,2) AS lo2,array_upper(a,2) AS hi2,array_length(a,1) AS n1,array_length(a,2) AS n2,cardinality(a) AS n,a[0][-1] AS first,a[0][0] AS second,a[0][1] AS duplicate,a[1][-1] AS null_key,a[1][0] AS missing,a[1][1] AS null_value FROM (" + source + ") s"
		s := fixture.Select(query, []string{"rank", "lo1", "hi1", "lo2", "hi2", "n1", "n2", "n", "first", "second", "duplicate", "null_key", "missing", "null_value"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(2), int64(0), int64(1), int64(-1), int64(1), int64(2), int64(3), int64(6), nil, "1", "1", nil, nil, nil}})
		hstoreRun(t, p, b, db, s)
		hstoreRun(t, p, b, db, fixture.Prepared(s))
	}
	s := hstoreSelect(b, "SELECT hs_construct_replay.paired_now(keys,vals) AS v FROM hs_construct_replay_boundaries ORDER BY id", []string{"v"}, [][]any{{`"a\"b\\c"=>"x\"y\\z"`}, {nil}, {`"a"=>NULL, "b"=>NULL`}, {`"a"=>"1", "b"=>NULL`}, {nil}}, nil)
	hstoreRun(t, p, b, db, s)
	hstoreRun(t, p, b, db, fixture.Prepared(s))
	for _, tc := range []struct {
		signature, result, symbol string
		strict                    bool
	}{
		{"hs_construct_replay.pair_now(text,text)", "public.hstore", "hstore_from_text", false},
		{"hs_construct_replay.paired_now(text[],text[])", "public.hstore", "hstore_from_arrays", false},
		{"hs_construct_replay.single_now(text[])", "public.hstore", "hstore_from_array", true},
		{"hs_construct_replay.fetch_now(public.hstore,text)", "text", "hstore_fetchval", true},
		{"hs_construct_replay.slice_now(public.hstore,text[])", "text[]", "hstore_slice_to_array", true},
		{"hs_construct_replay.exist_now(public.hstore,text)", "boolean", "hstore_exists", true},
		{"hs_construct_replay.defined_now(public.hstore,text)", "boolean", "hstore_defined", true},
	} {
		hstoreRun(t, p, b, db, fixture.Select("SELECT p.prorettype=$2::regtype AS result,p.prosrc,p.proisstrict,p.pronamespace='hs_construct_replay'::regnamespace AS namespace FROM pg_proc p WHERE p.oid=$1::regprocedure", []string{"result", "prosrc", "proisstrict", "namespace"}, []string{"BOOL", "TEXT", "BOOL", "BOOL"}, [][]any{{true, tc.symbol, tc.strict, true}}, tc.signature, tc.result))
		hstoreRun(t, p, b, db, fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid='pg_proc'::regclass AND objid=$1::regprocedure AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='hstore') AND deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, tc.signature))
	}
	for _, tc := range []struct{ signature, right, procedure, result string }{
		{"public.->(public.hstore,text)", "text", "hs_construct_replay.fetch_now(public.hstore,text)", "text"},
		{"public.->(public.hstore,text[])", "text[]", "hs_construct_replay.slice_now(public.hstore,text[])", "text[]"},
	} {
		hstoreRun(t, p, b, db, fixture.Select("SELECT oprleft='public.hstore'::regtype AS left_type,oprright=$2::regtype AS right_type,oprcode=$3::regprocedure AS backing,oprresult=$4::regtype AS result FROM pg_operator WHERE oid=$1::regoperator", []string{"left_type", "right_type", "backing", "result"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}, tc.signature, tc.right, tc.procedure, tc.result))
		hstoreRun(t, p, b, db, fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid='pg_operator'::regclass AND objid=$1::regoperator AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='hstore') AND deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, tc.signature))
	}
}
