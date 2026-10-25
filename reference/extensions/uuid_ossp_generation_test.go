package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func uuidOsspGeneration(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, call string) {
	t.Helper()
	q := "SELECT " + call + " AS u"
	seen := map[string]bool{}
	check := func(values []string) {
		for _, v := range values {
			if seen[v] {
				t.Fatalf("reused generated value %q", v)
			}
			seen[v] = true
		}
	}
	check(uuidOsspRead(t, p, c, r, pg, q, 1, 1))
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for range 3 {
		check(uuidOsspRead(t, p, held, r, pg, q, 1, 1))
	}
	check(uuidOsspRead(t, p, c, r, pg, "SELECT "+call+" AS u,"+call+" AS u", 1, 2))
	for _, prepared := range []bool{false, true} {
		empty := q + " WHERE FALSE"
		var e fixture.Executor = c
		if prepared {
			s, err := c.PrepareContext(p.Ctx, empty)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			e = fixture.HeldStatement{Executor: c, Statement: s, SQL: empty}
		}
		uuidOsspRead(t, p, e, r, pg, empty, 0, 1)
	}
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT CASE WHEN FALSE THEN "+call+" ELSE NULL END AS u", []string{"u"}, []string{"UUID"}, [][]any{{nil}}))
	for _, dst := range []any{new(string), new([]byte), new(sql.NullString)} {
		if err := c.QueryRowContext(p.Ctx, q).Scan(dst); err != nil {
			t.Fatal(err)
		}
		switch v := dst.(type) {
		case *string:
			uuidOsspDecode(t, *v)
		case *[]byte:
			uuidOsspDecode(t, string(*v))
		case *sql.NullString:
			if !v.Valid {
				t.Fatal("NULL generated")
			}
			uuidOsspDecode(t, v.String)
		}
	}
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE uuid_ossp_ids(id integer)", 0))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO uuid_ossp_ids VALUES(1),(2),(3)", 3))
	check(uuidOsspRead(t, p, c, r, pg, q+" FROM uuid_ossp_ids ORDER BY id", 3, 1))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE uuid_ossp_nullable(u uuid)", 0))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO uuid_ossp_nullable VALUES(NULL)", 1))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT u FROM uuid_ossp_nullable", []string{"u"}, []string{"UUID"}, [][]any{{nil}}))
}

func TestReferenceUUIDOsspGeneration(t *testing.T) {
	uuidOsspCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		uuidOsspGeneration(t, p, c, r, pg, "uuid_generate_v4()")
	})
}
