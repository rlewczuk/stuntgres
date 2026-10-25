package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDomainCheckIndexCopyProducerDemand(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, format := range []string{"text", "binary"} {
			for _, lane := range []string{"key", "predicate"} {
				for _, failure := range []bool{false, true} {
					t.Run(fmt.Sprintf("prepared_setup_%t/%s/%s/failure_%t", prepared, format, lane, failure), func(t *testing.T) {
						domainCheckIndexEventScenario(t, prepared, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, _ *sql.Conn, run func(fixture.Step)) {
							run(fixture.Exec("CREATE SEQUENCE domain_s.copy_event_steps", 0))
							comparison := "<"
							if failure {
								comparison = ">"
							}
							run(fixture.Exec("CREATE DOMAIN domain_s.copy_event_checked AS integer CONSTRAINT copy_event_guard CHECK(VALUE"+comparison+"nextval('domain_s.copy_event_steps'))", 0))
							run(fixture.Exec("CREATE TABLE domain_s.copy_event_target(id integer DEFAULT nextval('domain_s.copy_event_steps'), payload integer)", 0))
							definition := "CREATE INDEX copy_event_index ON domain_s.copy_event_target"
							if lane == "predicate" {
								definition += "(id) WHERE id::domain_s.copy_event_checked>0"
							} else {
								definition += "(((id::domain_s.copy_event_checked)::integer))"
							}
							run(fixture.Exec(definition, 0))
							db := p.Native
							if backend.PG {
								db = p.CopyPostgres
							}
							transport, err := db.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer transport.Close()
							a, b := int32(7), int32(8)
							var input io.Reader = strings.NewReader("7\n8\n")
							if format == "binary" {
								input = bytes.NewReader(domainCheckBinaryInput(&a, &b))
							}
							query := "COPY domain_s.copy_event_target(payload) FROM STDIN WITH (FORMAT " + format + ")"
							result, err := domainCheckCopyFrom(p, backend.PG, transport, query, input)
							var effects int64
							if failure {
								if result != (stuntgres.CopyResult{}) {
									t.Fatalf("failed COPY published result %+v", result)
								}
								want := domainCheckDiagnostic("copy_event_checked", "copy_event_guard")
								// The error belongs to insertion at the first buffered
								// tuple, not physical input of payload or the final line.
								want.Context = "COPY copy_event_target, line 1"
								t.Run("COPY insertion diagnostic", func(t *testing.T) {
									fixture.AssertDiagnostic(t, err, &want)
								})
								if t.Failed() {
									t.FailNow()
								}
								effects = 3
								run(fixture.Select("SELECT id,payload FROM domain_s.copy_event_target", []string{"id", "payload"}, []string{"INT4", "INT4"}, nil))
							} else {
								if err != nil || result != (stuntgres.CopyResult{Count: 2, CommandTag: "COPY 2"}) {
									t.Fatalf("COPY insertion result=%+v error=%v", result, err)
								}
								effects = 4
								// nextval alone is COPY-batch-safe in PG18: both
								// defaults precede the genuine per-tuple flush checks.
								run(fixture.Select("SELECT id,payload FROM domain_s.copy_event_target ORDER BY id", []string{"id", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(8)}}))
							}
							var got int64
							if err := transport.QueryRowContext(p.Ctx, "SELECT currval('domain_s.copy_event_steps')").Scan(&got); err != nil || got != effects {
								t.Fatalf("COPY physical-owner effects=%d want=%d error=%v", got, effects, err)
							}
						})
					})
				}
			}
		}
	}
}

