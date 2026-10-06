//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func referenceSequenceRegclassOutput(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name string
		db   *sql.DB
	}{{"native", p.memory}, {"postgres", p.postgres}} {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			exec := func(q string) {
				t.Helper()
				if _, err := c.ExecContext(p.ctx, q); err != nil {
					t.Fatal(q, err)
				}
			}
			for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE a.s", "CREATE SEQUENCE b.s", `CREATE SEQUENCE a."select"`, `CREATE SEQUENCE a."A"`, "SET search_path TO a,b"} {
				exec(q)
			}
			for _, tc := range []struct{ query, want string }{{"SELECT 'a.s'::regclass", "s"}, {"SELECT 'b.s'::regclass", "b.s"}, {`SELECT 'a.select'::regclass`, `"select"`}, {`SELECT 'a."A"'::regclass`, `"A"`}, {"SELECT '4294967295'::regclass", "4294967295"}, {"SELECT '0'::regclass", "-"}} {
				rows, err := c.QueryContext(p.ctx, tc.query)
				if err != nil {
					t.Fatal(err)
				}
				columns, err := rows.Columns()
				if err != nil || len(columns) != 1 || columns[0] != "regclass" {
					t.Fatal(columns, err)
				}
				if !rows.Next() {
					t.Fatal(rows.Err())
				}
				var got string
				if err := rows.Scan(&got); err != nil || got != tc.want {
					t.Fatal(tc.query, got, tc.want, err)
				}
				rows.Close()
			}
			exec("SET search_path TO b,a")
			var got string
			if err := c.QueryRowContext(p.ctx, "SELECT 'a.s'::regclass").Scan(&got); err != nil || got != "a.s" {
				t.Fatal(got, err)
			}
		})
	}
}

// Native retained OID datums are witnessed through the typed engine API. Only
// this oracle uses PostgreSQL's general oid cast, which native SQL excludes.
func referenceSequenceNumericIdentity(t *testing.T) {
	p := newReferencePair(t)
	c, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	exec := func(q string) {
		t.Helper()
		if _, err := c.ExecContext(p.ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	exec("CREATE SEQUENCE s START 10")
	var oid int64
	if err := c.QueryRowContext(p.ctx, "SELECT 's'::regclass::oid::bigint").Scan(&oid); err != nil {
		t.Fatal(err)
	}
	numeric, err := c.PrepareContext(p.ctx, fmt.Sprintf("SELECT nextval('%d'::regclass)", oid))
	if err != nil {
		t.Fatal(err)
	}
	defer numeric.Close()
	parameter, err := c.PrepareContext(p.ctx, "SELECT nextval($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer parameter.Close()
	exec("ALTER SEQUENCE s RENAME TO identity")
	var got int64
	if err := numeric.QueryRowContext(p.ctx).Scan(&got); err != nil || got != 10 {
		t.Fatal(got, err)
	}
	if err := parameter.QueryRowContext(p.ctx, fmt.Sprint(oid)).Scan(&got); err != nil || got != 11 {
		t.Fatal(got, err)
	}
	exec("DROP SEQUENCE identity")
	exec("CREATE SEQUENCE s START 90")
	want := controlsReferenceDiagnostic("XX000", fmt.Sprintf("could not open relation with OID %d", oid))
	referenceAssertDiagnostic(t, numeric.QueryRowContext(p.ctx).Scan(&got), &want)
	referenceAssertDiagnostic(t, parameter.QueryRowContext(p.ctx, fmt.Sprint(oid)).Scan(&got), &want)
	sequenceProbeValue(t, p.ctx, c, "SELECT nextval('s')", 90)
}
