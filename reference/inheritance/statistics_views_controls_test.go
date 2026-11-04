package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func statisticsViewsSavedSkew(t *testing.T, s statisticsViewsScenario) {
	t.Helper()
	s.run(fixture.Select("SELECT attname::text,attnum::int4 FROM pg_attribute WHERE attrelid='sv_saved'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum"}, []string{"TEXT", "INT4"}, [][]any{{"a", int64(2)}, {"b", int64(3)}}))
	s.run(fixture.Select("SELECT stxkeys::text AS keys FROM pg_statistic_ext WHERE stxname='sv_saved_joint'", []string{"keys"}, []string{"TEXT"}, [][]any{{"3"}}))
	s.run(statisticsViewsSkew("pg_stats", "tablename='sv_saved' AND attname='a' AND NOT inherited", "{1,2}"))
	s.run(statisticsViewsSkew("pg_stats_ext_exprs", "statistics_name='sv_saved_expr' AND NOT inherited", "{2,3}"))
	s.run(fixture.Select("SELECT attnames,exprs,kinds,inherited,n_distinct::text AS nd FROM pg_stats_ext WHERE statistics_name='sv_saved_joint'", []string{"attnames", "exprs", "kinds", "inherited", "nd"}, []string{"_NAME", "_TEXT", "_CHAR", "BOOL", "TEXT"}, [][]any{{"{b}", `{"(a + 1)"}`, "{d,f,m,e}", false, `{"3, -1": 3}`}}))
	statisticsViewsMCV(t, s, "sv_saved_joint", statisticsViewsMCVItem{[]any{"10", "2"}, .7, .49}, statisticsViewsMCVItem{[]any{"20", "3"}, .1, .01}, statisticsViewsMCVItem{[]any{nil, nil}, .2, .04})
}

func statisticsViewsControlScenario(t *testing.T, s statisticsViewsScenario, conn *sql.Conn, backend fixture.Backend) statisticsViewsScenario {
	t.Helper()
	run := func(step fixture.Step) {
		step.Prepared = s.prepared
		fixture.RunStep(t, s.pair.Ctx, conn, backend.Recorder, step)
	}
	return statisticsViewsScenario{ndistinctScenario{run, backend.PG, conn, s.pair, s.prepared}, backend}
}

