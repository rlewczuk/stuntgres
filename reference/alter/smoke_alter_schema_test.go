package reference_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSmokeAlterSchemaSmoke(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "s1", "s2")
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					m, db, ctx, notices := fixture.MigrationAlterFixture(t, p, backend)
					c, err := db.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					exec := func(q string, count int64) {
						t.Helper()
						fixture.RunStep(t, ctx, c, m.Recorder, fixture.Exec(q, count))
					}
					diagnostic := func(err error, code, message, position string) {
						t.Helper()
						want := fixture.ControlsReferenceDiagnostic(code, message)
						want.Position = position
						fixture.AssertDiagnostic(t, err, &want)
					}
					dump := func() string {
						t.Helper()
						var b bytes.Buffer
						if err := stuntgres.Dump(ctx, p.Native, &b); err != nil {
							t.Fatal(err)
						}
						return b.String()
					}
					check := func(q string) {
						t.Helper()
						rows, err := c.QueryContext(ctx, q)
						if err != nil {
							t.Fatal(q, err)
						}
						m.NullRows(rows, []string{"id", "note"}, []string{"INT4", "TEXT"}, []bool{false, true}, [][]any{{int64(7), "keep"}, {int64(9), nil}})
					}
					scalar := func(q string, want int64) {
						t.Helper()
						var got int64
						if err := c.QueryRowContext(ctx, q).Scan(&got); err != nil || got != want {
							t.Fatal(q, got, want, err)
						}
					}
					for _, q := range []string{"CREATE SCHEMA s1", "CREATE SCHEMA s2", "CREATE TABLE s1.t(id INT CONSTRAINT nn_id NOT NULL,note TEXT DEFAULT 'fallback')", "COMMENT ON TABLE s1.t IS 'table comment'", "COMMENT ON COLUMN s1.t.id IS 'id comment'", "CREATE SEQUENCE s1.q1 START 10 CACHE 1 OWNED BY s1.t.id", "CREATE SEQUENCE s1.q2 START 100 CACHE 1 OWNED BY s1.t.id", "CREATE SEQUENCE s1.free START 1000", "CREATE VIEW v AS SELECT id,note FROM s1.t"} {
						exec(q, 0)
					}
					exec("INSERT INTO s1.t VALUES(7,'keep'),(9,NULL)", 2)
					scalar("SELECT nextval('s1.q1')", 10)
					scalar("SELECT nextval('s1.q2')", 100)
					for _, name := range []string{"t", "q2"} {
						exec("CREATE SEQUENCE s2."+name, 0)
						before := ""
						if !backend.PG {
							before = dump()
						}
						_, err := c.ExecContext(ctx, "ALTER TABLE s1.t SET SCHEMA s2")
						diagnostic(err, "42P07", `relation "`+name+`" already exists in schema "s2"`, "")
						if !backend.PG && dump() != before {
							t.Fatal("collision changed dependency closure")
						}
						check("SELECT id,note FROM s1.t ORDER BY id")
						scalar("SELECT currval('s1.q1')", 10)
						scalar("SELECT currval('s1.q2')", 100)
						exec("DROP SEQUENCE s2."+name, 0)
					}
					*notices = nil
					missing, err := c.PrepareContext(ctx, "ALTER TABLE IF EXISTS absent.missing SET SCHEMA absent_destination")
					if err != nil || len(*notices) != 0 {
						t.Fatal("effect-free missing Prepare", err, notices)
					}
					result, err := missing.ExecContext(ctx)
					missing.Close()
					if err != nil {
						t.Fatal(err)
					}
					if n, err := result.RowsAffected(); n != 0 || err != nil {
						t.Fatal(n, err)
					}
					if !reflect.DeepEqual(*notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `relation "missing" does not exist, skipping`}}) {
						t.Fatal(notices)
					}
					*notices = nil
					before := ""
					if !backend.PG {
						before = dump()
					}
					_, err = c.ExecContext(ctx, "ALTER TABLE s1.t SET SCHEMA s2, DROP COLUMN missing")
					diagnostic(err, "42601", `syntax error at or near ","`, "31")
					if backend.PG {
						fixture.SessionCancel(t, ctx, c, prepared)
					} else {
						canceled, cancel := context.WithCancel(ctx)
						cancel()
						_, err = c.ExecContext(canceled, "ALTER TABLE s1.t SET SCHEMA s2")
						if !errors.Is(err, context.Canceled) {
							t.Fatal(err)
						}
					}
					if (!backend.PG && dump() != before) || len(*notices) != 0 {
						t.Fatal("malformed/canceled call had effects", notices)
					}
					if prepared {
						s, err := c.PrepareContext(ctx, "ALTER TABLE s1.t SET SCHEMA s2")
						if err != nil {
							t.Fatal("Prepare", err)
						}
						if !backend.PG && dump() != before {
							t.Fatal("Prepare changed state")
						}
						result, err = s.ExecContext(ctx)
						s.Close()
						if err != nil {
							t.Fatal(err)
						}
						if n, err := result.RowsAffected(); err != nil || n != 0 {
							t.Fatal(n, err)
						}
					} else {
						exec("ALTER TABLE s1.t SET SCHEMA s2", 0)
					}
					check("SELECT id,note FROM s2.t ORDER BY id")
					// View nullable metadata follows its existing contract; assert values and
					// labels/types with the same public query, rather than invent view identity.
					rows, err := c.QueryContext(ctx, "SELECT id,note FROM public.v ORDER BY id")
					if err != nil {
						t.Fatal(err)
					}
					var view [][]any
					for rows.Next() {
						var a, b any
						if err := rows.Scan(&a, &b); err != nil {
							t.Fatal(err)
						}
						view = append(view, []any{a, b})
					}
					err = rows.Err()
					rows.Close()
					if err != nil || !reflect.DeepEqual(view, [][]any{{int64(7), "keep"}, {int64(9), nil}}) {
						t.Fatal(view, err)
					}
					_, err = c.QueryContext(ctx, "SELECT id,note FROM s1.t ORDER BY id")
					diagnostic(err, "42P01", `relation "s1.t" does not exist`, "21")
					scalar("SELECT nextval('s2.q1')", 11)
					scalar("SELECT currval('s2.q1')", 11)
					scalar("SELECT lastval()", 11)
					scalar("SELECT nextval('s2.q2')", 101)
					scalar("SELECT nextval('s1.free')", 1000)
					if !backend.PG {
						persistent := dump()
						if err := stuntgres.Reset(ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(ctx, p.Native, strings.NewReader(persistent)); err != nil {
							t.Fatal(err)
						}
						if dump() != persistent {
							t.Fatal("canonical Dump/Reset/Restore mismatch")
						}
						check("SELECT id,note FROM s2.t ORDER BY id")
						_, err = c.QueryContext(ctx, "SELECT id,note FROM s1.t ORDER BY id")
						diagnostic(err, "42P01", `relation "s1.t" does not exist`, "21")
					}
				})
			}
		})
	}
}
