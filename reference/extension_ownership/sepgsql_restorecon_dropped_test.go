package reference_test

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func restoreconDroppedSetup(run sepgsqlRun) {
	run(fixture.Exec("CREATE TABLE restorecon_dropped(keep integer, gone text, tail integer)", 0))
	run(fixture.Exec("INSERT INTO restorecon_dropped VALUES(7,'retired',9)", 1))
	run(fixture.Exec("ALTER TABLE restorecon_dropped DROP COLUMN gone", 0))
}

func restoreconDroppedFacts(t *testing.T, p *fixture.Pair, db fixture.Executor, run sepgsqlRun, active bool) {
	t.Helper()
	before := sepgsqlRestoreconLabels(t, p, db)
	assertUnchanged := func(prepared bool) {
		restoreconUnchanged(t, p, db, before)
		data := fixture.Select("SELECT keep,tail FROM restorecon_dropped", []string{"keep", "tail"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}})
		data.Prepared = prepared
		run(data)
	}
	for _, prepared := range []bool{false, true} {
		step := fixture.Select("SELECT attnum,attname::text,attisdropped FROM pg_attribute WHERE attrelid='restorecon_dropped'::regclass AND attnum>0 ORDER BY attnum",
			[]string{"attnum", "attname", "attisdropped"}, []string{"INT2", "TEXT", "BOOL"}, [][]any{{int64(1), "keep", false}, {int64(2), "........pg.dropped.2........", true}, {int64(3), "tail", false}})
		step.Prepared = prepared
		run(step)
		step = fixture.Select("SELECT keep,tail FROM restorecon_dropped", []string{"keep", "tail"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}})
		step.Prepared = prepared
		run(step)
		step = fixture.ImplicitError(`SELECT "........pg.dropped.2........" FROM restorecon_dropped`, fixture.Diagnostic{
			Code: "42703", Message: `column "........pg.dropped.2........" does not exist`, Position: "8",
		})
		step.Prepared = prepared
		run(step)
		assertUnchanged(prepared)
		query := `SECURITY LABEL FOR selinux ON COLUMN restorecon_dropped."........pg.dropped.2........" IS '` + sepgsqlS + `'`
		step = fixture.ImplicitError(query, fixture.Diagnostic{
			Code: "42703", Message: `column "........pg.dropped.2........" of relation "restorecon_dropped" does not exist`,
		})
		if !active {
			// Provider lookup precedes column resolution on the actual inactive
			// native and pinned PostgreSQL backends.
			step = fixture.ImplicitError(query, fixture.Diagnostic{Code: "22023", Message: "security label provider \"selinux\" is not loaded"})
		}
		step.Prepared = prepared
		run(step)
		assertUnchanged(prepared)
	}
}

func restoreconCall(run sepgsqlRun, name string, profile any, prepared bool) {
	step := sepgsqlBool("SELECT "+name+"($1) AS restored", "restored", true)
	step.Args, step.Prepared = []any{profile}, prepared
	run(step)
}

func restoreconDroppedLabels(run sepgsqlRun, tombstone string) {
	rows := [][]any{{int64(-6), sepgsqlP}, {int64(-5), sepgsqlP}, {int64(-4), sepgsqlP}, {int64(-3), sepgsqlP}, {int64(-2), sepgsqlP}, {int64(-1), sepgsqlP}, {int64(0), sepgsqlP}, {int64(1), sepgsqlP}, {int64(2), tombstone}, {int64(3), sepgsqlP}}
	for _, prepared := range []bool{false, true} {
		step := fixture.Select("SELECT objsubid,label FROM pg_seclabel WHERE classoid='pg_class'::regclass AND objoid='restorecon_dropped'::regclass AND provider='selinux' ORDER BY objsubid", []string{"objsubid", "label"}, []string{"INT4", "TEXT"}, rows)
		step.Prepared = prepared
		run(step)
	}
}

func restoreconDroppedConfig(config *stuntgres.SepgsqlSimulationConfig) {
	config.Policy.NamedSpecs["tombstone"] = "db_column *.public.restorecon_dropped.\"........pg.dropped.2........\" " + sepgsqlS + "\n" + config.Policy.DefaultSpec
}

func restoreconInactive(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
	restoreconDroppedSetup(run)
	restoreconDroppedFacts(t, p, c, run, false)
	for _, prepared := range []bool{false, true} {
		step := fixture.ImplicitError("SECURITY LABEL FOR selinux ON TABLE restorecon_dropped IS '"+sepgsqlS+"'", fixture.Diagnostic{Code: "22023", Message: "security label provider \"selinux\" is not loaded"})
		step.Prepared = prepared
		run(step)
	}
	run(sepgsqlCount("SELECT count(*) FROM pg_proc WHERE proname='sepgsql_restorecon'", 0))
}