func statisticsViewsNativeControls(t *testing.T, s statisticsViewsScenario) {
	t.Helper()
	// A real independent Driver owns the stale source. Oracle discovery can
	// produce its genuine emitted SQL without reading any missing native view.
	driver := stuntgres.NewDriver()
	connector, err := driver.OpenConnector(t.Name() + "-views-source")
	if err != nil {
		t.Fatal(err)
	}
	source := sql.OpenDB(connector)
	defer source.Close()
	population := strings.TrimSuffix(strings.Repeat("(0,1,10),", 14)+strings.Repeat("(0,2,20),", 2)+strings.Repeat("(0,NULL,NULL),", 4), ",")
	for _, query := range []string{
		"CREATE TABLE sv_saved(spare INT,a INT,b INT)",
		"INSERT INTO sv_saved VALUES" + population,
		"CREATE STATISTICS sv_saved_expr ON (a+1) FROM sv_saved",
		"CREATE STATISTICS sv_saved_joint ON b,(a+1) FROM sv_saved",
		"ANALYZE sv_saved",
		"UPDATE sv_saved SET a=9,b=90",
		"ALTER TABLE sv_saved DROP COLUMN spare",
	} {
		if _, err := source.ExecContext(s.pair.Ctx, query); err != nil {
			t.Fatal("source operation", query, err)
		}
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(s.pair.Ctx, source, &dump); err != nil {
		t.Fatal(err)
	}
	if dump.Len() == 0 {
		t.Fatal("empty real statistics-view dump")
	}
	if s.pg {
		conn := fixture.SequenceCoreConn(t, s.pair.Ctx, s.backend.DB)
		defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		if _, err := conn.ExecContext(s.pair.Ctx, dump.String()); err != nil {
			t.Fatal("actual emitted statistics definition replay", err)
		}
		if _, err := conn.ExecContext(s.pair.Ctx, "SET SESSION AUTHORIZATION DEFAULT"); err != nil {
			t.Fatal(err)
		}
		pg := statisticsViewsControlScenario(t, s, conn, s.backend)
		pg.run(statisticsViewsCount("pg_stats_ext", "tablename='sv_saved'", 0))
		pg.run(statisticsViewsCount("pg_stats", "tablename='sv_saved'", 0))
		pg.run(fixture.Select("SELECT statistics_name,expr,inherited,null_frac FROM pg_stats_ext_exprs WHERE tablename='sv_saved' ORDER BY statistics_name", []string{"statistics_name", "expr", "inherited", "null_frac"}, []string{"NAME", "TEXT", "BOOL", "FLOAT4"}, [][]any{{"sv_saved_expr", "(a + 1)", nil, nil}, {"sv_saved_joint", "(a + 1)", nil, nil}}))
		pg.exec("ANALYZE sv_saved")
		pg.run(statisticsViewsUniform("sv_saved_expr", "{10}"))
		statisticsViewsMCV(t, pg, "sv_saved_joint", statisticsViewsMCVItem{[]any{"90", "10"}, 1, 1})
		return
	}

	// Retained destination TEMP facts belong to a real live physical session,
	// not to the imported source snapshot.
	s.exec("CREATE TEMP TABLE sv_keep(a INT,b INT)", "CREATE STATISTICS pg_temp.sv_keep_s ON (a+1) FROM sv_keep")
	keep := strings.TrimSuffix(strings.Repeat("(1,10),", 14)+strings.Repeat("(2,20),", 2)+strings.Repeat("(NULL,NULL),", 4), ",")
	s.run(fixture.Exec("INSERT INTO sv_keep VALUES"+keep, 20))
	s.exec("ANALYZE sv_keep")
	old, closeRows := s.query(t, "SELECT most_common_vals,most_common_freqs FROM pg_stats_ext_exprs WHERE statistics_name='sv_keep_s'")
	defer closeRows()
	if err := stuntgres.Reset(s.pair.Ctx, s.pair.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(s.pair.Ctx, s.pair.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	statisticsViewsReadHeldSkew(t, s, old)
	s.run(statisticsViewsSkew("pg_stats_ext_exprs", "statistics_name='sv_keep_s' AND NOT inherited", "{2,3}"))
	statisticsViewsSavedSkew(t, s)
	s.run(fixture.Select("SELECT min(a),max(a),count(*) FROM sv_saved", []string{"min", "max", "count"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(9), int64(9), int64(20)}}))
	s.exec("CREATE TABLE sv_sentinel(v INT)")
	s.run(fixture.Exec("INSERT INTO sv_sentinel VALUES(47)", 1))
	unchanged := func() {
		t.Helper()
		s.run(fixture.Select("SELECT v FROM sv_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(47)}}))
		statisticsViewsSavedSkew(t, s)
		s.run(statisticsViewsSkew("pg_stats_ext_exprs", "statistics_name='sv_keep_s' AND NOT inherited", "{2,3}"))
	}
	canceled, cancel := context.WithCancel(s.pair.Ctx)
	cancel()
	if err := stuntgres.Restore(canceled, s.pair.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled real restore", err)
	}
	unchanged()
	if err := stuntgres.Restore(s.pair.Ctx, s.pair.Native, strings.NewReader(dump.String()+"\nCREATE TABLE broken(")); err == nil {
		t.Fatal("malformed restore accepted")
	}
	unchanged()
	// Corrupt an actual emitted expression record in place, never a substitute
	// handwritten payload or duplicate tail record.
	found := false
	for _, line := range strings.Split(dump.String(), "\n") {
		const prefix = "-- stuntgres-ndistinct-v1 "
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, prefix))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(raw, []byte(`"expressions"`)) {
			continue
		}
		corrupt := bytes.Replace(raw, []byte(`"null_count":4`), []byte(`"null_count":999999`), 1)
		if bytes.Equal(raw, corrupt) {
			t.Fatal("actual expression record lacks expected four NULLs")
		}
		malformed := strings.Replace(dump.String(), line, prefix+base64.StdEncoding.EncodeToString(corrupt), 1)
		if err := stuntgres.Restore(s.pair.Ctx, s.pair.Native, strings.NewReader(malformed)); err == nil {
			t.Fatal("invalid collected expression accepted")
		}
		found = true
		unchanged()
		break
	}
	if !found {
		t.Fatal("real dump omitted expression semantic state")
	}

	// A second genuine Driver forces destination identity allocation rather
	// than accidentally adopting the source's retained descriptors.
	otherDriver := stuntgres.NewDriver()
	otherConnector, err := otherDriver.OpenConnector(t.Name() + "-views-remint")
	if err != nil {
		t.Fatal(err)
	}
	other := sql.OpenDB(otherConnector)
	defer other.Close()
	recorded, recorder := fixture.RecordedNativePool(t, otherConnector)
	if _, err := other.ExecContext(s.pair.Ctx, "CREATE TABLE collision(v INT); CREATE TYPE collision_type AS ENUM('x')"); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(s.pair.Ctx, other, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	otherConn, err := recorded.Conn(s.pair.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer otherConn.Close()
	reminted := statisticsViewsControlScenario(t, s, otherConn, fixture.Backend{Name: "reminted", DB: recorded, Recorder: recorder})
	statisticsViewsSavedSkew(t, reminted)
	reminted.run(statisticsViewsCount("pg_stats_ext_exprs", "statistics_name='sv_keep_s'", 0))
	reminted.exec("ANALYZE sv_saved")
	reminted.run(statisticsViewsUniform("sv_saved_expr", "{10}"))
	statisticsViewsMCV(t, reminted, "sv_saved_joint", statisticsViewsMCVItem{[]any{"90", "10"}, 1, 1})
	s.exec("ANALYZE sv_saved")
	s.run(statisticsViewsUniform("sv_saved_expr", "{10}"))
	statisticsViewsMCV(t, s, "sv_saved_joint", statisticsViewsMCVItem{[]any{"90", "10"}, 1, 1})
}
