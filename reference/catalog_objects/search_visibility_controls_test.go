package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func searchVisibilityLinks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, ns := range []string{"sv_a", "sv_b"} {
		query := "SELECT c.cfgparser=p.oid AS parser_link,d.dicttemplate=t.oid AS template_link,m.mapdict=d.oid AS mapping_link" +
			" FROM pg_catalog.pg_ts_config c JOIN pg_catalog.pg_namespace n ON n.oid=c.cfgnamespace" +
			" JOIN pg_catalog.pg_ts_parser p ON p.prsnamespace=n.oid AND p.prsname='v'" +
			" JOIN pg_catalog.pg_ts_dict d ON d.dictnamespace=n.oid AND d.dictname='v'" +
			" JOIN pg_catalog.pg_ts_template t ON t.tmplnamespace=n.oid AND t.tmplname='v'" +
			" JOIN pg_catalog.pg_ts_config_map m ON m.mapcfg=c.oid WHERE n.nspname=$1 AND c.cfgname='v'"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(query,
			[]string{"parser_link", "template_link", "mapping_link"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}, ns))
	}
}

func TestReferenceSearchVisibilityControls(t *testing.T) {
	var emitted []byte
	searchVisibilityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		searchVisibilitySetup(t, p, b, c)
		searchVisibilityStored(t, p, b, c)
		check := func(p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			searchVisibilityMatrix(t, p, b, c, true)
			searchVisibilityLinks(t, p, b, c)
			for i, k := range searchVisibilityClasses {
				searchVisibilityCheckReject(t, p, b, c, i, k)
			}
		}
		check(p, b, c)
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("missing actual native emitted SQL")
			}
			for _, ns := range []string{"sv_a", "sv_b", "sv_off"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP SCHEMA "+ns+" CASCADE", 0))
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
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path=sv_a,sv_b", 0))
		old, err := c.QueryContext(p.Ctx, "SELECT visible FROM sv_off.view_0 ORDER BY schema_name")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		types, err := old.ColumnTypes()
		if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "BOOL" {
			t.Fatal(types, err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal("same driver Restore", err)
		}
		visibilityInquiryRows(t, old, nil, "visible", []any{true, false})
		check(p, b, c)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal("Reset Restore", err)
		}
		check(p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE sv_off.sentinel(id integer)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO sv_off.sentinel VALUES(42)", 1))
		identities := make([][2]int64, len(searchVisibilityClasses))
		for i, k := range searchVisibilityClasses {
			identities[i] = [2]int64{searchVisibilityDiscover(t, p, c, k, "sv_a", "v"), searchVisibilityDiscover(t, p, c, k, "sv_b", "v")}
		}
		invalidPrefix := string(emitted) + "CREATE TABLE sv_off.invalid(id oid); INSERT INTO sv_off.invalid VALUES("
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(invalidPrefix+"'bad');"))
		want := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type oid: "bad"`)
		want.Position = fixture.Position(utf8.RuneCountInString(invalidPrefix) + 1)
		fixture.AssertDiagnostic(t, err, &want)
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		reading, stop := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(reading, p.Native, &fixture.EnumCancelReader{Reader: bytes.NewReader(emitted), Cancel: stop})
		stop()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("reading cancellation", err)
		}
		check(p, b, c)
		for i, k := range searchVisibilityClasses {
			a := searchVisibilityDiscover(t, p, c, k, "sv_a", "v")
			z := searchVisibilityDiscover(t, p, c, k, "sv_b", "v")
			if a != identities[i][0] || z != identities[i][1] {
				t.Fatal("failed replacement changed identity", k.fn, a, z, identities[i])
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM sv_off.sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(42)}}))

		connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "-fresh")
		if err != nil {
			t.Fatal(err)
		}
		native := sql.OpenDB(connector)
		defer native.Close()
		pool, recorder := fixture.RecordedNativePool(t, connector)
		defer pool.Close()
		fresh := &fixture.Pair{Ctx: p.Ctx, Native: native, Memory: pool, MemoryRecord: recorder}
		fc, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		fb := fixture.Backend{DB: pool, Recorder: recorder}
		// TEMP survives replacement. Occupy the source parser's actual address
		// in that preserved namespace so Restore must remint the restored parser.
		parser := searchVisibilityClasses[2]
		sourceOID := identities[2][0]
		var collision string
		for i := 0; ; i++ {
			collision = fmt.Sprintf("search_visibility_collision_%d", i)
			fixture.RunStep(t, p.Ctx, fc, recorder, fixture.Exec(searchVisibilityCreate(2, "pg_temp."+collision), 0))
			var oid int64
			if err := fc.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_catalog.pg_ts_parser WHERE prsnamespace=pg_catalog.pg_my_temp_schema() AND prsname=$1", collision).Scan(&oid); err != nil {
				t.Fatal(err)
			}
			if oid > sourceOID {
				t.Fatalf("parser collision overshot source identity: %d > %d", oid, sourceOID)
			}
			if oid == sourceOID {
				break
			}
		}
		if err := stuntgres.Restore(p.Ctx, native, bytes.NewReader(emitted)); err != nil {
			t.Fatal("fresh driver Restore", err)
		}
		if restored := searchVisibilityDiscover(t, fresh, fc, parser, "sv_a", "v"); restored == sourceOID {
			t.Fatal("fresh destination parser identity was not displaced", sourceOID)
		}
		check(fresh, fb, fc)
		changed := false
		for i, k := range searchVisibilityClasses {
			if searchVisibilityDiscover(t, fresh, fc, k, "sv_a", "v") != identities[i][0] {
				changed = true
			}
		}
		if !changed {
			t.Fatal("fresh destination did not displace source identities")
		}
		fixture.RunStep(t, p.Ctx, fc, recorder, fixture.Select(
			"SELECT oid::bigint FROM pg_catalog.pg_ts_parser WHERE prsnamespace=pg_catalog.pg_my_temp_schema() AND prsname=$1",
			[]string{"oid"}, []string{"INT8"}, [][]any{{sourceOID}}, collision))
		searchVisibilityRoutes(t, fresh, fb, fc, parser, nil, sourceOID, false)
	})
}
