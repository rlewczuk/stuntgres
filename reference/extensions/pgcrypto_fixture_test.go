package reference_test

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// The extension is database-global, so resetting public is insufficient.
func pgcryptoCase(t *testing.T, install bool, body func(*testing.T, *fixture.Pair, *sql.Conn, *fixture.Recorder, bool)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if backend.PG {
				clean := func() {
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS pgcrypto CASCADE", "DROP SCHEMA IF EXISTS pgcrypto_part CASCADE"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
					var count int
					if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_roles WHERE rolname='pgcrypto_owner'").Scan(&count); err != nil {
						t.Error(err)
					} else if count != 0 {
						for _, q := range []string{"DROP OWNED BY pgcrypto_owner", "DROP ROLE pgcrypto_owner"} {
							if _, err := c.ExecContext(p.Ctx, q); err != nil {
								t.Error(q, err)
							}
						}
					}
				}
				clean()
				defer clean()
			}
			if install {
				fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.Exec("CREATE EXTENSION pgcrypto", 0))
			}
			body(t, p, c, backend.Recorder, backend.PG)
		})
	}
}

// SQL witnesses use middleware on the same configured connector as native
// controls. Keep this connection pinned so TEMP authority belongs to the actual
// wrapped physical connection throughout catalog replacement.
func pgcryptoControlDestination(t *testing.T, p *fixture.Pair, dsn string) (*sql.DB, *sql.Conn, *fixture.Recorder) {
	t.Helper()
	connector, err := stuntgres.NewDriver().OpenConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	raw := sql.OpenDB(connector)
	t.Cleanup(func() {
		if err := raw.Close(); err != nil {
			t.Error(err)
		}
	})
	db, recorder := fixture.InsertSelectRecordedPool(t, connector)
	c, err := db.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return raw, c, recorder
}

func pgcryptoBytes(t *testing.T, value string) string {
	t.Helper()
	b, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func pgcryptoResult(t *testing.T, p *fixture.Pair, c fixture.Executor, r *fixture.Recorder, name, query, expected string, args ...any) {
	t.Helper()
	defer func() {
		if t.Failed() {
			t.Logf("pgcrypto direct/prepared result SQL=%s args=%#v", query, args)
		}
	}()
	s := fixture.Select(query, []string{name}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, expected)}}, args...)
	fixture.RunStep(t, p.Ctx, c, r, s)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
}

func pgcryptoUnsupported(query, algorithm string, args ...any) fixture.Step {
	return fixture.ImplicitError(query, fixture.Diagnostic{Code: "22023", Message: fmt.Sprintf("Cannot use %q: No such hash algorithm", algorithm)}, args...)
}
