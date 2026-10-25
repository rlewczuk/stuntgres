package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func domainCheckIndexEventScenario(t *testing.T, prepared bool, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, func(fixture.Step))) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "domain_s")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ordinal := 0
			run := func(s fixture.Step) {
				ordinal++
				s.Prepared = prepared
				t.Run(fmt.Sprintf("%02d %s", ordinal, s.Query), func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
				})
				if t.Failed() {
					t.FailNow()
				}
			}
			run(fixture.Exec("CREATE SCHEMA domain_s", 0))
			run(fixture.Exec("CREATE TABLE public.one(v integer)", 0))
			run(fixture.Exec("INSERT INTO public.one VALUES(1)", 1))
			body(t, p, backend, conn, run)
		})
	}
}

func domainCheckIndexProducerDefinition(run func(fixture.Step), lane, comparison string) {
	run(fixture.Exec("CREATE SEQUENCE domain_s.producer_steps", 0))
	run(fixture.Exec("CREATE DOMAIN domain_s.producer_checked AS integer CONSTRAINT producer_guard CHECK(VALUE"+comparison+"nextval('domain_s.producer_steps'))", 0))
	run(fixture.Exec("CREATE TABLE domain_s.producer_target(id integer)", 0))
	definition := "CREATE INDEX producer_index ON domain_s.producer_target"
	if lane == "predicate" {
		definition += "(id) WHERE id::domain_s.producer_checked>0"
	} else {
		definition += "(((id::domain_s.producer_checked)::integer))"
	}
	run(fixture.Exec(definition, 0))
	run(fixture.Exec("CREATE TABLE domain_s.producer_source(id integer)", 0))
	run(fixture.Exec("INSERT INTO domain_s.producer_source VALUES(1),(2)", 2))
}

func domainCheckIndexProducerInsert(route string) string {
	query := "INSERT INTO domain_s.producer_target "
	if route == "values" {
		return query + "VALUES(nextval('domain_s.producer_steps')::integer),(nextval('domain_s.producer_steps')::integer)"
	}
	return query + "SELECT nextval('domain_s.producer_steps')::integer FROM domain_s.producer_source"
}

