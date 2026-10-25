package reference_test

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"stuntgres"
	"stuntgres/reference/fixture"
)

// Same public transport adapter convention as the existing COPY reference group.
func domainCheckCopyFrom(p *fixture.Pair, pg bool, conn *sql.Conn, q string, input io.Reader) (stuntgres.CopyResult, error) {
	if !pg {
		return stuntgres.CopyFrom(p.Ctx, conn, q, input)
	}
	var result stuntgres.CopyResult
	err := conn.Raw(func(raw any) error {
		c, ok := raw.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("unexpected oracle COPY connection %T", raw)
		}
		tag, err := c.Conn().PgConn().CopyFrom(p.Ctx, input, q)
		if err == nil {
			result = stuntgres.CopyResult{tag.RowsAffected(), tag.String()}
		}
		return err
	})
	return result, err
}

func domainCheckBinaryInput(values ...*int32) []byte {
	var b bytes.Buffer
	b.WriteString("PGCOPY\n\xff\r\n\x00")
	binary.Write(&b, binary.BigEndian, int32(0))
	binary.Write(&b, binary.BigEndian, int32(0))
	for _, value := range values {
		binary.Write(&b, binary.BigEndian, int16(1))
		if value == nil {
			binary.Write(&b, binary.BigEndian, int32(-1))
			continue
		}
		binary.Write(&b, binary.BigEndian, int32(4))
		binary.Write(&b, binary.BigEndian, *value)
	}
	binary.Write(&b, binary.BigEndian, int16(-1))
	return b.Bytes()
}

func TestReferenceDomainCheckInputRoutes(t *testing.T) {
	for _, format := range []string{"text", "binary"} {
		for _, null := range []bool{false, true} {
			t.Run(format+map[bool]string{false: "_zero", true: "_null"}[null], func(t *testing.T) {
				domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
					run(fixture.Exec("CREATE DOMAIN domain_s.copy_value AS integer CONSTRAINT valid CHECK (VALUE IS NOT NULL AND VALUE>0)", 0))
					run(fixture.Exec("CREATE TABLE domain_s.copy_subject (v domain_s.copy_value)", 0))
					run(fixture.Exec("INSERT INTO domain_s.copy_subject VALUES (9)", 1))
					db := p.Native
					if b.PG {
						db = p.CopyPostgres
					}
					transport, err := db.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer transport.Close()
					good, bad := int32(1), int32(0)
					var invalid *int32 = &bad
					text := "1\n0\n"
					if null {
						invalid = nil
						text = "1\n\\N\n"
					}
					var input io.Reader = strings.NewReader(text)
					if format == "binary" {
						input = bytes.NewReader(domainCheckBinaryInput(&good, invalid))
					}
					result, err := domainCheckCopyFrom(p, b.PG, transport, "COPY domain_s.copy_subject FROM STDIN WITH (FORMAT "+format+")", input)
					if result != (stuntgres.CopyResult{}) {
						t.Fatalf("failed COPY published result %+v", result)
					}
					want := domainCheckDiagnostic("copy_value", "valid")
					want.Context = "COPY copy_subject, line 2, column v"
					if format == "text" {
						want.Context += ": \"0\""
						if null {
							want.Context = "COPY copy_subject, line 2, column v: null input"
						}
					}
					t.Run("diagnostic", func(t *testing.T) { fixture.AssertDiagnostic(t, err, &want) })
					run(fixture.Select("SELECT v FROM domain_s.copy_subject", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}))
					input = strings.NewReader("2\n")
					good = 2
					if format == "binary" {
						input = bytes.NewReader(domainCheckBinaryInput(&good))
					}
					result, err = domainCheckCopyFrom(p, b.PG, transport, "COPY domain_s.copy_subject FROM STDIN WITH (FORMAT "+format+")", input)
					if err != nil || result != (stuntgres.CopyResult{Count: 1, CommandTag: "COPY 1"}) {
						t.Fatalf("valid COPY result=%+v error=%v", result, err)
					}
					run(fixture.Select("SELECT v FROM domain_s.copy_subject ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(9)}}))
				})
			})
		}
	}
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "members_direct", true: "members_prepared"}[prepared], func(t *testing.T) {
			domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				for _, q := range []string{
					"CREATE DOMAIN domain_s.member AS integer CHECK (VALUE>0)",
					"CREATE DOMAIN domain_s.required_member AS integer CHECK (VALUE IS NOT NULL)",
					"CREATE DOMAIN domain_s.array_value AS integer[] CHECK (VALUE IS NOT NULL)",
					"CREATE TYPE domain_s.wrapper AS (i domain_s.member)",
					"CREATE TYPE domain_s.required_wrapper AS (i domain_s.required_member)",
				} {
					run(fixture.Exec(q, 0))
				}
				for _, s := range []fixture.Step{
					fixture.Select("SELECT ('{1,NULL}'::domain_s.member[])[1] AS first,('{1,NULL}'::domain_s.member[])[2] AS second FROM public.one", []string{"first", "second"}, []string{"INT4", "INT4"}, [][]any{{int64(1), nil}}),
					fixture.Select("SELECT ('(1)'::domain_s.wrapper).i AS i,('()'::domain_s.wrapper).i AS n FROM public.one", []string{"i", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), nil}}),
					fixture.Select("SELECT ('{1,NULL}'::domain_s.array_value)[1] AS i FROM public.one", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}}),
				} {
					s.Prepared = prepared
					run(s)
				}
				for _, tc := range []struct {
					q, domain, constraint string
					args                  []any
				}{
					{"SELECT $1::domain_s.member[] AS v FROM public.one", "member", "member_check", []any{"{1,0}"}},
					{"SELECT $1::domain_s.required_member[] AS v FROM public.one", "required_member", "required_member_check", []any{"{1,NULL}"}},
					{"SELECT $1::domain_s.wrapper AS v FROM public.one", "member", "member_check", []any{"(0)"}},
					{"SELECT $1::domain_s.required_wrapper AS v FROM public.one", "required_member", "required_member_check", []any{"()"}},
					{"SELECT $1::domain_s.array_value AS v FROM public.one", "array_value", "array_value_check", []any{nil}},
				} {
					t.Run(tc.q, func(t *testing.T) {
						s := domainCheckFailure(tc.q, tc.domain, tc.constraint, true, prepared, tc.args...)
						s.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
						if tc.args[0] == nil {
							s.Diagnostic.Context = "unnamed portal parameter $1"
						}
						fixture.RunStep(t, p.Ctx, c, r, s)
					})
				}
			})
		})
	}
}
