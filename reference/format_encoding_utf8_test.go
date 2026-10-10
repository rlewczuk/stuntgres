package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestReferenceFormatEncodingUTF8(t *testing.T) {
	formatEncodingBackends(t, func(p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE fe_unicode(id integer, v text)", 0))
		run(referencePrepared(referenceExec("INSERT INTO fe_unicode VALUES(1,$1)", 1, "é🙂")))
		for _, tc := range []struct{ input, want string }{{"UTF8", "UTF8"}, {"utf-8", "UTF8"}, {"unicode", "UTF8"}, {"UNICODE", "UNICODE"}, {"u.t_f-8", "UTF8"}} {
			t.Run("alias/"+tc.input, func(t *testing.T) {
				for _, names := range []bool{false, true} {
					q := "SET client_encoding TO '" + tc.input + "'"
					if names {
						q = "SET NAMES '" + tc.input + "'"
					}
					formatEncodingHeld(t, p, b, c, referenceExec(q, 0), nil)
					referenceRunStep(t, p.ctx, c, b.recorder, formatEncodingText("SHOW client_encoding", "client_encoding", tc.want))
					referenceRunStep(t, p.ctx, c, b.recorder, formatEncodingText("SELECT 'é🙂'::text AS v", "v", "é🙂"))
					referenceRunStep(t, p.ctx, c, b.recorder, referencePrepared(formatEncodingText("SELECT $1::text AS v", "v", "é🙂", "é🙂")))
					characters := referenceSelect("SELECT $1::char(3) AS c,$2::varchar(2) AS v", []string{"c", "v"}, []string{"BPCHAR", "VARCHAR"}, [][]any{{"é🙂 ", "é🙂"}}, "é🙂", "é🙂")
					formatEncodingHeld(t, p, b, c, characters, nil)
					referenceRunStep(t, p.ctx, c, b.recorder, formatEncodingText("SELECT v FROM fe_unicode WHERE id=1", "v", "é🙂"))
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
						step := referenceImplicitError(query, formatEncodingInvalid("client_encoding", strings.Repeat("-", 63), ""))
						step.prepared = prepared
						referenceRunStep(t, p.ctx, c, b.recorder, step)
						referenceRunStep(t, p.ctx, c, b.recorder, formatEncodingText("SHOW client_encoding", "client_encoding", "UTF8"))
					})
				}
			}
			t.Run("set_config", func(t *testing.T) {
				step := referenceImplicitError("SELECT set_config('client_encoding',$1,false)", formatEncodingInvalid("client_encoding", strings.Repeat("-", 63), ""), long)
				referenceRunStep(t, p.ctx, c, b.recorder, step)
				formatEncodingHeld(t, p, b, c, step, nil)
				referenceRunStep(t, p.ctx, c, b.recorder, formatEncodingText("SHOW client_encoding", "client_encoding", "UTF8"))
			})
		})
		run(referenceExec("SET SESSION NAMES DEFAULT", 0))
		run(formatEncodingText("SHOW client_encoding", "client_encoding", "UTF8"))
		run(referenceExec("BEGIN", 0))
		run(referenceExec("SET LOCAL NAMES 'UNICODE'", 0))
		run(formatEncodingText("SHOW client_encoding", "client_encoding", "UNICODE"))
		run(referenceExec("COMMIT", 0))
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
					step := referenceImplicitError("SET client_encoding TO '"+value+"'", want)
					step.prepared = prepared
					referenceRunStep(t, p.ctx, c, b.recorder, step)
					referenceRunStep(t, p.ctx, c, b.recorder, formatEncodingText("SHOW client_encoding", "client_encoding", "UTF8"))
				}
			})
		}
		for _, value := range []string{"LATIN1", "SQL_ASCII"} {
			t.Run("transport-exclusion/"+value, func(t *testing.T) {
				// set_config avoids preparing SET with a lib/pq Bind encoding change;
				// only ASCII crosses the transport until UTF8 is restored.
				if b.pg {
					referenceRunStep(t, p.ctx, c, b.recorder, formatEncodingText("SELECT set_config('client_encoding',$1,false)", "set_config", value, value))
					referenceRunStep(t, p.ctx, c, b.recorder, formatEncodingText("SHOW client_encoding", "client_encoding", value))
				} else {
					want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "client encoding \"" + value + "\" is not supported by stuntgres", Detail: "Only UTF8 client encoding is supported."}
					referenceRunStep(t, p.ctx, c, b.recorder, referenceImplicitError("SELECT set_config('client_encoding',$1,false)", want, value))
					referenceRunStep(t, p.ctx, c, b.recorder, formatEncodingText("SHOW client_encoding", "client_encoding", "UTF8"))
				}
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET client_encoding TO UTF8", 0))
			})
		}
		for _, tc := range []struct{ input, bytes string }{{"\x00", "0x00"}, {"\xff", "0xff"}} {
			for _, prepared := range []bool{false, true} {
				t.Run("bad-bind/"+tc.bytes+"/"+map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
					want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22021", Message: `invalid byte sequence for encoding "UTF8": ` + tc.bytes}
					// Native import owns this existing portal continuation; keep
					// the independently pinned PostgreSQL record below explicit.
					want.Context = "unnamed portal parameter $1"
					step := referenceImplicitError("SELECT $1::text AS v", want, tc.input)
					step.selectRows, step.prepared = true, prepared
					if b.pg {
						pg := want
						pg.Context = "unnamed portal parameter $1"
						step.postgresDiagnostic = &pg
					}
					referenceRunStep(t, p.ctx, c, b.recorder, step)
					referenceRunStep(t, p.ctx, c, b.recorder, formatEncodingText("SELECT v FROM fe_unicode WHERE id=1", "v", "é🙂"))
				})
			}
		}
		for _, tc := range []struct{ q, bytes string }{{`SELECT E'\xff'::text`, "0xff"}, {`SELECT E'\000'::text`, "0x00"}} {
			want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22021", Message: `invalid byte sequence for encoding "UTF8": ` + tc.bytes}
			step := referenceErrorDiagnostic(tc.q, want)
			step.selectRows = true
			run(step)
		}
	})
}
