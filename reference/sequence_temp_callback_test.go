package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestReferenceSequencePreparedPeerTemporaryNamespace(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			exec := func(c *sql.Conn, q string) { t.Helper(); sequenceProbeExec(t, p.ctx, c, q) }
			if backend.name == "postgres" {
				defer func() {
					exec(a, "SET search_path TO public")
					exec(a, "DROP SCHEMA IF EXISTS peer_temp_a,peer_temp_b CASCADE")
				}()
			}
			before := map[uint64]string{}
			peerCount := 1
			if backend.name == "postgres" {
				rows, err := a.QueryContext(p.ctx, "SELECT oid,nspname FROM pg_namespace")
				if err != nil {
					t.Fatal(err)
				}
				for rows.Next() {
					var oid uint64
					var name string
					if err := rows.Scan(&oid, &name); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					before[oid] = name
					if strings.HasPrefix(name, "pg_temp_") {
						peerCount++
					}
				}
				if err := rows.Err(); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				rows.Close()
				// Reserve more live backend slots than existing temporary namespaces.
				// At least one slot must insert a namespace, rather than reuse a tuple.
				if peerCount > 64 {
					t.Fatal("namespace insertion fixture exceeds bounded backend reservation", peerCount)
				}
			}
			peers := make([]*sql.Conn, 0, peerCount)
			defer func() {
				for _, c := range peers {
					c.Close()
				}
			}()
			for range peerCount {
				c, err := backend.db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				peers = append(peers, c)
				if backend.name == "postgres" {
					var temp uint64
					if err := c.QueryRowContext(p.ctx, "SELECT pg_my_temp_schema()").Scan(&temp); err != nil || temp != 0 {
						t.Fatal("peer backend already initialized temporary namespace", temp, err)
					}
				}
			}
			for _, q := range []string{"CREATE SCHEMA peer_temp_a", "CREATE SCHEMA peer_temp_b", "CREATE SEQUENCE peer_temp_b.s START 10 CACHE 1", "SET search_path TO peer_temp_a,peer_temp_b"} {
				exec(a, q)
			}
			held, err := a.PrepareContext(p.ctx, "SELECT nextval('s')")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			value := func(want int64) {
				t.Helper()
				referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: a, statement: held, query: "SELECT nextval('s')"}, backend.recorder, sequenceInt64Step(referenceStep{query: "SELECT nextval('s')", selectRows: true, columns: []string{"nextval"}, types: []string{"INT8"}, rows: [][]any{{want}}}))
			}
			value(10)
			exec(a, "CREATE SEQUENCE peer_temp_a.s START 100 CACHE 1")
			value(11)
			inserted := false
			for _, b := range peers {
				for _, q := range []string{"BEGIN", "CREATE TEMP SEQUENCE unrelated_temp", "COMMIT"} {
					exec(b, q)
				}
				if !backend.pg {
					inserted = true
					break
				}
				var oid uint64
				if err := b.QueryRowContext(p.ctx, "SELECT pg_my_temp_schema()").Scan(&oid); err != nil || oid == 0 {
					t.Fatal("missing initialized temporary namespace", oid, err)
				}
				var name string
				if err := a.QueryRowContext(p.ctx, "SELECT nspname FROM pg_namespace WHERE oid=$1", oid).Scan(&name); err != nil || !strings.HasPrefix(name, "pg_temp_") {
					t.Fatal("committed namespace tuple missing", oid, name, err)
				}
				if _, existed := before[oid]; !existed {
					inserted = true
					break
				}
			}
			if !inserted {
				t.Fatal("reserved backend slots did not produce a new pg_namespace tuple")
			}
			value(100)
			sequenceProbeValue(t, p.ctx, a, "SELECT last_value FROM peer_temp_b.s", 11)
			sequenceProbeValue(t, p.ctx, a, "SELECT last_value FROM peer_temp_a.s", 100)
			for _, name := range []string{"peer_temp_a.s", "peer_temp_b.s"} {
				var called bool
				if err := a.QueryRowContext(p.ctx, "SELECT is_called FROM "+name).Scan(&called); err != nil || !called {
					t.Fatal("called state", name, called, err)
				}
			}
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 101)
			sequenceProbeValue(t, p.ctx, a, "SELECT nextval('peer_temp_b.s')", 12)
		})
	}
}

