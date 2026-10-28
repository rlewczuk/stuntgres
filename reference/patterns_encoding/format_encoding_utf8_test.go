package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFormatEncodingUTF8(t *testing.T) {
	formatEncodingBackends(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE fe_unicode(id integer, v text)", 0))
		run(fixture.Prepared(fixture.Exec("INSERT INTO fe_unicode VALUES(1,$1)", 1, "é🙂")))
		for _, tc := range []struct{ input, want string }{{"UTF8", "UTF8"}, {"utf-8", "UTF8"}, {"unicode", "UTF8"}, {"UNICODE", "UNICODE"}, {"u.t_f-8", "UTF8"}} {
			t.Run("alias/"+tc.input, func(t *testing.T) {
				for _, names := range []bool{false, true} {
					q := "SET client_encoding TO '" + tc.input + "'"
					if names {
						q = "SET NAMES '" + tc.input + "'"
					}
					formatEncodingHeld(t, p, b, c, fixture.Exec(q, 0), nil)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, formatEncodingText("SHOW client_encoding", "client_encoding", tc.want))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, formatEncodingText("SELECT 'é🙂'::text AS v", "v", "é🙂"))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(formatEncodingText("SELECT $1::text AS v", "v", "é🙂", "é🙂")))
					characters := fixture.Select("SELECT $1::char(3) AS c,$2::varchar(2) AS v", []string{"c", "v"}, []string{"BPCHAR", "VARCHAR"}, [][]any{{"é🙂 ", "é🙂"}}, "é🙂", "é🙂")
					formatEncodingHeld(t, p, b, c, characters, nil)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, formatEncodingText("SELECT v FROM fe_unicode WHERE id=1", "v", "é🙂"))
				}
			})
		}
		t.Run("encoding-name-length", func(t *testing.T) {
			long := strings.Repeat("-", 80) + "utf8"
			// Pinned tests-1 through tests-3 observed the same clipped invalid
			// primary for SQL SET, SET NAMES and set_config TEXT input.
			for _, query := range []string{"SET client_encoding TO '" + long + "'", "SET NAMES '" + long + "'"} {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/prepared=%v", query, prepared), func(t *testing.T) {
						step := fixture.ImplicitError(query, formatEncodingInvalid("client_encoding", strings.Repeat("-", 63), ""))
						step.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, formatEncodingText("SHOW client_encoding", "client_encoding", "UTF8"))
					})
				}
			}
			t.Run("set_config", func(t *testing.T) {
				step := fixture.ImplicitError("SELECT set_config('client_encoding',$1,false)", formatEncodingInvalid("client_encoding", strings.Repeat("-", 63), ""), long)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				formatEncodingHeld(t, p, b, c, step, nil)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, formatEncodingText("SHOW client_encoding", "client_encoding", "UTF8"))
			})
		})
		run(fixture.Exec("SET SESSION NAMES DEFAULT", 0))
		run(formatEncodingText("SHOW client_encoding", "client_encoding", "UTF8"))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET LOCAL NAMES 'UNICODE'", 0))
		run(formatEncodingText("SHOW client_encoding", "client_encoding", "UNICODE"))
		run(fixture.Exec("COMMIT", 0))
		run(formatEncodingText("SHOW client_encoding", "client_encoding", "UTF8"))
		for _, value := range []string{"stuntgres_unknown_encoding", "MULE_INTERNAL"} {
			t.Run("invalid/"+value, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					want := formatEncodingInvalid("client_encoding", value, "")
					if value == "MULE_INTERNAL" {
						// Pinned tests-2 established this genuinely unavailable
						// conversion's complete diagnostic; native matches it.
						want.Code = "0A000"
						want.Detail = "Conversion between MULE_INTERNAL and UTF8 is not supported."
					}
					step := fixture.ImplicitError("SET client_encoding TO '"+value+"'", want)
					step.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, formatEncodingText("SHOW client_encoding", "client_encoding", "UTF8"))
				}
			})
		}
		for _, value := range []string{"LATIN1", "SQL_ASCII"} {
			t.Run("transport-admission/"+value, func(t *testing.T) {
				// Only ASCII crosses pq's transport until UTF8 is restored;
				// native Unicode controls are exercised by the shared encoding family.
				fixture.RunStep(t, p.Ctx, c, b.Recorder, formatEncodingText("SELECT set_config('client_encoding',$1,false)", "set_config", value, value))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, formatEncodingText("SHOW client_encoding", "client_encoding", value))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET client_encoding TO UTF8", 0))
			})
		}
		for _, tc := range []struct{ input, bytes string }{{"\x00", "0x00"}, {"\xff", "0xff"}} {
			for _, prepared := range []bool{false, true} {
				t.Run("bad-bind/"+tc.bytes+"/"+map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
					want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22021", Message: `invalid byte sequence for encoding "UTF8": ` + tc.bytes}
					// Native import owns this existing portal continuation; keep
					// the independently pinned PostgreSQL record below explicit.
					want.Context = "unnamed portal parameter $1"
					step := fixture.ImplicitError("SELECT $1::text AS v", want, tc.input)
					step.SelectRows, step.Prepared = true, prepared
					if b.PG {
						pg := want
						pg.Context = "unnamed portal parameter $1"
						step.PostgresDiagnostic = &pg
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, formatEncodingText("SELECT v FROM fe_unicode WHERE id=1", "v", "é🙂"))
				})
			}
		}
		for _, tc := range []struct{ q, bytes string }{{`SELECT E'\xff'::text`, "0xff"}, {`SELECT E'\000'::text`, "0x00"}} {
			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22021", Message: `invalid byte sequence for encoding "UTF8": ` + tc.bytes}
			step := fixture.ErrorDiagnostic(tc.q, want)
			step.SelectRows = true
			run(step)
		}
	})
}
