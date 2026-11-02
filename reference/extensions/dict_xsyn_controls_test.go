package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const xsynOwnerDeniedSQL = "ALTER TEXT SEARCH DICTIONARY public.xsyn_custom (keeporig=false)"
const xsynSchemaDeniedSQL = "ALTER TEXT SEARCH CONFIGURATION xsyn_addon.xsyn_guard DROP MAPPING FOR asciiword"

func TestReferenceDictXsynControls(t *testing.T) {
	for _, mutated := range []bool{false, true} {
		name := "absent_source"
		if mutated {
			name = "configured_moved_detached"
		}
		t.Run(name, func(t *testing.T) {
			var emitted []byte
			var withoutComments []byte
			xsynCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				if !b.PG {
					xsynExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY pg_temp.xsyn_source (TEMPLATE=pg_catalog.simple)", 0)
				}
				n := xsynControlSetup(t, p, b, c, mutated)
				check := func() { xsynControlState(t, p, b, c, n, mutated) }
				check()
				xsynControlAuthorization(t, p, b, c, check)
				// Both ordinary and installed/detached comments can be removed.
				// Replaying actual native SQL must not resurrect the released
				// dictionary's default comment at CREATE EXTENSION.
				for _, dictionary := range []string{n.member, "public.xsyn_custom"} {
					xsynExec(t, p, b, c, "COMMENT ON TEXT SEARCH DICTIONARY "+dictionary+" IS ' '", 0)
					xsynSelect(t, p, b, c, "SELECT obj_description($1::regdictionary::oid,'pg_ts_dict') AS comment", []string{"comment"}, []string{"TEXT"}, [][]any{{" "}}, dictionary)
					xsynExec(t, p, b, c, "COMMENT ON TEXT SEARCH DICTIONARY "+dictionary+" IS ''", 0)
					xsynSelect(t, p, b, c, "SELECT obj_description($1::regdictionary::oid,'pg_ts_dict') AS comment", []string{"comment"}, []string{"TEXT"}, [][]any{{nil}}, dictionary)
					xsynExec(t, p, b, c, "COMMENT ON TEXT SEARCH DICTIONARY "+dictionary+" IS NULL", 0)
				}
				if b.PG {
					if len(withoutComments) == 0 {
						t.Fatal("actual native comment-removal SQL required")
					}
					xsynControlCleanup(t, p, b, c, n, mutated)
					if _, err := c.ExecContext(p.Ctx, string(withoutComments)); err != nil {
						t.Fatal("actual comment-removal replay", err)
					}
					if !mutated {
						n.helperOwner = "postgres"
					}
				} else {
					withoutComments = []byte(fixture.ExtensionDump(t, p.Ctx, p.Native))
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(withoutComments)); err != nil {
						t.Fatal(err)
					}
				}
				for _, dictionary := range []string{n.member, "public.xsyn_custom"} {
					xsynSelect(t, p, b, c, "SELECT obj_description($1::regdictionary::oid,'pg_ts_dict') AS comment", []string{"comment"}, []string{"TEXT"}, [][]any{{nil}}, dictionary)
					xsynSelect(t, p, b, c, "SELECT description FROM pg_description WHERE classoid=3600 AND objoid=$1::regdictionary::oid", []string{"description"}, []string{"TEXT"}, nil, dictionary)
				}
				xsynExec(t, p, b, c, "COMMENT ON TEXT SEARCH DICTIONARY "+n.member+" IS 'retained xsyn member'", 0)
				xsynExec(t, p, b, c, "COMMENT ON TEXT SEARCH DICTIONARY public.xsyn_custom IS 'custom group'", 0)
				check()
				if b.PG {
					if len(emitted) == 0 {
						t.Fatal("actual native-emitted SQL required; exercise controls after native implementation")
					}
					xsynControlCleanup(t, p, b, c, n, mutated)
					if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
						t.Fatal("actual native SQL replay", err)
					}
					if !mutated {
						n.helperOwner = "postgres"
					}
					check()
					xsynControlAuthorization(t, p, b, c, check)
					return
				}
				emitted = []byte(fixture.ExtensionDump(t, p.Ctx, p.Native))
				if bytes.Count(emitted, []byte(`CREATE EXTENSION "dict_xsyn"`)) != 1 || bytes.Contains(emitted, []byte("CREATE TEXT SEARCH TEMPLATE")) {
					t.Fatal("provider must replay once as genuine extension, not loose callbacks/template", string(emitted))
				}
				source := dictIntID(t, p, c, "SELECT 'xsyn_custom'::regdictionary::oid::bigint")
				old, err := c.QueryContext(p.Ctx, xsynLexizeSQL, "xsyn_custom", "supernova")
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				ct, err := old.ColumnTypes()
				if err != nil || len(ct) != 1 || ct[0].DatabaseTypeName() != "_TEXT" {
					t.Fatal("old source metadata", ct, err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
					t.Fatal(err)
				}
				check()
				xsynControlAuthorization(t, p, b, c, check)
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				xsynSelect(t, p, b, c, "SELECT extname FROM pg_extension WHERE extname='dict_xsyn'", []string{"extname"}, []string{"NAME"}, nil)
				xsynSelect(t, p, b, c, "SELECT dictname FROM pg_ts_dict WHERE dictname='xsyn_custom'", []string{"dictname"}, []string{"NAME"}, nil)
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
					t.Fatal(err)
				}
				check()
				xsynControlAuthorization(t, p, b, c, check)
				if got := fixture.ExtensionDump(t, p.Ctx, p.Native); got != string(emitted) {
					t.Fatal("same-target redump changed")
				}
				xsynControlFailures(t, p, b, c, emitted, check)
				fresh := fixture.NewFixture(t, false, true)
				fc, err := fresh.Memory.Conn(fresh.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer fc.Close()
				fb := fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}
				for _, q := range []string{"CREATE ROLE xsyn_owner", "CREATE ROLE xsyn_denied"} {
					xsynExec(t, fresh, fb, fc, q, 0)
				}
				collision := xsynCollision(t, fresh, fb, fc, source)
				if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil {
					t.Fatal(err)
				}
				target := dictIntID(t, fresh, fc, "SELECT 'xsyn_custom'::regdictionary::oid::bigint")
				if target == source {
					t.Fatal("class-3600 collision did not remap imported dictionary", source)
				}
				freshCheck := func() { collision(); xsynControlState(t, fresh, fb, fc, n, mutated) }
				freshCheck()
				xsynControlAuthorization(t, fresh, fb, fc, freshCheck)
				xsynControlFailures(t, fresh, fb, fc, emitted, freshCheck)
				if !old.Next() {
					t.Fatal("retained source row missing", old.Err())
				}
				var value any
				if err := old.Scan(&value); err != nil || value != xsynAll {
					t.Fatal("retained source value", value, err)
				}
				if old.Next() || old.Err() != nil {
					t.Fatal("retained source completion", old.Err())
				}
			})
		})
	}
}

