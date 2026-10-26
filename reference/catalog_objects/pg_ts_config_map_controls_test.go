package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func pgTSConfigMapIdentities(t *testing.T, p *fixture.Pair, e fixture.Executor) (int64, int64, int64) {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, "SELECT 'public.ptscm_live'::regconfig::oid::bigint,'public.ptscm_first'::regdictionary::oid::bigint,'public.ptscm_second'::regdictionary::oid::bigint")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing generated identities", rows.Err())
	}
	var config, first, second int64
	if err := rows.Scan(&config, &first, &second); err != nil {
		t.Fatal(err)
	}
	if config <= 0 || first <= 0 || second <= 0 || first == second {
		t.Fatal("genuine distinct identities", config, first, second)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("identity trailing rows", rows.Err())
	}
	return config, first, second
}

func pgTSConfigMapConsume(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	got := [][]any{}
	for rows.Next() {
		var cfg, token, sequence, dictionary int64
		if err := rows.Scan(&cfg, &token, &sequence, &dictionary); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{cfg, token, sequence, dictionary})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retained mappings: got %v, want %v", got, want)
	}
}

func pgTSConfigMapLifecycle(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, q := range []string{
		"CREATE TEXT SEARCH DICTIONARY public.ptscm_first (TEMPLATE=pg_catalog.simple)",
		"CREATE TEXT SEARCH DICTIONARY public.ptscm_second (TEMPLATE=pg_catalog.simple)",
		"CREATE TEXT SEARCH CONFIGURATION public.ptscm_live (PARSER=pg_catalog.default)",
		"ALTER TEXT SEARCH CONFIGURATION public.ptscm_live ADD MAPPING FOR asciiword WITH public.ptscm_first,public.ptscm_second",
	} {
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(q, 0))
	}
	config, first, second := pgTSConfigMapIdentities(t, p, conn)
	original := [][]any{{config, int64(1), int64(1), first}, {config, int64(1), int64(2), second}}
	stmt, err := conn.PrepareContext(p.Ctx, pgTSConfigMapProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: pgTSConfigMapProbe}
	check := func(cfg int64, want [][]any) {
		t.Helper()
		s := pgTSConfigMapStep(b, pgTSConfigMapProbe, want, cfg)
		fixture.PgDescriptionExecutorRows(t, p, b, held, s)
		pgTSConfigBoth(t, p, b, conn, s)
	}
	check(config, original)
	var dump bytes.Buffer
	var old *sql.Rows
	var saved []*sql.ColumnType
	if !b.PG {
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		old, err = conn.QueryContext(p.Ctx, pgTSConfigMapProbe, config)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		saved = pgTSConfigMapMetadata(t, old, false)
	}
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.ptscm_live ALTER MAPPING FOR asciiword WITH public.ptscm_second", 0))
	check(config, [][]any{{config, int64(1), int64(1), second}})
	if b.PG {
		return
	} // Never retain an unconsumed pq stream across native controls.
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	check(config, [][]any{})
	pgTSConfigBoth(t, p, b, conn, pgTSConfigMapStep(b, pgTSConfigMapProbe, pgTSConfigMapStock(), int64(3748)))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	restoredConfig, restoredFirst, restoredSecond := pgTSConfigMapIdentities(t, p, conn)
	check(restoredConfig, [][]any{{restoredConfig, int64(1), int64(1), restoredFirst}, {restoredConfig, int64(1), int64(2), restoredSecond}})
	pgTSConfigMapConsume(t, old, original)
	pgTSConfigMapSavedMetadata(t, saved, false)
	// Existing FTSConfigurationControls independently covers logical redump/replay.
}

func TestReferencePGTSConfigMapLifecycle(t *testing.T) {
	pgTSConfigScenario(t, pgTSConfigMapLifecycle)
}
