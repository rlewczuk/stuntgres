package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func identConsume(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	defer rows.Close()
	var got [][]any
	for rows.Next() {
		cells := make([]any, 7)
		targets := make([]any, 7)
		for i := range cells {
			targets[i] = &cells[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		for i, v := range cells {
			if data, ok := v.([]byte); ok {
				cells[i] = string(data)
			}
		}
		got = append(got, cells)
	}
	if err := rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, want, err)
	}
}
func identDump(t *testing.T, p *fixture.Pair, raw *sql.DB) string {
	t.Helper()
	var out bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, raw, &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}
func identControlFlags(t *testing.T, p *fixture.Pair, b fixture.Backend, granted bool) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT has_table_privilege('pg_read_all_stats','pg_catalog.pg_ident_file_mappings','SELECT') AS whole,has_column_privilege('pg_signal_backend','pg_catalog.pg_ident_file_mappings','file_name','SELECT') AS col,has_function_privilege('pg_signal_backend','pg_catalog.pg_ident_file_mappings()','EXECUTE') AS routine", []string{"whole", "col", "routine"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{granted, granted, granted}}))
	identInitialACL(t, p, b, b.DB, !granted)
}

func TestReferencePGIdentFileMappingsControls(t *testing.T) {
	p, files := fixture.NewIdentFileScenario(t, "source_control os_source pg_source\n", nil)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			want := [][]any{identValid(1, files.Path(b, ""), 1, "source_control", "os_source", "pg_source")}
			identRead(t, p, b, b.DB, identSelect, want, false)
			identInitialACL(t, p, b, b.DB, true)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE ident_control_sentinel(n int)", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO ident_control_sentinel VALUES(42)", 1))
			if !b.PG {
				baseline := identDump(t, p, p.Native)
				if strings.Contains(baseline, "pg_ident_file_mappings") || strings.Contains(baseline, files.Path(b, "")) || strings.Contains(baseline, "source_control") {
					t.Fatal("baseline serializes startup authority or reconstructed ACL", baseline)
				}
			}
			if b.PG {
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					for _, q := range []string{
						"REVOKE SELECT ON pg_catalog.pg_ident_file_mappings FROM pg_read_all_stats",
						"REVOKE SELECT(file_name) ON pg_catalog.pg_ident_file_mappings FROM pg_signal_backend",
						"REVOKE EXECUTE ON FUNCTION pg_catalog.pg_ident_file_mappings() FROM pg_signal_backend",
					} {
						if _, err := b.DB.ExecContext(ctx, q); err != nil {
							t.Error(err)
						}
					}
				}()
			}
			for _, q := range []string{
				"GRANT SELECT ON pg_catalog.pg_ident_file_mappings TO pg_read_all_stats",
				"GRANT SELECT(file_name) ON pg_catalog.pg_ident_file_mappings TO pg_signal_backend",
				"GRANT EXECUTE ON FUNCTION pg_catalog.pg_ident_file_mappings() TO pg_signal_backend",
			} {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
			}
			identControlFlags(t, p, b, true)
			// Common genuine SQL phases exercise grants, current bytes and sensitive
			// column admission before native-only control operations diverge.
			files.Write(t, p.Ctx, b, "", "source_current os_current pg_current\n")
			identRead(t, p, b, b.DB, identSelect, [][]any{identValid(1, files.Path(b, ""), 1, "source_current", "os_current", "pg_current")}, true)
			files.Write(t, p.Ctx, b, "", "source_control os_source pg_source\n")
			reader, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Exec("SET ROLE pg_signal_backend", 0))
			fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Select("SELECT file_name FROM pg_catalog.pg_ident_file_mappings", []string{"file_name"}, []string{"TEXT"}, [][]any{{files.Path(b, "")}}))
			fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Exec("RESET ROLE", 0))
			reader.Close()
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT n FROM ident_control_sentinel", []string{"n"}, []string{"INT4"}, [][]any{{int64(42)}}))
			if b.PG {
				return
			}
			files.Remove(t, p.Ctx, b, "")
			saved := identDump(t, p, p.Native) // Dump is metadata-only, even with a missing attached file.
			for _, fragment := range []string{"pg_ident_file_mappings", "pg_read_all_stats", "pg_signal_backend", "attacl", "proacl", "relacl"} {
				if !strings.Contains(saved, fragment) {
					t.Fatal("missing symbolic ACL", fragment, saved)
				}
			}
			if strings.Contains(saved, files.Path(b, "")) || strings.Contains(saved, "source_control") {
				t.Fatal("dump leaks file authority", saved)
			}
			files.Write(t, p.Ctx, b, "", "source_control os_source pg_source\n")
			old, err := b.DB.QueryContext(p.Ctx, identSelect)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			identRead(t, p, b, b.DB, identSelect, want, true)
			identControlFlags(t, p, b, false)
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(saved)); err != nil {
				t.Fatal(err)
			}
			identRead(t, p, b, b.DB, identSelect, want, false)
			identControlFlags(t, p, b, true)
			identConsume(t, old, want)
			sentinel := fixture.Select("SELECT n FROM ident_control_sentinel", []string{"n"}, []string{"INT4"}, [][]any{{int64(42)}})
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, sentinel)
			// Restore into a distinct Driver must never import the source path or bytes.
			destination := filepath.Join(t.TempDir(), "destination.conf")
			if err := os.WriteFile(destination, []byte("destination_control os_dest pg_dest\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			d := stuntgres.NewDriver()
			if err := d.ConfigureIdentFile(destination); err != nil {
				t.Fatal(err)
			}
			raw, target := identNativePool(t, d, p.NativeDSN)
			if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader(saved)); err != nil {
				t.Fatal(err)
			}
			targetWant := [][]any{identValid(1, destination, 1, "destination_control", "os_dest", "pg_dest")}
			identRead(t, p, target, target.DB, identSelect, targetWant, true)
			identControlFlags(t, p, target, true)
			before := identDump(t, p, raw)
			retained, err := target.DB.QueryContext(p.Ctx, identSelect)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader("CREATE TABLE ident_partial(n int); INSERT INTO ident_partial VALUES ('bad');")); err == nil {
				t.Fatal("failed Restore accepted")
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(ctx, raw, strings.NewReader(saved)); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if after := identDump(t, p, raw); after != before {
				t.Fatal("failed Restore mutated catalog/ACL", before, after)
			}
			identRead(t, p, target, target.DB, identSelect, targetWant, false)
			identControlFlags(t, p, target, true)
			fixture.RunStep(t, p.Ctx, target.DB, target.Recorder, sentinel)
			identConsume(t, retained, targetWant)
			// Explicit column and routine grants admit their actual sensitive values.
			actor, err := target.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer actor.Close()
			fixture.RunStep(t, p.Ctx, actor, target.Recorder, fixture.Exec("SET ROLE pg_signal_backend", 0))
			fixture.RunStep(t, p.Ctx, actor, target.Recorder, fixture.Select("SELECT file_name FROM pg_catalog.pg_ident_file_mappings", []string{"file_name"}, []string{"TEXT"}, [][]any{{destination}}))
			identDenied(t, p, target, actor, identSelect, false)
			fixture.RunStep(t, p.Ctx, actor, target.Recorder, fixture.Exec("RESET ROLE", 0))
			identRead(t, p, b, b.DB, identSelect, want, false)
		})
	}
}