func TestReferenceDomainCheckIndexProducerDemand(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, lane := range []string{"key", "predicate"} {
			for _, route := range []string{"values", "query"} {
				for _, returning := range []bool{false, true} {
					name := fmt.Sprintf("prepared_%t/%s/%s/returning_%t", prepared, lane, route, returning)
					t.Run(name, func(t *testing.T) {
						domainCheckIndexEventScenario(t, prepared, func(_ *testing.T, _ *fixture.Pair, _ fixture.Backend, _ *sql.Conn, run func(fixture.Step)) {
							domainCheckIndexProducerDefinition(run, lane, "<")
							run(fixture.Exec("BEGIN", 0))
							query := domainCheckIndexProducerInsert(route)
							if returning {
								run(fixture.Select(query+" RETURNING id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
							} else {
								run(fixture.Exec(query, 2))
							}
							// Source1, index2, source3, index4: final count alone
							// cannot establish this consumer-visible interleaving.
							run(fixture.Select("SELECT id FROM domain_s.producer_target ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
							run(fixture.Select("SELECT currval('domain_s.producer_steps') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
							run(fixture.Exec("COMMIT", 0))
							run(fixture.Select("SELECT currval('domain_s.producer_steps') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
						})
					})
				}
			}
		}
	}
}

func TestReferenceDomainCheckIndexProducerFailureDemand(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, lane := range []string{"key", "predicate"} {
			for _, route := range []string{"values", "query"} {
				for _, returning := range []bool{false, true} {
					name := fmt.Sprintf("prepared_%t/%s/%s/returning_%t", prepared, lane, route, returning)
					t.Run(name, func(t *testing.T) {
						domainCheckIndexEventScenario(t, prepared, func(_ *testing.T, _ *fixture.Pair, _ fixture.Backend, _ *sql.Conn, run func(fixture.Step)) {
							domainCheckIndexProducerDefinition(run, lane, ">")
							query := domainCheckIndexProducerInsert(route)
							if returning {
								query += " RETURNING id"
							}
							run(domainCheckFailure(query, "producer_checked", "producer_guard", returning, prepared))
							// First source1/index2 rejects; source3 must not be
							// demanded, including the non-RETURNING route.
							run(fixture.Select("SELECT currval('domain_s.producer_steps') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
							run(fixture.Select("SELECT id FROM domain_s.producer_target", []string{"id"}, []string{"INT4"}, nil))
							run(fixture.Exec("INSERT INTO domain_s.producer_target VALUES(9)", 1))
							run(fixture.Select("SELECT currval('domain_s.producer_steps') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
							run(fixture.Select("SELECT id FROM domain_s.producer_target", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
						})
					})
				}
			}
		}
	}
}

func TestReferenceDomainCheckIndexMutationProducerDemand(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, lane := range []string{"key", "predicate"} {
			for _, failure := range []bool{false, true} {
				for _, returning := range []bool{false, true} {
					t.Run(fmt.Sprintf("prepared_%t/%s/failure_%t/returning_%t", prepared, lane, failure, returning), func(t *testing.T) {
						domainCheckIndexEventScenario(t, prepared, func(_ *testing.T, _ *fixture.Pair, _ fixture.Backend, _ *sql.Conn, run func(fixture.Step)) {
							comparison := "<"
							seed := "0"
							if failure {
								comparison, seed = ">", "100"
							}
							domainCheckIndexProducerDefinition(run, lane, comparison)
							run(fixture.Exec("INSERT INTO domain_s.producer_target VALUES("+seed+"),("+seed+")", 2))
							query := "UPDATE domain_s.producer_target SET id=nextval('domain_s.producer_steps')::integer"
							if returning {
								query += " RETURNING id"
							}
							if failure {
								run(domainCheckFailure(query, "producer_checked", "producer_guard", returning, prepared))
								run(fixture.Select("SELECT id FROM domain_s.producer_target ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(100)}, {int64(100)}}))
								run(fixture.Select("SELECT currval('domain_s.producer_steps') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
							} else {
								if returning {
									s := fixture.Select(query, []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(5)}})
									s.Bag = true
									run(s)
								} else {
									run(fixture.Exec(query, 2))
								}
								run(fixture.Select("SELECT id FROM domain_s.producer_target ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(5)}}))
								run(fixture.Select("SELECT currval('domain_s.producer_steps') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}}))
							}
						})
					})
				}
			}
		}
	}
}

func TestReferenceDomainCheckIndexMergeProducerDemand(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, lane := range []string{"key", "predicate"} {
			for _, failure := range []bool{false, true} {
				t.Run(fmt.Sprintf("prepared_%t/%s/failure_%t", prepared, lane, failure), func(t *testing.T) {
					domainCheckIndexEventScenario(t, prepared, func(_ *testing.T, _ *fixture.Pair, backend fixture.Backend, _ *sql.Conn, run func(fixture.Step)) {
						comparison := "<"
						if failure {
							comparison = ">"
						}
						domainCheckIndexProducerDefinition(run, lane, comparison)
						query := "MERGE INTO domain_s.producer_target AS t USING domain_s.producer_source AS s ON t.id=s.id WHEN NOT MATCHED THEN INSERT(id) VALUES(nextval('domain_s.producer_steps')::integer)"
						if failure {
							run(domainCheckFailure(query, "producer_checked", "producer_guard", false, prepared))
							run(fixture.Select("SELECT id FROM domain_s.producer_target", []string{"id"}, []string{"INT4"}, nil))
							run(fixture.Select("SELECT currval('domain_s.producer_steps') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
						} else {
							// Preserve fixture.MergeRun's pq command-tag contract
							// while currval stays on this same physical owner.
							count := int64(2)
							if backend.PG {
								count = 0
							}
							run(fixture.Exec(query, count))
							run(fixture.Select("SELECT id FROM domain_s.producer_target ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
							run(fixture.Select("SELECT currval('domain_s.producer_steps') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
						}
					})
				})
			}
		}
	}
}

func TestReferenceDomainCheckIndexReturningProducerDemand(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, lane := range []string{"key", "predicate"} {
			for _, route := range []string{"values", "query"} {
				t.Run(fmt.Sprintf("prepared_%t/%s/%s", prepared, lane, route), func(t *testing.T) {
					domainCheckIndexEventScenario(t, prepared, func(_ *testing.T, _ *fixture.Pair, _ fixture.Backend, _ *sql.Conn, run func(fixture.Step)) {
						domainCheckIndexProducerDefinition(run, lane, "<")
						run(fixture.Exec("BEGIN", 0))
						query := domainCheckIndexProducerInsert(route) + " RETURNING id,nextval('domain_s.producer_steps') AS returned"
						run(fixture.Select(query, []string{"id", "returned"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(3)}, {int64(4), int64(6)}}))
						run(fixture.Select("SELECT id FROM domain_s.producer_target ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}}))
						run(fixture.Select("SELECT currval('domain_s.producer_steps') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}}))
						run(fixture.Exec("COMMIT", 0))
						run(fixture.Select("SELECT currval('domain_s.producer_steps') AS n FROM public.one", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}}))
					})
				})
			}
		}
	}
}