func TestReferenceSequencePreparedTemporaryNoninvalidation(t *testing.T) {
	for _, scenario := range []string{"ExistingNamespace", "PhysicalEnd"} {
		t.Run(scenario, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					a, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer a.Close()
					b, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer b.Close()
					exec := func(c *sql.Conn, q string) { t.Helper(); sequenceProbeExec(t, p.ctx, c, q) }
					if backend.name == "postgres" {
						defer func() {
							exec(a, "SET search_path TO public")
							exec(a, "DROP SCHEMA IF EXISTS temp_stable_a,temp_stable_b CASCADE")
						}()
					}
					exec(b, "CREATE TEMP SEQUENCE initialized_temp")
					var pid int
					var namespace uint64
					var namespaceName string
					if backend.name == "postgres" {
						if err := b.QueryRowContext(p.ctx, "SELECT pg_backend_pid(),pg_my_temp_schema()").Scan(&pid, &namespace); err != nil || namespace == 0 {
							t.Fatal("initialized peer authority", pid, namespace, err)
						}
						if err := a.QueryRowContext(p.ctx, "SELECT nspname FROM pg_namespace WHERE oid=$1", namespace).Scan(&namespaceName); err != nil || !strings.HasPrefix(namespaceName, "pg_temp_") {
							t.Fatal("initialized namespace tuple", namespace, namespaceName, err)
						}
					}
					for _, q := range []string{"CREATE SCHEMA temp_stable_a", "CREATE SCHEMA temp_stable_b", "CREATE SEQUENCE temp_stable_b.s START 10 CACHE 1", "SET search_path TO temp_stable_a,temp_stable_b"} {
						exec(a, q)
					}
					held, err := a.PrepareContext(p.ctx, "SELECT nextval('s')")
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					value := func(want int64) {
						t.Helper()
						referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: a, statement: held, query: "SELECT nextval('s')"}, backend.recorder, sequenceInt64Step(referenceStep{query: "SELECT nextval('s')", selectRows: true, columns: []string{"nextval"}, types: []string{"INT8"}, rows: [][]any{{want}}}))
					}
					value(10)
					exec(a, "CREATE SEQUENCE temp_stable_a.s START 100 CACHE 1")
					value(11)
					if scenario == "ExistingNamespace" {
						exec(b, "CREATE TEMP SEQUENCE another_temp")
						if backend.name == "postgres" {
							var current uint64
							if err := b.QueryRowContext(p.ctx, "SELECT pg_my_temp_schema()").Scan(&current); err != nil || current != namespace {
								t.Fatal("second TEMP create changed namespace identity", current, namespace, err)
							}
						}
					} else {
						backend.db.SetMaxIdleConns(0)
						if err := b.Close(); err != nil {
							t.Fatal(err)
						}
						if backend.name == "postgres" {
							// SQL round trips provide bounded observation of real backend
							// termination; no borrower-return assumption or arbitrary sleep.
							ctx, cancel := context.WithTimeout(p.ctx, 2*time.Second)
							defer cancel()
							for {
								var live bool
								if err := a.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1)", pid).Scan(&live); err != nil {
									t.Fatal("observing physical backend exit", err)
								}
								if !live {
									break
								}
							}
						}
					}
					if backend.name == "postgres" {
						var name string
						if err := a.QueryRowContext(p.ctx, "SELECT nspname FROM pg_namespace WHERE oid=$1", namespace).Scan(&name); err != nil || name != namespaceName {
							t.Fatal("peer lifecycle removed namespace tuple", namespace, name, err)
						}
					}
					value(12)
					for _, state := range []struct {
						name   string
						last   int64
						called bool
					}{{"temp_stable_b.s", 12, true}, {"temp_stable_a.s", 100, false}} {
						var last int64
						var called bool
						if err := a.QueryRowContext(p.ctx, "SELECT last_value,is_called FROM "+state.name).Scan(&last, &called); err != nil || last != state.last || called != state.called {
							t.Fatal("noninvalidation state", state.name, last, called, err)
						}
					}
					sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 100)
					sequenceProbeValue(t, p.ctx, a, "SELECT nextval('temp_stable_b.s')", 13)
				})
			}
		})
	}
}
