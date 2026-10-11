package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBinaryAccessTyping(t *testing.T) {
	binaryAccessScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE DOMAIN access_bytes AS bytea", 0),
			fixture.Exec("CREATE DOMAIN access_offset AS bigint", 0),
			fixture.Exec("CREATE TABLE access_domains(v access_bytes,n access_offset)", 0),
			fixture.Exec(`INSERT INTO access_domains VALUES('\x0180',15)`, 1),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		binaryAccessBoth(t, p, b, c, fixture.Select("SELECT bit_count(v),get_bit(v,n),get_byte(v,1::smallint),set_bit(v,n,0::smallint),set_byte(v,0::smallint,256),reverse(v) FROM access_domains", binaryAccessNames, binaryAccessTypes, [][]any{{int64(2), int64(1), int64(128), "\x01\x00", "\x00\x80", "\x80\x01"}}))
		binaryAccessBoth(t, p, b, c, fixture.Select(`SELECT pg_catalog.bit_count('\xff'::bytea),"get_byte"('\xff'::bytea,0),"set_bit"('\xff'::bytea,0,0)`, []string{"bit_count", "get_byte", "set_bit"}, []string{"INT8", "INT4", "BYTEA"}, [][]any{{int64(8), int64(255), "\xfe"}}))
		q := "SELECT bit_count($1::bytea),reverse($1::bytea)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			input any
			want  []any
		}{{[]byte{}, []any{int64(0), ""}}, {nil, []any{nil, nil}}, {[]byte(nil), []any{nil, nil}}, {[]byte{0, 255, 128, 65}, []any{int64(11), "\x41\x80\xff\x00"}}, {[]byte{255}, []any{int64(8), "\xff"}}} {
			s := fixture.Select(q, []string{"bit_count", "reverse"}, []string{"INT8", "BYTEA"}, [][]any{tc.want}, tc.input)
			fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
		}
		for _, q := range []string{"SELECT get_byte($1::bytea,$2::integer)", "SELECT get_bit($1::bytea,$2::bigint)", "SELECT set_byte($1::bytea,0,$2::integer)", "SELECT set_bit($1::bytea,0,$2::integer)"} {
			t.Run(q, func(t *testing.T) {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				typ := "integer"
				if q == "SELECT get_bit($1::bytea,$2::bigint)" {
					typ = "bigint"
				}
				d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type `+typ+`: "bad"`)
				d.Context = `unnamed portal parameter $2 = '...'`
				s := fixture.ErrorDiagnostic(q, d, nil, "bad")
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
				name, result := "get_byte", "INT4"
				if typ == "bigint" {
					name = "get_bit"
				}
				if q[7:10] == "set" {
					result = "BYTEA"
					name = "set_byte"
					if q == "SELECT set_bit($1::bytea,0,$2::integer)" {
						name = "set_bit"
					}
				}
				fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{name}, []string{result}, [][]any{{nil}}, nil, 0))
			})
		}
	})
}