func restoreconTamper(t *testing.T, image string, mutate func(map[string]any)) string {
	t.Helper()
	const marker = "-- stuntgres-sepgsql-simulation-v1 "
	start := strings.Index(image, marker)
	if start < 0 {
		t.Fatal("missing actual simulation requirement")
	}
	end := strings.IndexByte(image[start:], '\n') + start
	data, err := base64.StdEncoding.DecodeString(image[start+len(marker) : end])
	if err != nil {
		t.Fatal(err)
	}
	var requirement map[string]any
	if err := json.Unmarshal(data, &requirement); err != nil {
		t.Fatal(err)
	}
	mutate(requirement)
	data, err = json.Marshal(requirement)
	if err != nil {
		t.Fatal(err)
	}
	return image[:start] + marker + base64.StdEncoding.EncodeToString(data) + image[end:]
}

func TestReferenceSepgsqlSimulationRestoreconDroppedColumns(t *testing.T) {
	sepgsqlCase(t, restoreconInactive, func(config *stuntgres.SepgsqlSimulationConfig) {
		restoreconDroppedConfig(config)
		config.Policy.NamedSpecs["denied-tombstone"] = "db_column *.public.restorecon_dropped.\"........pg.dropped.2........\" " + sepgsqlT + "\n" + config.Policy.DefaultSpec
		config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlA, Target: sepgsqlT, Class: "db_column", Deny: []string{"relabelto"}})
	}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		restoreconDroppedSetup(run)
		run(fixture.Exec("CREATE INDEX restorecon_dropped_index ON restorecon_dropped(keep)", 0))
		run(fixture.Exec("CREATE MATERIALIZED VIEW restorecon_dropped_mv AS SELECT tail FROM restorecon_dropped", 1))
		run(fixture.Exec("CREATE DATABASE restorecon_dropped_peer", 0))
		peerPool, _, peerRecorder := fixture.DatabaseScenarioPool(t, p, false, "restorecon_dropped_peer", "")
		peer, err := peerPool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		peerRun := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, peer, peerRecorder, step) }
		peerRun(fixture.Exec("CREATE TABLE peer_kept(v text)", 0))
		peerRun(fixture.Exec("INSERT INTO peer_kept VALUES('peer data')", 1))
		peerRun(fixture.Exec("SECURITY LABEL ON TABLE peer_kept IS '"+sepgsqlS+"'", 0))
		assertPeer := func() {
			run(sepgsqlText("SELECT label FROM pg_shseclabel WHERE classoid='pg_database'::regclass AND objoid=(SELECT oid FROM pg_database WHERE datname='restorecon_dropped_peer') AND provider='selinux'", "label", sepgsqlS))
			peerRun(sepgsqlText("SELECT v FROM peer_kept", "v", "peer data"))
			peerRun(sepgsqlText("SELECT label FROM pg_seclabel WHERE objoid='peer_kept'::regclass AND objsubid=0 AND provider='selinux'", "label", sepgsqlS))
		}
		for _, prepared := range []bool{false, true} {
			restoreconCall(run, "sepgsql_restorecon", "tombstone", prepared)
			restoreconDroppedLabels(run, sepgsqlS)
			restoreconDroppedFacts(t, p, c, run, true)
			run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE classoid='pg_class'::regclass AND objoid IN ('restorecon_dropped_index'::regclass,'restorecon_dropped_mv'::regclass) AND provider='selinux'", 0))
			labels := sepgsqlRestoreconLabels(t, p, c)
			step := sepgsqlDenied("SELECT sepgsql_restorecon('denied-tombstone')")
			step.Prepared = prepared
			run(step)
			restoreconUnchanged(t, p, c, labels)
		}
		run(fixture.Exec("SECURITY LABEL ON DATABASE restorecon_dropped_peer IS '"+sepgsqlS+"'", 0))
		assertPeer()
		image := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if strings.Contains(image, `SECURITY LABEL FOR selinux ON COLUMN "public"."restorecon_dropped"."........pg.dropped.2........"`) {
			t.Fatal("dump emitted impossible dropped-column SQL")
		}
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		for _, mutate := range []func(map[string]any){
			func(r map[string]any) { r["version"] = "incompatible" },
			func(r map[string]any) { r["retired_columns"] = nil },
			func(r map[string]any) { a := r["retired_columns"].([]any); r["retired_columns"] = append(a, a[0]) },
			func(r map[string]any) { r["retired_columns"].([]any)[0].(map[string]any)["relation"] = "missing" },
			func(r map[string]any) { r["retired_columns"].([]any)[0].(map[string]any)["attribute"] = 1 },
			func(r map[string]any) { r["retired_columns"].([]any)[0].(map[string]any)["attribute"] = 4 },
			func(r map[string]any) { r["retired_columns"].([]any)[0].(map[string]any)["attribute"] = -1 },
			func(r map[string]any) { r["retired_columns"].([]any)[0].(map[string]any)["label"] = "undeclared" },
			func(r map[string]any) { r["retired_columns"].([]any)[0].(map[string]any)["oid"] = 42 },
		} {
			bad := restoreconTamper(t, image, mutate)
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
				t.Fatal("tampered retired-column metadata accepted")
			}
			if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
				t.Fatal("failed import changed entire destination")
			}
		}
		assertPeer()
		// A SQL comment carries no import authority, even with a different
		// valid label in its otherwise well-formed typed payload.
		commentImage := restoreconTamper(t, image, func(r map[string]any) { r["retired_columns"].([]any)[0].(map[string]any)["label"] = sepgsqlP })
		line := strings.SplitN(commentImage[strings.Index(commentImage, "-- stuntgres-sepgsql-"):], "\n", 2)[0]
		run(sepgsqlBool(line+"\nSELECT true AS unchanged", "unchanged", true))
		restoreconDroppedLabels(run, sepgsqlS)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		assertPeer()
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(image)); err != nil {
			t.Fatal(err)
		}
		restoreconDroppedFacts(t, p, c, run, true)
		restoreconDroppedLabels(run, sepgsqlS)
		assertPeer()
		// Resolve against a genuinely fresh destination rather than source OIDs.
		target := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error {
			config := sepgsqlConfig()
			restoreconDroppedConfig(&config)
			return d.RegisterSepgsqlSimulation(config)
		})
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(image)); err != nil {
			t.Fatal("fresh destination retired-label import", err)
		}
		for _, b := range target.Backends() {
			if b.PG {
				continue
			}
			runTarget := func(step fixture.Step) { fixture.RunStep(t, target.Ctx, b.DB, b.Recorder, step) }
			restoreconDroppedFacts(t, target, b.DB, runTarget, true)
			restoreconDroppedLabels(runTarget, sepgsqlS)
		}
		restoreconRetiredTransfer(t, image)
	})
}

