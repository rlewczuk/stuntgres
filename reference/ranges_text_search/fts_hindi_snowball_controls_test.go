package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFTSHindiSnowballControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"hindi_config_owner"})
	fixture.SchemaReferenceCleanup(t, "hindi_config_schema")
	var emitted []byte
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		var dictOwner, configOwner string
		if err := c.QueryRowContext(p.Ctx, "SELECT dictowner::regrole::text FROM pg_ts_dict WHERE oid='pg_catalog.hindi_stem'::regdictionary").Scan(&dictOwner); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT cfgowner::regrole::text FROM pg_ts_config WHERE oid='pg_catalog.hindi'::regconfig").Scan(&configOwner); err != nil {
			t.Fatal(err)
		}
		if b.PG {
			// Stock catalog edits outlive public-schema fixture cleanup. Restore
			// canonical seed names, owners, options and mappings even after failure.
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				queries := []string{
					"RESET ROLE",
					"ALTER TEXT SEARCH DICTIONARY hindi_config_schema.hindi_seed SET SCHEMA pg_catalog",
					"ALTER TEXT SEARCH DICTIONARY pg_catalog.hindi_seed RENAME TO hindi_stem",
					"ALTER TEXT SEARCH DICTIONARY pg_catalog.hindi_stem (language=hindi,stopwords)",
					"ALTER TEXT SEARCH DICTIONARY pg_catalog.hindi_stem OWNER TO " + dictOwner,
					"ALTER TEXT SEARCH CONFIGURATION hindi_config_schema.hindi_seed_config SET SCHEMA pg_catalog",
					"ALTER TEXT SEARCH CONFIGURATION pg_catalog.hindi_seed_config RENAME TO hindi",
					"ALTER TEXT SEARCH CONFIGURATION pg_catalog.hindi OWNER TO " + configOwner,
					"ALTER TEXT SEARCH CONFIGURATION pg_catalog.hindi ALTER MAPPING FOR word,hword_part,hword WITH pg_catalog.hindi_stem",
					"ALTER TEXT SEARCH CONFIGURATION pg_catalog.hindi ALTER MAPPING FOR asciiword,hword_asciipart,asciihword WITH pg_catalog.english_stem",
				}
				for _, q := range queries {
					if _, err := c.ExecContext(ctx, q); err != nil {
						t.Error("restore stock Hindi state", q, err)
					}
				}
			}()
		}
		for _, q := range []string{
			"CREATE ROLE hindi_config_owner",
			"CREATE SCHEMA hindi_config_schema",
			"CREATE TEXT SEARCH DICTIONARY hindi_control_dict (TEMPLATE=snowball,Language=hindi,StopWords=english)",
			"CREATE TEXT SEARCH CONFIGURATION hindi_control (COPY=pg_catalog.hindi)",
			"ALTER TEXT SEARCH CONFIGURATION hindi_control ALTER MAPPING FOR asciiword WITH hindi_control_dict",
			"ALTER TEXT SEARCH CONFIGURATION hindi_control ALTER MAPPING FOR word WITH pg_catalog.simple,pg_catalog.hindi_stem",
			"ALTER TEXT SEARCH CONFIGURATION hindi_control DROP MAPPING FOR numword",
			"ALTER TEXT SEARCH CONFIGURATION hindi_control RENAME TO hindi_restored",
			"ALTER TEXT SEARCH CONFIGURATION hindi_restored SET SCHEMA hindi_config_schema",
			"ALTER TEXT SEARCH CONFIGURATION hindi_config_schema.hindi_restored OWNER TO hindi_config_owner",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.hindi_stem (language='HiNdI',stopwords)",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.hindi_stem RENAME TO hindi_seed",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.hindi_seed SET SCHEMA hindi_config_schema",
			"ALTER TEXT SEARCH DICTIONARY hindi_config_schema.hindi_seed OWNER TO hindi_config_owner",
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog.hindi ALTER MAPPING FOR asciiword WITH pg_catalog.simple",
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog.hindi RENAME TO hindi_seed_config",
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog.hindi_seed_config SET SCHEMA hindi_config_schema",
			"ALTER TEXT SEARCH CONFIGURATION hindi_config_schema.hindi_seed_config OWNER TO hindi_config_owner",
			"CREATE DOMAIN hindi_config_domain AS regconfig",
			"CREATE TYPE hindi_config_pair AS (v regconfig,d hindi_config_domain)",
			"CREATE TABLE hindi_config_rows(id int,c regconfig DEFAULT 'hindi_config_schema.hindi_restored'::regconfig,a regconfig[],d hindi_config_domain,p hindi_config_pair)",
			"CREATE VIEW hindi_config_view AS SELECT c FROM hindi_config_rows",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO hindi_config_rows(id,a,d,p) VALUES(1,ARRAY['hindi_config_schema.hindi_restored'::regconfig],'hindi_config_schema.hindi_restored',ROW('hindi_config_schema.hindi_restored'::regconfig,'hindi_config_schema.hindi_restored'::hindi_config_domain)::hindi_config_pair)", 1))
		check := func(p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			t.Helper()
			steps := []fixture.Step{
				ftsConfigurationVector("SELECT to_tsvector(c,'कमल RATS') AS normalized FROM hindi_config_rows", "'rats':2 'कमल':1"),
				ftsConfigurationVector("SELECT to_tsvector('hindi_config_schema.hindi_seed_config','कमल RATS') AS normalized", "'rats':2 'कमल':1"),
				fixture.Select("SELECT ts_lexize('hindi_config_schema.hindi_seed','ONLY') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{only}"}}),
				fixture.Select("SELECT c FROM hindi_config_view", []string{"c"}, []string{"REGCONFIG"}, [][]any{{"hindi_config_schema.hindi_restored"}}),
				fixture.Select("SELECT a[1]::text,d::regconfig::text,(p).v::text,(p).d::regconfig::text FROM hindi_config_rows", []string{"a", "d", "v", "d"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"hindi_config_schema.hindi_restored", "hindi_config_schema.hindi_restored", "hindi_config_schema.hindi_restored", "hindi_config_schema.hindi_restored"}}),
				fixture.Select("SELECT dictinitoption,dictowner='hindi_config_owner'::regrole::oid AS owned FROM pg_ts_dict WHERE oid='hindi_config_schema.hindi_seed'::regdictionary", []string{"dictinitoption", "owned"}, []string{"TEXT", "BOOL"}, [][]any{{"language = 'HiNdI'", true}}),
				fixture.Select("SELECT mapseqno,mapdict='pg_catalog.simple'::regdictionary::oid AS first,mapdict='hindi_config_schema.hindi_seed'::regdictionary::oid AS second FROM pg_ts_config_map WHERE mapcfg='hindi_config_schema.hindi_restored'::regconfig AND maptokentype=2 ORDER BY mapseqno", []string{"mapseqno", "first", "second"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, false}, {int64(2), false, true}}),
				fixture.Select("SELECT maptokentype FROM pg_ts_config_map WHERE mapcfg='hindi_config_schema.hindi_restored'::regconfig AND maptokentype=3", []string{"maptokentype"}, []string{"INT4"}, nil),
			}
			steps[0].CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
			for _, s := range steps {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO hindi_config_rows(id) VALUES(2)", 1))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT c='hindi_config_schema.hindi_restored'::regconfig AS same FROM hindi_config_rows WHERE id=2", []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM hindi_config_rows WHERE id=2", 1))
		}
		check(p, b, c)
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("missing native SQL")
			}
			for _, q := range []string{"DROP VIEW hindi_config_view", "DROP TABLE hindi_config_rows", "DROP TYPE hindi_config_pair", "DROP DOMAIN hindi_config_domain", "DROP TEXT SEARCH CONFIGURATION hindi_config_schema.hindi_restored", "DROP TEXT SEARCH DICTIONARY hindi_control_dict"} {
				run(fixture.Exec(q, 0))
			}
			// Put installed identities back at their creation names before replay.
			for _, q := range []string{"ALTER TEXT SEARCH DICTIONARY hindi_config_schema.hindi_seed SET SCHEMA pg_catalog", "ALTER TEXT SEARCH DICTIONARY pg_catalog.hindi_seed RENAME TO hindi_stem", "ALTER TEXT SEARCH DICTIONARY pg_catalog.hindi_stem (language=hindi,stopwords)", "ALTER TEXT SEARCH DICTIONARY pg_catalog.hindi_stem OWNER TO " + dictOwner, "ALTER TEXT SEARCH CONFIGURATION hindi_config_schema.hindi_seed_config SET SCHEMA pg_catalog", "ALTER TEXT SEARCH CONFIGURATION pg_catalog.hindi_seed_config RENAME TO hindi", "ALTER TEXT SEARCH CONFIGURATION pg_catalog.hindi OWNER TO " + configOwner, "ALTER TEXT SEARCH CONFIGURATION pg_catalog.hindi ALTER MAPPING FOR asciiword WITH english_stem", "DROP SCHEMA hindi_config_schema"} {
				run(fixture.Exec(q, 0))
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual native SQL replay", err)
			}
			check(p, b, c)
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(dump.Bytes())
		for _, reset := range []bool{false, true} {
			if reset {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				hindiConfigurationMaps(t, p, b, c, "hindi")
				run(fixture.Select("SELECT ts_lexize('hindi_stem','ONLY') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{only}"}}))
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check(p, b, c)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(emitted, again.Bytes()) {
				t.Fatal("seed redump", err)
			}
		}
		run(fixture.Exec("CREATE TABLE hindi_config_sentinel(v int)", 0))
		run(fixture.Exec("INSERT INTO hindi_config_sentinel VALUES(42)", 1))
		var destination bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &destination); err != nil {
			t.Fatal(err)
		}
		old, err := c.QueryContext(p.Ctx, "SELECT to_tsvector(c,'कमल RATS') AS normalized FROM hindi_config_rows")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		configurationVectorMetadata(t, old, false)
		preserved := func() {
			check(p, b, c)
			run(fixture.Select("SELECT v FROM hindi_config_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(destination.Bytes(), after.Bytes()) {
				t.Fatal("failed seed replay changed destination", err)
			}
		}
		d := fixture.ControlsReferenceDiagnostic("22023", `token type "not_a_token" does not exist`)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(string(emitted)+"ALTER TEXT SEARCH CONFIGURATION hindi_config_schema.hindi_restored ALTER MAPPING FOR asciiword,not_a_token WITH simple;")), &d)
		preserved()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		preserved()
		expired, expire := context.WithDeadline(p.Ctx, time.Unix(1, 0))
		defer expire()
		if err := stuntgres.Restore(expired, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		preserved()
		reading, stop := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(reading, p.Native, &fixture.EnumCancelReader{Reader: bytes.NewReader(emitted), Cancel: stop})
		stop()
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		preserved()
		if !old.Next() {
			t.Fatal(old.Err())
		}
		var value string
		if err := old.Scan(&value); err != nil || value != "'rats':2 'कमल':1" {
			t.Fatal(value, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal(old.Err())
		}
		old.Close()
		fresh := fixture.NewFixture(t, false, true)
		fc, err := fresh.Memory.Conn(fresh.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		fb := fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}
		fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Exec("CREATE ROLE hindi_config_owner", 0))
		var source int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'hindi_config_schema.hindi_restored'::regconfig::oid::bigint").Scan(&source); err != nil {
			t.Fatal(err)
		}
		var collision string
		for i := 0; ; i++ {
			name := fmt.Sprintf("pg_temp.hindi_config_collision_%d", i)
			fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Exec("CREATE TEXT SEARCH CONFIGURATION "+name+" (PARSER=pg_catalog.default)", 0))
			var id int64
			if err := fc.QueryRowContext(fresh.Ctx, "SELECT $1::regconfig::oid::bigint", name).Scan(&id); err != nil {
				t.Fatal(err)
			}
			if id > source {
				t.Fatal("collision overshot", id, source)
			}
			if id == source {
				collision = name
				break
			}
		}
		collisionRows, err := fc.QueryContext(fresh.Ctx, "SELECT to_tsvector($1::regconfig,'RATS') AS normalized", collision)
		if err != nil {
			t.Fatal(err)
		}
		defer collisionRows.Close()
		configurationVectorMetadata(t, collisionRows, false)
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		check(fresh, fb, fc)
		fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Select("SELECT c.oid::bigint,n.nspname LIKE 'pg_temp_%' AS temporary FROM pg_ts_config c JOIN pg_namespace n ON n.oid=c.cfgnamespace WHERE c.oid=$1::oid AND c.oid=$2::regconfig::oid", []string{"oid", "temporary"}, []string{"INT8", "BOOL"}, [][]any{{source, true}}, source, collision))
		fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, ftsConfigurationVector("SELECT to_tsvector($1::regconfig,'RATS') AS normalized", "", collision))
		if !collisionRows.Next() {
			t.Fatal(collisionRows.Err())
		}
		var collisionValue string
		if err := collisionRows.Scan(&collisionValue); err != nil || collisionValue != "" {
			t.Fatal("retained TEMP vector", collisionValue, err)
		}
		if collisionRows.Next() || collisionRows.Err() != nil {
			t.Fatal(collisionRows.Err())
		}
		collisionRows.Close()
		fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Select("SELECT 'hindi_config_schema.hindi_restored'::regconfig::oid<>$1::oid AS relocated", []string{"relocated"}, []string{"BOOL"}, [][]any{{true}}, source))
		for _, q := range []string{"CREATE TABLE hindi_collision_sentinel(v int)", "INSERT INTO hindi_collision_sentinel VALUES(42)"} {
			affected := int64(0)
			if q[0] == 'I' {
				affected = 1
			}
			fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Exec(q, affected))
		}
		var collisionBefore bytes.Buffer
		if err := stuntgres.Dump(fresh.Ctx, fresh.Native, &collisionBefore); err != nil {
			t.Fatal(err)
		}
		retained, err := fc.QueryContext(fresh.Ctx, "SELECT a[1]::text FROM hindi_config_rows")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		retainedTypes, err := retained.ColumnTypes()
		if err != nil || retainedTypes[0].DatabaseTypeName() != "TEXT" {
			t.Fatal(retainedTypes, err)
		}
		preserveCollision := func() {
			check(fresh, fb, fc)
			fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Select("SELECT v FROM hindi_collision_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
			fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Select("SELECT c.oid::bigint,n.nspname LIKE 'pg_temp_%' AS temporary FROM pg_ts_config c JOIN pg_namespace n ON n.oid=c.cfgnamespace WHERE c.oid=$1::oid AND c.oid=$2::regconfig::oid", []string{"oid", "temporary"}, []string{"INT8", "BOOL"}, [][]any{{source, true}}, source, collision))
			var after bytes.Buffer
			if err := stuntgres.Dump(fresh.Ctx, fresh.Native, &after); err != nil || !bytes.Equal(collisionBefore.Bytes(), after.Bytes()) {
				t.Fatal("failed collision replay changed destination", err)
			}
		}
		fixture.AssertDiagnostic(t, stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewBufferString(string(emitted)+"ALTER TEXT SEARCH CONFIGURATION hindi_config_schema.hindi_restored ALTER MAPPING FOR asciiword,not_a_token WITH simple;")), &d)
		preserveCollision()
		collisionCanceled, collisionCancel := context.WithCancel(fresh.Ctx)
		collisionCancel()
		if err := stuntgres.Restore(collisionCanceled, fresh.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		preserveCollision()
		collisionExpired, collisionExpire := context.WithDeadline(fresh.Ctx, time.Unix(1, 0))
		defer collisionExpire()
		if err := stuntgres.Restore(collisionExpired, fresh.Native, bytes.NewReader(emitted)); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		preserveCollision()
		collisionReading, collisionStop := context.WithCancel(fresh.Ctx)
		err = stuntgres.Restore(collisionReading, fresh.Native, &fixture.EnumCancelReader{Reader: bytes.NewReader(emitted), Cancel: collisionStop})
		collisionStop()
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		preserveCollision()
		if !retained.Next() {
			t.Fatal(retained.Err())
		}
		if err := retained.Scan(&value); err != nil || value != "hindi_config_schema.hindi_restored" {
			t.Fatal(value, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal(retained.Err())
		}
	})
}