func TestReferencePGIdentFileMappingsSmoke(t *testing.T) {
	p, files := fixture.NewIdentFileScenario(t, "smoke os db\nmissing\n", nil)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			main := files.Path(b, "")
			want := [][]any{identValid(1, main, 1, "smoke", "os", "db"), identInvalid(main, 2, identMissing)}
			identRead(t, p, b, b.DB, identSelect+" ORDER BY line_number", want, false)
			query := identSelect + " ORDER BY line_number"
			stmt, err := b.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			old, err := stmt.QueryContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			files.Write(t, p.Ctx, b, "", "changed next target\n")
			next := [][]any{identValid(1, main, 1, "changed", "next", "target")}
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: query}
			identRead(t, p, b, held, query, next, true)
			actor, admin := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(e fixture.Executor, q string) { fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 0)) }
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, err := actor.ExecContext(ctx, "RESET ROLE"); err != nil {
					t.Error(err)
				}
				for _, q := range []string{"REVOKE SELECT ON pg_catalog.pg_ident_file_mappings FROM pg_signal_backend", "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_ident_file_mappings() FROM pg_signal_backend"} {
					if _, err := admin.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			run(actor, "SET ROLE pg_signal_backend")
			identDenied(t, p, b, actor, identSelect, false)
			run(admin, "GRANT SELECT ON pg_catalog.pg_ident_file_mappings TO pg_signal_backend")
			identDenied(t, p, b, actor, identSelect, true)
			run(admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_ident_file_mappings() TO pg_signal_backend")
			identRead(t, p, b, actor, identSelect, next, true)
			run(admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_ident_file_mappings() FROM pg_signal_backend")
			identDenied(t, p, b, actor, identSelect, true)
			run(actor, "RESET ROLE")
			if !b.PG {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				identRead(t, p, b, b.DB, identSelect, next, true)
				identInitialACL(t, p, b, b.DB, true)
				run(actor, "SET ROLE pg_signal_backend")
				identDenied(t, p, b, actor, identSelect, false)
				run(actor, "RESET ROLE")
			}
			identConsume(t, old, want)
		})
	}
}