func xsynControlAuthorization(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check func()) {
	t.Helper()
	const state = "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint,dictinitoption FROM pg_ts_dict WHERE oid='xsyn_custom'::regdictionary::oid"
	const maps = "SELECT mapcfg::bigint,maptokentype,mapseqno,mapdict::bigint FROM pg_ts_config_map WHERE mapcfg='xsyn_cfg'::regconfig::oid OR mapcfg='xsyn_addon.xsyn_guard'::regconfig::oid ORDER BY mapcfg,maptokentype,mapseqno"
	before, mappings := dictIntRows(t, p, c, state), dictIntRows(t, p, c, maps)
	var principal string
	if err := c.QueryRowContext(p.Ctx, "SELECT session_user").Scan(&principal); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ query, message string }{
		{xsynOwnerDeniedSQL, "must be owner of text search dictionary public.xsyn_custom"},
		{xsynSchemaDeniedSQL, "permission denied for schema xsyn_addon"},
		{"COMMENT ON TEXT SEARCH DICTIONARY public.xsyn_custom IS 'denied'", "must be owner of text search dictionary public.xsyn_custom"},
		{"COMMENT ON TEXT SEARCH DICTIONARY xsyn_addon.absent IS 'denied'", "permission denied for schema xsyn_addon"},
	} {
		for _, prepared := range []bool{false, true} {
			func() {
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
					defer cancel()
					if _, err := c.ExecContext(ctx, "SET SESSION AUTHORIZATION "+fixture.ExtensionTempIdentifier(principal)); err != nil {
						t.Error("restore principal", err)
					}
				}()
				xsynExec(t, p, b, c, "SET SESSION AUTHORIZATION xsyn_denied", 0)
				s := fixture.ErrorDiagnostic(tc.query, fixture.ControlsReferenceDiagnostic("42501", tc.message))
				s.Prepared, s.ErrorStage = prepared, "execute"
				xsynRun(t, p, b, c, s)
			}()
			dictIntUnchanged(t, p, c, before, state)
			dictIntUnchanged(t, p, c, mappings, maps)
			check()
		}
	}
}