func restoreconPhaseSetup(run sepgsqlRun) {
	restoreconDroppedSetup(run)
	run(fixture.Exec("CREATE SCHEMA restorecon_phase", 0))
	run(fixture.Exec("CREATE SEQUENCE restorecon_phase.counter", 0))
	run(fixture.Exec("CREATE VIEW restorecon_phase.visible AS SELECT keep FROM restorecon_dropped", 0))
	run(fixture.Exec("CREATE FUNCTION restorecon_phase.f() RETURNS integer LANGUAGE SQL AS 'SELECT 1'", 0))
	run(fixture.Exec("CREATE FUNCTION restorecon_phase.f(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'", 0))
}

func restoreconAssertPhases(t *testing.T, events []stuntgres.SepgsqlAuditEvent) {
	t.Helper()
	last := -1
	seen := make(map[string]bool)
	dropped, system, overloads := false, false, 0
	for _, event := range events {
		if !slices.Contains(event.Required, "relabelfrom") && !slices.Contains(event.Required, "relabelto") {
			continue
		}
		rank, ok := map[string]int{"db_database": 0, "db_schema": 1, "db_table": 2, "db_sequence": 2, "db_view": 2, "db_column": 3, "db_procedure": 4}[event.Class]
		if !ok || rank < last {
			t.Fatal("released five-phase boundary", last, event)
		}
		last, seen[event.Class] = rank, true
		dropped = dropped || strings.HasSuffix(event.Name, `public.restorecon_dropped."........pg.dropped.2........"`)
		system = system || strings.HasSuffix(event.Name, "public.restorecon_dropped.ctid")
		if strings.HasSuffix(event.Name, "restorecon_phase.f") && slices.Contains(event.Required, "relabelfrom") {
			overloads++
		}
	}
	for _, class := range []string{"db_database", "db_schema", "db_table", "db_sequence", "db_view", "db_column", "db_procedure"} {
		if !seen[class] {
			t.Fatal("missing genuine restore category", class)
		}
	}
	if !dropped || !system || overloads != 2 {
		t.Fatal("dropped/system/signature-free overload identities", dropped, system, overloads)
	}
}

func TestReferenceSepgsqlSimulationRestoreconPhases(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "restorecon_phase")
	var events []stuntgres.SepgsqlAuditEvent
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		restoreconPhaseSetup(run)
		restoreconDroppedFacts(t, p, c, run, false)
		run(fixture.Select("SELECT restorecon_phase.f() AS zero,restorecon_phase.f(9) AS one", []string{"zero", "one"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}}))
	}, func(config *stuntgres.SepgsqlSimulationConfig) {
		restoreconDroppedConfig(config)
		config.AuditAllowed = true
		config.Audit = func(event stuntgres.SepgsqlAuditEvent) { events = append(events, event) }
	}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		restoreconPhaseSetup(run)
		for _, prepared := range []bool{false, true} {
			events = nil
			restoreconCall(run, "sepgsql_restorecon", "tombstone", prepared)
			restoreconAssertPhases(t, events)
			restoreconDroppedLabels(run, sepgsqlS)
			run(sepgsqlCount("SELECT count(*) FROM pg_seclabel l JOIN pg_proc p ON p.oid=l.objoid WHERE l.classoid='pg_proc'::regclass AND p.pronamespace='restorecon_phase'::regnamespace AND p.proname='f' AND l.label='"+sepgsqlP+"'", 2))
		}
	})
}

func restoreconUnchanged(t *testing.T, p *fixture.Pair, c fixture.Executor, before [][]string) {
	t.Helper()
	if !reflect.DeepEqual(sepgsqlRestoreconLabels(t, p, c), before) {
		t.Fatal("restore changed actual local/shared associations")
	}
}
