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

func TestReferenceFTSFrenchSnowballControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"french_config_owner"})
	fixture.SchemaReferenceCleanup(t, "french_config_schema")
	var emitted []byte
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		var dictOwner, configOwner string
		if err := c.QueryRowContext(p.Ctx, "SELECT dictowner::regrole::text FROM pg_ts_dict WHERE oid='pg_catalog.french_stem'::regdictionary").Scan(&dictOwner); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT cfgowner::regrole::text FROM pg_ts_config WHERE oid='pg_catalog.french'::regconfig").Scan(&configOwner); err != nil {
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
					"ALTER TEXT SEARCH DICTIONARY french_config_schema.french_seed SET SCHEMA pg_catalog",
					"ALTER TEXT SEARCH DICTIONARY pg_catalog.french_seed RENAME TO french_stem",
					"ALTER TEXT SEARCH DICTIONARY pg_catalog.french_stem (language=french,stopwords=french)",
					"ALTER TEXT SEARCH DICTIONARY pg_catalog.french_stem OWNER TO " + dictOwner,
					"ALTER TEXT SEARCH CONFIGURATION french_config_schema.french_seed_config SET SCHEMA pg_catalog",
					"ALTER TEXT SEARCH CONFIGURATION pg_catalog.french_seed_config RENAME TO french",
					"ALTER TEXT SEARCH CONFIGURATION pg_catalog.french OWNER TO " + configOwner,
					"ALTER TEXT SEARCH CONFIGURATION pg_catalog.french ALTER MAPPING FOR asciiword,word,hword_part,hword_asciipart,asciihword,hword WITH pg_catalog.french_stem",
				}
				for _, q := range queries {
					if _, err := c.ExecContext(ctx, q); err != nil {
						t.Error("restore stock French state", q, err)
					}
				}
			}()
		}
		for _, q := range []string{
			"CREATE ROLE french_config_owner",
			"CREATE SCHEMA french_config_schema",
			"CREATE TEXT SEARCH DICTIONARY french_control_dict (TEMPLATE=snowball,Language=french,StopWords=french)",
			"CREATE TEXT SEARCH CONFIGURATION french_control (COPY=pg_catalog.french)",
			"ALTER TEXT SEARCH CONFIGURATION french_control ALTER MAPPING FOR asciiword WITH french_control_dict",
			"ALTER TEXT SEARCH CONFIGURATION french_control ALTER MAPPING FOR word WITH pg_catalog.simple,pg_catalog.french_stem",
			"ALTER TEXT SEARCH CONFIGURATION french_control DROP MAPPING FOR numword",
			"ALTER TEXT SEARCH CONFIGURATION french_control RENAME TO french_restored",
			"ALTER TEXT SEARCH CONFIGURATION french_restored SET SCHEMA french_config_schema",
			"ALTER TEXT SEARCH CONFIGURATION french_config_schema.french_restored OWNER TO french_config_owner",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.french_stem (language='FrEnCh',stopwords)",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.french_stem RENAME TO french_seed",
			"ALTER TEXT SEARCH DICTIONARY pg_catalog.french_seed SET SCHEMA french_config_schema",
			"ALTER TEXT SEARCH DICTIONARY french_config_schema.french_seed OWNER TO french_config_owner",
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog.french ALTER MAPPING FOR asciiword WITH pg_catalog.simple",
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog.french RENAME TO french_seed_config",
			"ALTER TEXT SEARCH CONFIGURATION pg_catalog.french_seed_config SET SCHEMA french_config_schema",
			"ALTER TEXT SEARCH CONFIGURATION french_config_schema.french_seed_config OWNER TO french_config_owner",
			"CREATE DOMAIN french_config_domain AS regconfig",
			"CREATE TYPE french_config_pair AS (v regconfig,d french_config_domain)",
			"CREATE TABLE french_config_rows(id int,c regconfig DEFAULT 'french_config_schema.french_restored'::regconfig,a regconfig[],d french_config_domain,p french_config_pair)",
			"CREATE VIEW french_config_view AS SELECT c FROM french_config_rows",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO french_config_rows(id,a,d,p) VALUES(1,ARRAY['french_config_schema.french_restored'::regconfig],'french_config_schema.french_restored',ROW('french_config_schema.french_restored'::regconfig,'french_config_schema.french_restored'::french_config_domain)::french_config_pair)", 1))
		check := func(p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			t.Helper()
			steps := []fixture.Step{
				ftsConfigurationVector("SELECT to_tsvector(c,'hôtels CHATS') AS normalized FROM french_config_rows", "'chat':2 'hôtels':1"),
				ftsConfigurationVector("SELECT to_tsvector('french_config_schema.french_seed_config','hôtels CHATS') AS normalized", "'chats':2 'hôtel':1"),
				fixture.Select("SELECT ts_lexize('french_config_schema.french_seed','été') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{été}"}}),
				fixture.Select("SELECT c FROM french_config_view", []string{"c"}, []string{"REGCONFIG"}, [][]any{{"french_config_schema.french_restored"}}),
				fixture.Select("SELECT a[1]::text,d::regconfig::text,(p).v::text,(p).d::regconfig::text FROM french_config_rows", []string{"a", "d", "v", "d"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"french_config_schema.french_restored", "french_config_schema.french_restored", "french_config_schema.french_restored", "french_config_schema.french_restored"}}),
				fixture.Select("SELECT dictinitoption,dictowner='french_config_owner'::regrole::oid AS owned FROM pg_ts_dict WHERE oid='french_config_schema.french_seed'::regdictionary", []string{"dictinitoption", "owned"}, []string{"TEXT", "BOOL"}, [][]any{{"language = 'FrEnCh'", true}}),
				fixture.Select("SELECT mapseqno,mapdict='pg_catalog.simple'::regdictionary::oid AS first,mapdict='french_config_schema.french_seed'::regdictionary::oid AS second FROM pg_ts_config_map WHERE mapcfg='french_config_schema.french_restored'::regconfig AND maptokentype=2 ORDER BY mapseqno", []string{"mapseqno", "first", "second"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, false}, {int64(2), false, true}}),
				fixture.Select("SELECT maptokentype FROM pg_ts_config_map WHERE mapcfg='french_config_schema.french_restored'::regconfig AND maptokentype=3", []string{"maptokentype"}, []string{"INT4"}, nil),
			}
			steps[0].CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
			for _, s := range steps {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			}
		}
		check(p, b, c)
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("missing native SQL")
			}
			for _, q := range []string{"DROP VIEW french_config_view", "DROP TABLE french_config_rows", "DROP TYPE french_config_pair", "DROP DOMAIN french_config_domain", "DROP TEXT SEARCH CONFIGURATION french_config_schema.french_restored", "DROP TEXT SEARCH DICTIONARY french_control_dict"} {
				run(fixture.Exec(q, 0))
			}
			// Put installed identities back at their creation names before replay.
			for _, q := range []string{"ALTER TEXT SEARCH DICTIONARY french_config_schema.french_seed SET SCHEMA pg_catalog", "ALTER TEXT SEARCH DICTIONARY pg_catalog.french_seed RENAME TO french_stem", "ALTER TEXT SEARCH DICTIONARY pg_catalog.french_stem (language=french,stopwords=french)", "ALTER TEXT SEARCH DICTIONARY pg_catalog.french_stem OWNER TO " + dictOwner, "ALTER TEXT SEARCH CONFIGURATION french_config_schema.french_seed_config SET SCHEMA pg_catalog", "ALTER TEXT SEARCH CONFIGURATION pg_catalog.french_seed_config RENAME TO french", "ALTER TEXT SEARCH CONFIGURATION pg_catalog.french OWNER TO " + configOwner, "ALTER TEXT SEARCH CONFIGURATION pg_catalog.french ALTER MAPPING FOR asciiword WITH french_stem", "DROP SCHEMA french_config_schema"} {
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
				frenchConfigurationMaps(t, p, b, c, "french")
				run(fixture.Select("SELECT ts_lexize('french_stem','été') AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{}"}}))
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
		run(fixture.Exec("CREATE TABLE french_config_sentinel(v int)", 0))
		run(fixture.Exec("INSERT INTO french_config_sentinel VALUES(42)", 1))
		var destination bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &destination); err != nil {
			t.Fatal(err)
		}
		old, err := c.QueryContext(p.Ctx, "SELECT to_tsvector(c,'hôtels CHATS') AS normalized FROM french_config_rows")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		configurationVectorMetadata(t, old, false)
		preserved := func() {
			check(p, b, c)
			run(fixture.Select("SELECT v FROM french_config_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(destination.Bytes(), after.Bytes()) {
				t.Fatal("failed seed replay changed destination", err)
			}
		}
		d := fixture.ControlsReferenceDiagnostic("22023", `token type "not_a_token" does not exist`)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(string(emitted)+"ALTER TEXT SEARCH CONFIGURATION french_config_schema.french_restored ALTER MAPPING FOR asciiword,not_a_token WITH simple;")), &d)
		preserved()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
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
		if err := old.Scan(&value); err != nil || value != "'chat':2 'hôtels':1" {
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
		fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Exec("CREATE ROLE french_config_owner", 0))
		var source int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'french_config_schema.french_restored'::regconfig::oid::bigint").Scan(&source); err != nil {
			t.Fatal(err)
		}
		var collision string
		for i := 0; ; i++ {
			name := fmt.Sprintf("pg_temp.french_config_collision_%d", i)
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
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		check(fresh, fb, fc)
		fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Select("SELECT c.oid::bigint,n.nspname LIKE 'pg_temp_%' AS temporary FROM pg_ts_config c JOIN pg_namespace n ON n.oid=c.cfgnamespace WHERE c.oid=$1::oid AND c.oid=$2::regconfig::oid", []string{"oid", "temporary"}, []string{"INT8", "BOOL"}, [][]any{{source, true}}, source, collision))
		fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, ftsConfigurationVector("SELECT to_tsvector($1::regconfig,'CHATS') AS normalized", "", collision))
		fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Select("SELECT 'french_config_schema.french_restored'::regconfig::oid<>$1::oid AS relocated", []string{"relocated"}, []string{"BOOL"}, [][]any{{true}}, source))
		for _, q := range []string{"CREATE TABLE french_collision_sentinel(v int)", "INSERT INTO french_collision_sentinel VALUES(42)"} {
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
		retained, err := fc.QueryContext(fresh.Ctx, "SELECT a[1]::text FROM french_config_rows")
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
			fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Select("SELECT v FROM french_collision_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
			fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Select("SELECT c.oid::bigint,n.nspname LIKE 'pg_temp_%' AS temporary FROM pg_ts_config c JOIN pg_namespace n ON n.oid=c.cfgnamespace WHERE c.oid=$1::oid AND c.oid=$2::regconfig::oid", []string{"oid", "temporary"}, []string{"INT8", "BOOL"}, [][]any{{source, true}}, source, collision))
			var after bytes.Buffer
			if err := stuntgres.Dump(fresh.Ctx, fresh.Native, &after); err != nil || !bytes.Equal(collisionBefore.Bytes(), after.Bytes()) {
				t.Fatal("failed collision replay changed destination", err)
			}
		}
		fixture.AssertDiagnostic(t, stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewBufferString(string(emitted)+"ALTER TEXT SEARCH CONFIGURATION french_config_schema.french_restored ALTER MAPPING FOR asciiword,not_a_token WITH simple;")), &d)
		preserveCollision()
		collisionCanceled, collisionCancel := context.WithCancel(fresh.Ctx)
		collisionCancel()
		if err := stuntgres.Restore(collisionCanceled, fresh.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
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
		if err := retained.Scan(&value); err != nil || value != "french_config_schema.french_restored" {
			t.Fatal(value, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal(retained.Err())
		}
	})
}