func xsynControlFailures(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, emitted []byte, check func()) {
	t.Helper()
	xsynExec(t, p, b, c, "CREATE TABLE xsyn_sentinel(v int)", 0)
	xsynExec(t, p, b, c, "INSERT INTO xsyn_sentinel VALUES(42)", 1)
	old, err := c.QueryContext(p.Ctx, "SELECT v FROM xsyn_sentinel")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	ct, err := old.ColumnTypes()
	if err != nil || len(ct) != 1 || ct[0].DatabaseTypeName() != "INT4" {
		t.Fatal("retained sentinel metadata", ct, err)
	}
	before := fixture.ExtensionDump(t, p.Ctx, p.Native)
	unchanged := func() {
		check()
		xsynSelect(t, p, b, c, "SELECT v FROM xsyn_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
		if got := fixture.ExtensionDump(t, p.Ctx, p.Native); got != before {
			t.Fatal("rejected Restore changed destination")
		}
	}
	for _, suffix := range []string{
		"ALTER TEXT SEARCH DICTIONARY xsyn_custom (rules=xsyn_sample,unknown=1);",
		"ALTER TEXT SEARCH DICTIONARY xsyn_custom OWNER TO xsyn_missing_owner;",
	} {
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(string(emitted)+suffix)); err == nil {
			t.Fatal("late-invalid Restore published")
		}
		unchanged()
	}
	for _, tc := range []struct{ query, message string }{
		{xsynOwnerDeniedSQL, "must be owner of text search dictionary public.xsyn_custom"},
		{xsynSchemaDeniedSQL, "permission denied for schema xsyn_addon"},
	} {
		err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(string(emitted)+"SET SESSION AUTHORIZATION xsyn_denied; "+tc.query+";"))
		want := fixture.ControlsReferenceDiagnostic("42501", tc.message)
		fixture.AssertDiagnostic(t, err, &want)
		unchanged()
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-canceled Restore", err)
	}
	unchanged()
	reading, stop := context.WithCancel(p.Ctx)
	reader := &fixture.EnumCancelReader{Reader: bytes.NewReader(emitted), Cancel: stop}
	err = stuntgres.Restore(reading, p.Native, reader)
	stop()
	if !errors.Is(err, context.Canceled) {
		t.Fatal("reader-canceled Restore", err)
	}
	unchanged()
	if !old.Next() {
		t.Fatal("retained sentinel row missing", old.Err())
	}
	var value int64
	if err := old.Scan(&value); err != nil || value != 42 {
		t.Fatal("retained sentinel", value, err)
	}
	if old.Next() || old.Err() != nil {
		t.Fatal("retained sentinel completion", old.Err())
	}
}
