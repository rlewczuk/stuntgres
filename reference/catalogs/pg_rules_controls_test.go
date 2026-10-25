package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGRulesNativeControls(t *testing.T) {
	p := fixture.NewScenario(t)
	native := p.Backends()[0]
	for _, q := range []string{"CREATE TABLE pgr_control_src(id INT)", "CREATE TABLE pgr_control_log(id INT)", "CREATE RULE r AS ON INSERT TO pgr_control_src DO INSERT INTO pgr_control_log VALUES(NEW.id)", "ALTER TABLE pgr_control_src RENAME TO pgr_control_current", "ALTER TABLE pgr_control_log RENAME COLUMN id TO log_id"} {
		fixture.RunStep(t, p.Ctx, native.DB, native.Recorder, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, native.DB, native.Recorder, fixture.Exec("INSERT INTO pgr_control_log VALUES(19)", 1))
	dump := fixture.ViewDump(t, p)
	if len(dump) == 0 {
		t.Fatal("empty actual native Dump")
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			want := []any{"public", "pgr_control_current", "r", "CREATE RULE r AS\n    ON INSERT TO public.pgr_control_current DO  INSERT INTO pgr_control_log (log_id)\n  VALUES (new.id);"}
			check := func(db fixture.Executor) {
				pgRulesCheck(t, p, b, db, pgRulesProbe, [][]any{want}, "public", "pgr_control_current", "r")
				fixture.RunStep(t, p.Ctx, db, b.Recorder, fixture.Select("SELECT log_id FROM pgr_control_log", []string{"log_id"}, []string{"INT4"}, [][]any{{int64(19)}}))
				fixture.RunStep(t, p.Ctx, db, b.Recorder, fixture.Select("SELECT id FROM pgr_control_current", []string{"id"}, []string{"INT4"}, nil))
			}
			builtin := func() {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT relkind FROM pg_class WHERE oid='pg_catalog.pg_rules'::regclass", []string{"relkind"}, []string{"CHAR"}, [][]any{{"v"}}))
			}
			if b.PG {
				conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
				defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				if _, err := conn.ExecContext(p.Ctx, string(dump)); err != nil {
					t.Fatal("PG rejected actual native Dump", err)
				}
				check(conn)
				builtin()
				return
			}
			held := fixture.ViewHold(t, p, b, pgRulesProbe)
			check(b.DB)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.PGTableViewSelect(b, pgRulesProbe, pgRulesColumns, pgRulesTypes, [][]any{want}, "public", "pgr_control_current", "r"))
			old, err := b.DB.QueryContext(p.Ctx, pgRulesProbe, "public", "pgr_control_current", "r")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			types, err := old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(string(dump)+"CREATE SEQUENCE pgr_bad CACHE 0;")); err == nil {
				t.Fatal("invalid Restore succeeded")
			} else {
				diagnostic := fixture.ControlsReferenceDiagnostic("22023", "CACHE (0) must be greater than zero")
				fixture.AssertDiagnostic(t, err, &diagnostic)
			}
			check(b.DB)
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump)); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			check(b.DB)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.PGTableViewSelect(b, pgRulesProbe, pgRulesColumns, pgRulesTypes, nil, "public", "pgr_control_current", "r"))
			builtin()
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			check(b.DB)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.PGTableViewSelect(b, pgRulesProbe, pgRulesColumns, pgRulesTypes, [][]any{want}, "public", "pgr_control_current", "r"))
			builtin()
			pgRulesRetained(t, old, types, want)
			if redump := fixture.ViewDump(t, p); !bytes.Equal(dump, redump) {
				t.Fatal("logical Dump changed", string(dump), string(redump))
			}
		})
	}
}