func TestReferenceDomainCheckIndexCopyInsertionOrderDemand(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, format := range []string{"text", "binary"} {
			t.Run(fmt.Sprintf("prepared_setup_%t/%s", prepared, format), func(t *testing.T) {
				domainCheckIndexEventScenario(t, prepared, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, _ *sql.Conn, run func(fixture.Step)) {
					run(fixture.Exec("CREATE SEQUENCE domain_s.copy_order_steps", 0))
					run(fixture.Exec("CREATE DOMAIN domain_s.copy_order_first AS integer CONSTRAINT copy_order_first_guard CHECK(VALUE+nextval('domain_s.copy_order_steps')<7)", 0))
					run(fixture.Exec("CREATE DOMAIN domain_s.copy_order_second AS integer CONSTRAINT copy_order_second_guard CHECK(VALUE<nextval('domain_s.copy_order_steps'))", 0))
					run(fixture.Exec("CREATE TABLE domain_s.copy_order_target(id integer DEFAULT nextval('domain_s.copy_order_steps'), payload integer)", 0))
					run(fixture.Exec("CREATE INDEX z_copy_first_created ON domain_s.copy_order_target (((id::domain_s.copy_order_first)::integer))", 0))
					run(fixture.Exec("CREATE INDEX a_copy_second_created ON domain_s.copy_order_target (((id::domain_s.copy_order_second)::integer))", 0))
					db := p.Native
					if backend.PG {
						db = p.CopyPostgres
					}
					transport, err := db.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer transport.Close()
					a, b := int32(7), int32(8)
					var input io.Reader = strings.NewReader("7\n8\n")
					if format == "binary" {
						input = bytes.NewReader(domainCheckBinaryInput(&a, &b))
					}
					result, err := domainCheckCopyFrom(p, backend.PG, transport, "COPY domain_s.copy_order_target(payload) FROM STDIN WITH (FORMAT "+format+")", input)
					if result != (stuntgres.CopyResult{}) {
						t.Fatalf("failed COPY published result %+v", result)
					}
					want := domainCheckDiagnostic("copy_order_first", "copy_order_first_guard")
					want.Context = "COPY copy_order_target, line 2"
					t.Run("second tuple first index diagnostic", func(t *testing.T) { fixture.AssertDiagnostic(t, err, &want) })
					if t.Failed() {
						t.FailNow()
					}
					// Buffered defaults1,2; tuple1/index1=3/index2=4;
					// tuple2/index1=5 rejects before tuple2/index2.
					var got int64
					if err := transport.QueryRowContext(p.Ctx, "SELECT currval('domain_s.copy_order_steps')").Scan(&got); err != nil || got != 5 {
						t.Fatalf("COPY ordered flush effects=%d want=5 error=%v", got, err)
					}
					run(fixture.Select("SELECT id,payload FROM domain_s.copy_order_target", []string{"id", "payload"}, []string{"INT4", "INT4"}, nil))
				})
			})
		}
	}
}

func TestReferenceDomainCheckIndexCopyFlushDemand(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, format := range []string{"text", "binary"} {
			t.Run(fmt.Sprintf("prepared_setup_%t/%s", prepared, format), func(t *testing.T) {
				domainCheckIndexEventScenario(t, prepared, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, _ *sql.Conn, run func(fixture.Step)) {
					run(fixture.Exec("CREATE SEQUENCE domain_s.flush_steps", 0))
					run(fixture.Exec("CREATE DOMAIN domain_s.flush_checked AS integer CHECK(VALUE<nextval('domain_s.flush_steps'))", 0))
					payloadType := "text"
					if format == "binary" {
						payloadType = "integer"
					}
					run(fixture.Exec("CREATE TABLE domain_s.flush_target(id integer DEFAULT nextval('domain_s.flush_steps'), payload "+payloadType+")", 0))
					run(fixture.Exec("CREATE INDEX flush_index ON domain_s.flush_target (((id::domain_s.flush_checked)::integer))", 0))
					db := p.Native
					if backend.PG {
						db = p.CopyPostgres
					}
					transport, err := db.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer transport.Close()
					var input io.Reader
					var expected [][]any
					var effects, count int64
					types := []string{"INT4", "TEXT"}
					if format == "binary" {
						// Binary COPY has no text-byte counter: tuple1000
						// flushes before demanding default for tuple1001.
						value := int32(7)
						values := make([]*int32, 1001)
						expected = make([][]any, 1001)
						for i := range values {
							values[i] = &value
							expected[i] = []any{int64(i + 1), int64(7)}
						}
						expected[1000][0] = int64(2001)
						input = bytes.NewReader(domainCheckBinaryInput(values...))
						effects, count, types[1] = 2002, 1001, "INT4"
					} else {
						// Two32768-byte server raw lines cross65535, so
						// their checks precede the third row's default.
						value := strings.Repeat("x", 32768)
						input = strings.NewReader(value + "\n" + value + "\n" + value + "\n")
						expected = [][]any{{int64(1), value}, {int64(2), value}, {int64(5), value}}
						effects, count = 6, 3
					}
					result, err := domainCheckCopyFrom(p, backend.PG, transport, "COPY domain_s.flush_target(payload) FROM STDIN WITH (FORMAT "+format+")", input)
					want := stuntgres.CopyResult{Count: count, CommandTag: fmt.Sprintf("COPY %d", count)}
					if err != nil || result != want {
						t.Fatalf("COPY finite flush result=%+v want=%+v error=%v", result, want, err)
					}
					var got int64
					if err := transport.QueryRowContext(p.Ctx, "SELECT currval('domain_s.flush_steps')").Scan(&got); err != nil || got != effects {
						t.Fatalf("COPY finite flush effects=%d want=%d error=%v", got, effects, err)
					}
					run(fixture.Select("SELECT id,payload FROM domain_s.flush_target ORDER BY id", []string{"id", "payload"}, types, expected))
				})
			})
		}
	}
}
