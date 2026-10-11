package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSessionIdentityDatabaseName(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			if backend.PG {
				for _, prepared := range []bool{false, true} {
					s := fixture.Select(`SELECT current_database(),current_catalog`, []string{"current_database", "current_catalog"}, []string{"NAME", "NAME"}, [][]any{{"stuntgres_reference", "stuntgres_reference"}})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				}
				return
			}
			// Opaque DSN keys are native host identities, not PostgreSQL URLs.
			open := func(d *stuntgres.Driver, dsn string) *sql.DB {
				c, err := d.OpenConnector(dsn)
				if err != nil {
					t.Fatal(err)
				}
				db := sql.OpenDB(c)
				t.Cleanup(func() {
					if err := db.Close(); err != nil {
						t.Error(err)
					}
				})
				return db
			}
			for _, tc := range []struct{ dsn, want string }{{"", ""}, {"  identity db  ", "  identity db  "}, {"postgres://host/db?user=x", "postgres://host/db?user=x"}, {strings.Repeat("a", 64), strings.Repeat("a", 63)}, {strings.Repeat("a", 62) + "é", strings.Repeat("a", 62)}} {
				db := open(stuntgres.NewDriver(), tc.dsn)
				var name string
				if err := db.QueryRowContext(p.Ctx, `SELECT current_database()`).Scan(&name); err != nil || name != tc.want {
					t.Fatal(tc.dsn, name, err)
				}
			}
			d := stuntgres.NewDriver()
			a, b := open(d, strings.Repeat("a", 63)+"x"), open(d, strings.Repeat("a", 63)+"y")
			if _, err := a.ExecContext(p.Ctx, `CREATE TABLE only_a(id INT)`); err != nil {
				t.Fatal(err)
			}
			rows, err := b.QueryContext(p.Ctx, `SELECT id FROM only_a`)
			if rows != nil {
				rows.Close()
				t.Fatal("failed Query exposed cursor")
			}
			fixture.State(t, err, "42P01")
			bad := open(stuntgres.NewDriver(), string([]byte{255}))
			if err := bad.PingContext(p.Ctx); err == nil {
				t.Fatal("invalid UTF8 NAME admitted")
			}
			c, err := stuntgres.NewDriver().OpenConnector("notice identity")
			if err != nil {
				t.Fatal(err)
			}
			db := sql.OpenDB(c.(*stuntgres.Connector).WithNoticeHandler(func(stuntgres.Error) {}))
			defer db.Close()
			var name string
			if err := db.QueryRowContext(p.Ctx, `SELECT current_database()`).Scan(&name); err != nil || name != "notice identity" {
				t.Fatal(name, err)
			}
		})
	}
}
