package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFTSConfigurationControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"config_controls_owner", "config_restore_denied"})
	fixture.SchemaReferenceCleanup(t, "config_controls_schema")
	fixture.SchemaReferenceCleanup(t, "config_controls_private")
	var emitted []byte
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		if !b.PG {
			run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION pg_temp.config_source_temp (COPY=pg_catalog.simple)", 0))
		}
		for _, sql := range []string{
			"CREATE ROLE config_controls_owner",
			"CREATE ROLE config_restore_denied",
			"CREATE SCHEMA config_controls_schema AUTHORIZATION config_controls_owner",
			"CREATE SCHEMA config_controls_private",
			"REVOKE ALL ON SCHEMA config_controls_private FROM PUBLIC",
			"CREATE TEXT SEARCH DICTIONARY config_controls_private.config_controls_denied (TEMPLATE=pg_catalog.simple)",
			"CREATE TEXT SEARCH DICTIONARY public.config_controls_first (TEMPLATE=pg_catalog.simple,Accept=true)",
			"CREATE TEXT SEARCH DICTIONARY public.config_controls_second (TEMPLATE=pg_catalog.simple)",
			"CREATE TEXT SEARCH CONFIGURATION public.config_controls (COPY=pg_catalog.simple)",
			"ALTER TEXT SEARCH CONFIGURATION public.config_controls ALTER MAPPING FOR asciiword WITH public.config_controls_first,public.config_controls_second",
			"ALTER TEXT SEARCH CONFIGURATION public.config_controls DROP MAPPING FOR numword",
			"ALTER TEXT SEARCH DICTIONARY public.config_controls_first (Accept=false)",
			"ALTER TEXT SEARCH CONFIGURATION public.config_controls RENAME TO config_restored",
			"ALTER TEXT SEARCH CONFIGURATION public.config_restored SET SCHEMA config_controls_schema",
			"ALTER TEXT SEARCH CONFIGURATION config_controls_schema.config_restored OWNER TO config_controls_owner",
			"CREATE TABLE public.config_controls_refs(v regconfig DEFAULT 'config_controls_schema.config_restored'::regconfig)",
			"CREATE VIEW public.config_controls_view AS SELECT v FROM public.config_controls_refs",
		} {
			run(fixture.Exec(sql, 0))
		}
		run(fixture.Exec("INSERT INTO public.config_controls_refs DEFAULT VALUES", 1))
		for _, query := range ftsConfigurationCompoundSetup() {
			run(fixture.Exec(query, 0))
		}
		run(fixture.Exec(ftsConfigurationCompoundInsert(), 1))
		malformedSQL, malformedDiagnostic := ftsConfigurationCompoundMalformed()
		malformed := fixture.ErrorDiagnostic(malformedSQL, malformedDiagnostic)
		run(malformed)
		run(fixture.Prepared(malformed))
		check := func(p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
			t.Helper()
			steps := []fixture.Step{
				ftsConfigurationVector("SELECT to_tsvector(v,'The Fat Rats') AS normalized FROM public.config_controls_refs", "'fat':2 'rats':3 'the':1"),
				fixture.Select("SELECT v FROM public.config_controls_view", []string{"v"}, []string{"REGCONFIG"}, [][]any{{"config_controls_schema.config_restored"}}),
				fixture.Select("SELECT cfgowner='config_controls_owner'::regrole::oid AS owned,cfgparser::bigint FROM pg_ts_config WHERE oid='config_controls_schema.config_restored'::regconfig", []string{"owned", "cfgparser"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(3722)}}),
				fixture.Select("SELECT mapseqno,mapdict='public.config_controls_first'::regdictionary::oid AS first,mapdict='public.config_controls_second'::regdictionary::oid AS second FROM pg_ts_config_map WHERE mapcfg='config_controls_schema.config_restored'::regconfig AND maptokentype=1 ORDER BY mapseqno", []string{"mapseqno", "first", "second"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, false}, {int64(2), false, true}}),
				fixture.Select("SELECT maptokentype FROM pg_ts_config_map WHERE mapcfg='config_controls_schema.config_restored'::regconfig AND maptokentype=3", []string{"maptokentype"}, []string{"INT4"}, nil),
			}
			steps = append(steps, ftsConfigurationCompoundChecks()...)
			steps[0].CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
			for _, step := range steps {
				fixture.RunStep(t, p.Ctx, e, b.Recorder, step)
				fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Prepared(step))
			}
		}
		check(p, b, c)
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("missing actual native emitted stream")
			}
			for _, sql := range []string{"DROP VIEW public.config_controls_view", "DROP TABLE public.config_controls_refs,public.config_controls_compound", "DROP TYPE public.config_controls_pair,public.config_controls_span", "DROP DOMAIN public.config_controls_domain", "DROP SCHEMA config_controls_schema CASCADE", "DROP SCHEMA config_controls_private CASCADE", "DROP TEXT SEARCH DICTIONARY public.config_controls_first,public.config_controls_second"} {
				run(fixture.Exec(sql, 0))
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual emitted SQL replay", err)
			}
			check(p, b, c)
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(dump.Bytes())
		old, err := c.QueryContext(p.Ctx, "SELECT to_tsvector(v,'The Fat Rats') AS normalized FROM public.config_controls_refs")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		configurationVectorMetadata(t, old, false)
		metadata, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal("same-driver Restore", err)
		}
		check(p, b, c)
		if !old.Next() {
			t.Fatal("retained old Rows", old.Err())
		}
		var value string
		if err := old.Scan(&value); err != nil || value != "'fat':2 'rats':3 'the':1" {
			t.Fatal("retained old vector", value, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal(old.Err())
		}
		if metadata[0].DatabaseTypeName() != "TSVECTOR" {
			t.Fatal("retained result descriptor")
		}
		old.Close()
		redump := func(p *fixture.Pair) {
			t.Helper()
			var out bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil || !bytes.Equal(out.Bytes(), emitted) {
				t.Fatal("deterministic configuration redump", err, out.String())
			}
		}
		redump(p)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(ftsConfigurationMaps("pg_catalog.simple", ftsConfigurationSimpleMaps()))
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal("Reset/Restore", err)
		}
		check(p, b, c)
		run(fixture.Exec("CREATE TABLE config_controls_sentinel(v int)", 0))
		run(fixture.Exec("INSERT INTO config_controls_sentinel VALUES(42)", 1))
		retainedFailure, err := c.QueryContext(p.Ctx, "SELECT to_tsvector(v,'The Fat Rats') AS normalized FROM public.config_controls_refs")
		if err != nil {
			t.Fatal(err)
		}
		defer retainedFailure.Close()
		configurationVectorMetadata(t, retainedFailure, false)
		for _, tc := range []struct {
			suffix     string
			diagnostic fixture.Diagnostic
		}{
			{malformedSQL, malformedDiagnostic},
			{"ALTER TEXT SEARCH CONFIGURATION config_controls_schema.config_restored ALTER MAPPING FOR asciiword,not_a_token WITH simple;", fixture.ControlsReferenceDiagnostic("22023", `token type "not_a_token" does not exist`)},
			{"SET SESSION AUTHORIZATION config_restore_denied; ALTER TEXT SEARCH CONFIGURATION config_controls_schema.config_restored DROP MAPPING FOR asciiword;", fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema config_controls_schema")},
			{"SET SESSION AUTHORIZATION config_controls_owner; ALTER TEXT SEARCH CONFIGURATION config_controls_schema.config_restored ALTER MAPPING FOR asciiword WITH config_controls_private.config_controls_denied;", fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema config_controls_private")},
		} {
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(string(emitted)+tc.suffix)), &tc.diagnostic)
			check(p, b, c)
			run(fixture.Select("SELECT v FROM config_controls_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		}
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		reading, stop := context.WithCancel(p.Ctx)
		reader := &fixture.EnumCancelReader{Reader: bytes.NewReader(emitted), Cancel: stop}
		err = stuntgres.Restore(reading, p.Native, reader)
		stop()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("canceled reader published replacement", err)
		}
		check(p, b, c)
		run(fixture.Select("SELECT v FROM config_controls_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		if !retainedFailure.Next() {
			t.Fatal("failed/canceled Restore lost old Rows", retainedFailure.Err())
		}
		if err := retainedFailure.Scan(&value); err != nil || value != "'fat':2 'rats':3 'the':1" {
			t.Fatal("failed Restore changed old vector", value, err)
		}
		if retainedFailure.Next() || retainedFailure.Err() != nil {
			t.Fatal(retainedFailure.Err())
		}
		retainedFailure.Close()
		var sourceOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'config_controls_schema.config_restored'::regconfig::oid::bigint").Scan(&sourceOID); err != nil {
			t.Fatal(err)
		}
		fresh := fixture.NewFixture(t, false, true)
		fc, err := fresh.Memory.Conn(fresh.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		fb := fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}
		fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Exec("CREATE ROLE config_controls_owner", 0))
		fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Exec("CREATE ROLE config_restore_denied", 0))
		var collisionName string
		for i := 0; ; i++ {
			name := fmt.Sprintf("pg_temp.config_collision_%d", i)
			fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Exec("CREATE TEXT SEARCH CONFIGURATION "+name+" (PARSER=pg_catalog.default)", 0))
			var oid int64
			if err := fc.QueryRowContext(fresh.Ctx, "SELECT $1::regconfig::oid::bigint", name).Scan(&oid); err != nil {
				t.Fatal(err)
			}
			if oid > sourceOID {
				t.Fatalf("same-class collision overshot: %d > %d", oid, sourceOID)
			}
			if oid == sourceOID {
				collisionName = name
				break
			}
		}
		var collisionNamespace int64
		if err := fc.QueryRowContext(fresh.Ctx, "SELECT cfgnamespace::bigint FROM pg_ts_config WHERE oid=$1::regconfig::oid", collisionName).Scan(&collisionNamespace); err != nil || collisionNamespace <= 0 {
			t.Fatal("temporary collision namespace", collisionNamespace, err)
		}
		collision := func() {
			fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Select("SELECT c.oid::bigint,n.nspname LIKE 'pg_temp_%' AS temporary,c.cfgparser::bigint,c.cfgnamespace::bigint FROM pg_ts_config c JOIN pg_namespace n ON n.oid=c.cfgnamespace WHERE c.oid=$1::oid AND c.oid=$2::regconfig::oid", []string{"oid", "temporary", "cfgparser", "cfgnamespace"}, []string{"INT8", "BOOL", "INT8", "INT8"}, [][]any{{sourceOID, true, int64(3722), collisionNamespace}}, sourceOID, collisionName))
			fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, ftsConfigurationVector("SELECT to_tsvector($1::regconfig,$2::text) AS normalized", "", collisionName, "The Fat Rats"))
		}
		collision()
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal("fresh Driver remap", err)
		}
		var targetOID int64
		if err := fc.QueryRowContext(fresh.Ctx, "SELECT 'config_controls_schema.config_restored'::regconfig::oid::bigint").Scan(&targetOID); err != nil || targetOID == sourceOID {
			t.Fatal("imported address did not relocate", sourceOID, targetOID, err)
		}
		collision()
		check(fresh, fb, fc)
		redump(fresh)
		fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Exec("CREATE TABLE config_controls_sentinel(v int)", 0))
		fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Exec("INSERT INTO config_controls_sentinel VALUES(42)", 1))
		var destination bytes.Buffer
		if err := stuntgres.Dump(fresh.Ctx, fresh.Native, &destination); err != nil {
			t.Fatal("collision destination dump", err)
		}
		preserved := func() {
			t.Helper()
			collision()
			check(fresh, fb, fc)
			fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Select("SELECT v FROM config_controls_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
			var after bytes.Buffer
			if err := stuntgres.Dump(fresh.Ctx, fresh.Native, &after); err != nil || !bytes.Equal(destination.Bytes(), after.Bytes()) {
				t.Fatal("failed Restore changed collision destination", err, after.String())
			}
		}
		compoundRows, err := fc.QueryContext(fresh.Ctx, "SELECT a[1]::text,d::regconfig::text,(c).v::text,(c).d::regconfig::text,lower(r)::text,upper(r)::text FROM public.config_controls_compound")
		if err != nil {
			t.Fatal("retaining collision compound Rows", err)
		}
		defer compoundRows.Close()
		for _, tc := range []struct {
			suffix     string
			diagnostic fixture.Diagnostic
		}{
			{malformedSQL, malformedDiagnostic},
			{"ALTER TEXT SEARCH CONFIGURATION config_controls_schema.config_restored ALTER MAPPING FOR asciiword,not_a_token WITH simple;", fixture.ControlsReferenceDiagnostic("22023", `token type "not_a_token" does not exist`)},
			{"SET SESSION AUTHORIZATION config_restore_denied; ALTER TEXT SEARCH CONFIGURATION config_controls_schema.config_restored DROP MAPPING FOR asciiword;", fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema config_controls_schema")},
			{"SET SESSION AUTHORIZATION config_controls_owner; ALTER TEXT SEARCH CONFIGURATION config_controls_schema.config_restored ALTER MAPPING FOR asciiword WITH config_controls_private.config_controls_denied;", fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema config_controls_private")},
		} {
			fixture.AssertDiagnostic(t, stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewBufferString(string(emitted)+tc.suffix)), &tc.diagnostic)
			preserved()
		}
		collisionCanceled, collisionCancel := context.WithCancel(fresh.Ctx)
		collisionCancel()
		if err := stuntgres.Restore(collisionCanceled, fresh.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
			t.Fatal("pre-canceled collision Restore", err)
		}
		preserved()
		collisionReading, collisionStop := context.WithCancel(fresh.Ctx)
		collisionReader := &fixture.EnumCancelReader{Reader: bytes.NewReader(emitted), Cancel: collisionStop}
		err = stuntgres.Restore(collisionReading, fresh.Native, collisionReader)
		collisionStop()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("reader-canceled collision Restore", err)
		}
		preserved()
		if !compoundRows.Next() {
			t.Fatal("failed Restore lost retained compound Rows", compoundRows.Err())
		}
		var names [6]string
		if err := compoundRows.Scan(&names[0], &names[1], &names[2], &names[3], &names[4], &names[5]); err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			if name != "config_controls_schema.config_restored" {
				t.Fatal("failed Restore changed retained compound reference", names)
			}
		}
		if compoundRows.Next() || compoundRows.Err() != nil {
			t.Fatal("retained compound Rows termination", compoundRows.Err())
		}
		compoundRows.Close()
		fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Exec("ALTER TEXT SEARCH CONFIGURATION pg_catalog.simple DROP MAPPING FOR asciiword", 0))
		var seeded bytes.Buffer
		if err := stuntgres.Dump(fresh.Ctx, fresh.Native, &seeded); err != nil {
			t.Fatal("mutated seed dump", err)
		}
		if err := stuntgres.Reset(fresh.Ctx, fresh.Native); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, ftsConfigurationMaps("pg_catalog.simple", ftsConfigurationSimpleMaps()))
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(seeded.Bytes())); err != nil {
			t.Fatal("mutated seed replay", err)
		}
		fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, ftsConfigurationVector("SELECT to_tsvector('pg_catalog.simple'::regconfig,$1::text) AS normalized", "", "The Fat Rats"))
		check(fresh, fb, fc)
	})
}

