package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Defaults are installed only after the actual referenced objects exist. The
// native control phase must reconstruct these supported snapshots without
// borrowing the original allocation order or invoking any remote callback.
func TestReferenceFDWImportDefaultPersistence(t *testing.T) {
	for _, scenario := range []string{"later", "ordinary-deferred", "self", "cycle", "inline"} {
		t.Run(scenario, func(t *testing.T) {
			fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
				fdwLive(t, p, b, reader, run)
				fdwSourceExec(t, p, b, reader, run, "CREATE TABLE fdw_source.default_data(id integer,ref regclass,ok boolean)", 0)
				fdwSourceExec(t, p, b, reader, run, "INSERT INTO fdw_source.default_data VALUES(1,NULL,true)", 1)
				for _, query := range []string{
					"CREATE SCHEMA fdw_import",
					"CREATE ROLE fdw_owner",
					"CREATE ROLE fdw_other",
					"CREATE ROLE fdw_reader",
				} {
					run(fixture.Exec(query, 0))
				}
				if scenario == "ordinary-deferred" {
					run(fixture.Exec("CREATE TABLE fdw_import.ordinary(ref regclass)", 0))
					run(fixture.Exec("ALTER TABLE fdw_import.ordinary ALTER COLUMN ref SET DEFAULT 'fdw_import.ordinary'::regclass", 0))
					run(fixture.Exec("INSERT INTO fdw_import.ordinary DEFAULT VALUES", 1))
				}
				foreign := func(name string) {
					run(fixture.Exec("CREATE FOREIGN TABLE fdw_import."+name+"(id integer,ref regclass,ok boolean) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'default_data')", 0))
				}
				foreign("default_a")
				var definitions, dependencies [][]any
				switch scenario {
				case "later", "ordinary-deferred", "inline":
					// This allocation must follow the foreign consumer's creation.
					run(fixture.Exec("CREATE TABLE fdw_import.later(id integer)", 0))
					run(fixture.Exec("INSERT INTO fdw_import.later VALUES(73)", 1))
					if scenario == "inline" {
						run(fixture.Exec("ALTER FOREIGN TABLE fdw_import.default_a ALTER COLUMN ok SET DEFAULT ('fdw_import.later'::regclass IS NOT NULL)", 0))
						definitions = [][]any{{"default_a", "ok", "('fdw_import.later'::regclass IS NOT NULL)"}}
						// Opaque BOOL literals require replay ordering but need not
						// own a normal relation DROP edge on the native backend.
						dependencies = nil
					} else {
						run(fixture.Exec("ALTER FOREIGN TABLE fdw_import.default_a ALTER COLUMN ref SET DEFAULT 'fdw_import.later'::regclass", 0))
						definitions = [][]any{{"default_a", "ref", "'fdw_import.later'::regclass"}}
						dependencies = [][]any{{"default_a", "ref", "later", "n"}}
					}
					if scenario == "ordinary-deferred" {
						definitions = append(definitions, []any{"ordinary", "ref", "'fdw_import.ordinary'::regclass"})
						dependencies = append(dependencies, []any{"ordinary", "ref", "ordinary", "n"})
					}
				case "self":
					run(fixture.Exec("ALTER FOREIGN TABLE fdw_import.default_a ALTER COLUMN ref SET DEFAULT 'fdw_import.default_a'::regclass", 0))
					definitions = [][]any{{"default_a", "ref", "'fdw_import.default_a'::regclass"}}
					dependencies = [][]any{{"default_a", "ref", "default_a", "n"}}
				case "cycle":
					foreign("default_b")
					run(fixture.Exec("ALTER FOREIGN TABLE fdw_import.default_a ALTER COLUMN ref SET DEFAULT 'fdw_import.default_b'::regclass", 0))
					run(fixture.Exec("ALTER FOREIGN TABLE fdw_import.default_b ALTER COLUMN ref SET DEFAULT 'fdw_import.default_a'::regclass", 0))
					definitions = [][]any{{"default_a", "ref", "'fdw_import.default_b'::regclass"}, {"default_b", "ref", "'fdw_import.default_a'::regclass"}}
					dependencies = [][]any{{"default_a", "ref", "default_b", "n"}, {"default_b", "ref", "default_a", "n"}}
				}
				for _, query := range []string{
					"ALTER SCHEMA fdw_import OWNER TO fdw_owner",
					"ALTER SERVER fdw_live OWNER TO fdw_other",
					"ALTER FOREIGN TABLE fdw_import.default_a OWNER TO fdw_reader",
				} {
					run(fixture.Exec(query, 0))
				}
				if scenario == "cycle" {
					run(fixture.Exec("ALTER FOREIGN TABLE fdw_import.default_b OWNER TO fdw_reader", 0))
				}
				check := func() {
					t.Helper()
					run(fixture.Select("SELECT c.relname,a.attname,pg_get_expr(d.adbin,d.adrelid) AS definition FROM pg_attrdef d JOIN pg_class c ON c.oid=d.adrelid JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=d.adrelid AND a.attnum=d.adnum WHERE n.nspname='fdw_import' ORDER BY c.relname,a.attnum", []string{"relname", "attname", "definition"}, []string{"NAME", "NAME", "TEXT"}, definitions))
					if scenario != "inline" {
						run(fixture.Select("SELECT c.relname,a.attname,p.relname AS provider,x.deptype FROM pg_attrdef d JOIN pg_class c ON c.oid=d.adrelid JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=d.adrelid AND a.attnum=d.adnum JOIN pg_depend x ON x.objid=d.oid JOIN pg_class p ON p.oid=x.refobjid WHERE n.nspname='fdw_import' AND x.classid=2604 AND x.refclassid=1259 AND x.deptype='n' ORDER BY c.relname,a.attnum,p.relname", []string{"relname", "attname", "provider", "deptype"}, []string{"NAME", "NAME", "NAME", "CHAR"}, dependencies))
					}
					if scenario == "later" || scenario == "ordinary-deferred" || scenario == "inline" {
						run(fixture.Select("SELECT id FROM fdw_import.later", []string{"id"}, []string{"INT4"}, [][]any{{int64(73)}}))
					}
					if scenario == "ordinary-deferred" {
						run(fixture.Select("SELECT ref='fdw_import.ordinary'::regclass AS same FROM fdw_import.ordinary", []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
					}
					run(fixture.Select("SELECT r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid='fdw_import.default_a'::regclass::oid", []string{"rolname"}, []string{"NAME"}, [][]any{{"fdw_reader"}}))
					if !b.PG {
						run(fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{"postgres"}}))
					}
				}
				check()
				run(fixture.Select("SELECT id,ok FROM fdw_import.default_a", []string{"id", "ok"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
				if b.PG {
					return
				} // Native Dump/Reset/Restore has no PostgreSQL API counterpart.
				var forbiddenCalls atomic.Int64
				reader.setHook(func(ctx context.Context, method string) error {
					if method != "Validate" {
						forbiddenCalls.Add(1)
						return errors.New("default replay invoked remote callback")
					}
					return ctx.Err()
				})
				defer reader.setHook(nil)
				var snapshot bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &snapshot); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(snapshot.Bytes())); err != nil {
					t.Fatal(err)
				}
				check()
				run(fixture.Exec("SET ROLE fdw_reader", 0))
				denied := fdwDiagnostic("42501", "permission denied for schema fdw_import")
				denied.Position = "16"
				step := fixture.ErrorDiagnostic("SELECT id FROM fdw_import.default_a", denied)
				step.SelectRows = true
				run(step)
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Exec("CREATE TABLE default_replay_sentinel(v integer)", 0))
				run(fixture.Exec("INSERT INTO default_replay_sentinel VALUES(99)", 1))
				failure := errors.New("foreign default replay input failed")
				for _, kind := range []string{"missing-target", "noneligible", "drop-default", "malformed", "reader", "cancel"} {
					t.Run(kind, func(t *testing.T) {
						ctx := p.Ctx
						var input io.Reader = bytes.NewReader(snapshot.Bytes())
						switch kind {
						case "missing-target":
							input = strings.NewReader(snapshot.String() + "ALTER FOREIGN TABLE fdw_import.default_a ALTER COLUMN ref SET DEFAULT 'fdw_import.missing_default_target'::regclass;")
						case "noneligible":
							input = strings.NewReader(snapshot.String() + "ALTER FOREIGN TABLE fdw_import.default_a ALTER COLUMN id SET DEFAULT 7;")
						case "drop-default":
							input = strings.NewReader(snapshot.String() + "ALTER FOREIGN TABLE fdw_import.default_a ALTER COLUMN ref DROP DEFAULT;")
						case "malformed":
							input = strings.NewReader(snapshot.String() + "ALTER FOREIGN TABLE fdw_import.default_a ALTER COLUMN ref SET DEFAULT (")
						case "reader":
							input = &referenceRangeErrorReader{err: failure}
						case "cancel":
							var cancel context.CancelFunc
							ctx, cancel = context.WithCancel(p.Ctx)
							cancel()
						}
						err := stuntgres.Restore(ctx, p.Native, input)
						if err == nil {
							t.Fatal("invalid foreign default replay replaced the published snapshot")
						}
						if kind == "reader" && !errors.Is(err, failure) {
							t.Fatal("reader error identity lost", err)
						}
						if kind == "cancel" && !errors.Is(err, context.Canceled) {
							t.Fatal("cancellation identity lost", err)
						}
						check()
						run(fixture.Select("SELECT v FROM default_replay_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
					})
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(snapshot.Bytes())); err != nil {
					t.Fatal("recovery after bad foreign default", err)
				}
				check()
				if forbiddenCalls.Load() != 0 {
					t.Fatal("foreign default controls invoked remote callbacks")
				}
				reader.setHook(nil)
				run(fixture.Select("SELECT id,ok FROM fdw_import.default_a", []string{"id", "ok"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
			})
		})
	}
}