// The SQL authorization suffix used by native Restore has the same ordinary
// DDL lookup contract as a caller issuing it directly or through Prepare.
func TestReferenceFTSConfigurationMappingNamespaceDenial(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"config_mapping_owner", "config_mapping_denied"})
	fixture.SchemaReferenceCleanup(t, "config_mapping_private")
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, sql := range []string{
			"CREATE ROLE config_mapping_owner",
			"CREATE ROLE config_mapping_denied",
			"CREATE SCHEMA config_mapping_private AUTHORIZATION config_mapping_owner",
			"CREATE TEXT SEARCH CONFIGURATION config_mapping_private.config_mapping_target (COPY=pg_catalog.simple)",
			"ALTER TEXT SEARCH CONFIGURATION config_mapping_private.config_mapping_target OWNER TO config_mapping_owner",
		} {
			run(fixture.Exec(sql, 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION config_mapping_denied", 0))
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		denied := fixture.ErrorDiagnostic(
			"ALTER TEXT SEARCH CONFIGURATION config_mapping_private.config_mapping_target DROP MAPPING FOR asciiword",
			fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema config_mapping_private"),
		)
		run(denied)
		run(fixture.Prepared(denied))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(ftsConfigurationVector("SELECT to_tsvector('config_mapping_private.config_mapping_target'::regconfig,'The Fat Rats') AS normalized", "'fat':2 'rats':3 'the':1"))
	})
}
