package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func stringAggHeldRows(t *testing.T, rows *sql.Rows, want string) {
	t.Helper()
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	var got string
	if err := rows.Scan(&got); err != nil || got != want {
		t.Fatalf("held value=%q want=%q err=%v", got, want, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceStringAggConsumers(t *testing.T) {
	for _, plain := range []bool{false, true} {
		t.Run(map[bool]string{false: "context", true: "plain"}[plain], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) {
						s.NonContext = plain
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					}
					stringAggSetup(run)
					run(fixture.Exec("CREATE TABLE consumed(v TEXT,b BYTEA)", 0))
					for _, prepared := range []bool{false, true} {
						s := fixture.Exec("INSERT INTO consumed SELECT string_agg(v,d ORDER BY id),string_agg(b,s ORDER BY id) FROM sa", 1)
						s.Prepared = prepared
						run(s)
						s = fixture.Exec("INSERT INTO consumed SELECT string_agg(v,d),string_agg(b,s) FROM empty_sa GROUP BY k", 0)
						s.Prepared = prepared
						run(s)
					}
					run(fixture.Select("SELECT v,b FROM consumed", []string{"v", "b"}, []string{"TEXT", "BYTEA"}, [][]any{{"a!b/c", string([]byte{0, 0, 255, 92, 128})}, {"a!b/c", string([]byte{0, 0, 255, 92, 128})}}))
					const q = "SELECT string_agg(v,$1 ORDER BY id) FROM sa"
					var held *sql.Stmt
					var err error
					if plain {
						held, err = backend.DB.Prepare(q)
					} else {
						held, err = backend.DB.PrepareContext(p.Ctx, q)
					}
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					query := func(delimiter string) *sql.Rows {
						var rows *sql.Rows
						var err error
						if plain {
							rows, err = held.Query(delimiter)
						} else {
							rows, err = held.QueryContext(p.Ctx, delimiter)
						}
						if err != nil {
							t.Fatal(err)
						}
						return rows
					}
					old := query(",")
					defer old.Close()
					// Native is eager. Drain PG before borrowing another pool connection,
					// but both assert the same original execution snapshot.
					if backend.PG {
						stringAggHeldRows(t, old, "a,,b,c")
					}
					run(fixture.Exec("UPDATE sa SET v='z',d='?' WHERE id=4", 1))
					stringAggHeldRows(t, query("/"), "a//z/c")
					if !backend.PG {
						stringAggHeldRows(t, old, "a,,b,c")
					}
					stringAggHeldRows(t, query("!"), "a!!z!c")
					run(fixture.Select("SELECT pg_catalog.string_agg(v,d ORDER BY id),\"string_agg\"(v,d ORDER BY id) FROM sa", []string{"string_agg", "string_agg"}, []string{"TEXT", "TEXT"}, [][]any{{"a!?z/c", "a!?z/c"}}))
					run(fixture.Exec("INSERT INTO sa VALUES(2,7,'second','ignored',NULL,NULL)", 1))
					run(fixture.Exec("CREATE TABLE grouped_consumed(k INT,v TEXT,b BYTEA)", 0))
					for _, prepared := range []bool{false, true} {
						s := fixture.Exec("INSERT INTO grouped_consumed SELECT k,string_agg(v,d ORDER BY id),string_agg(b,s ORDER BY id) FROM sa GROUP BY k", 2)
						s.Prepared = prepared
						run(s)
					}
					run(fixture.Select("SELECT k,v,b FROM grouped_consumed ORDER BY k", []string{"k", "v", "b"}, []string{"INT4", "TEXT", "BYTEA"}, [][]any{{int64(1), "a!?z/c", string([]byte{0, 0, 255, 92, 128})}, {int64(1), "a!?z/c", string([]byte{0, 0, 255, 92, 128})}, {int64(2), "second", nil}, {int64(2), "second", nil}}))
				})
			}
		})
	}
}

func TestReferenceStringAggSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			stringAggSetup(run)
			const q = "SELECT string_agg(v,d ORDER BY id),string_agg(v,d ORDER BY id DESC),string_agg(b,s ORDER BY id),string_agg(b,s ORDER BY id DESC) FROM sa"
			s := fixture.Select(q, []string{"string_agg", "string_agg", "string_agg", "string_agg"}, []string{"TEXT", "TEXT", "BYTEA", "BYTEA"}, [][]any{{"a!b/c", "cb!FIRSTa", string([]byte{0, 0, 255, 92, 128}), string([]byte{128, 255, 0, 255, 0})}})
			s.CheckMetadata = stringAggMetadata(backend.PG)
			run(s)
			held, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for range 2 {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: backend.DB, Statement: held, SQL: q}, backend.Recorder, s)
			}
			run(fixture.Exec("CREATE TABLE smoke_out(v TEXT,b BYTEA)", 0))
			run(fixture.Exec("INSERT INTO smoke_out SELECT string_agg(v,d ORDER BY id),string_agg(b,s ORDER BY id) FROM sa", 1))
			run(fixture.Select("SELECT v,b FROM smoke_out", []string{"v", "b"}, []string{"TEXT", "BYTEA"}, [][]any{{"a!b/c", string([]byte{0, 0, 255, 92, 128})}}))
			for _, from := range []string{"empty_sa", "sa WHERE v IS NULL"} {
				n := fixture.Select("SELECT string_agg(v,d),string_agg(b,s) FROM "+from, []string{"string_agg", "string_agg"}, []string{"TEXT", "BYTEA"}, [][]any{{nil, nil}})
				n.CheckMetadata = stringAggMetadata(backend.PG)
				run(n)
			}
			stringAggViewLifecycle(t, p, backend.PG, backend.DB, backend.Recorder, run)
		})
	}
}

func TestReferenceStringAggVolatileEvaluation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE volatile_sa(id INT,v TEXT)", 0))
			run(fixture.Exec("INSERT INTO volatile_sa VALUES(1,'x'),(2,'x'),(3,NULL)", 3))
			run(fixture.Exec("CREATE SEQUENCE sa_seq", 0))
			// Every delimiter and key occurrence evaluates even for NULL values;
			// duplicate volatile calls cannot share an aggregate slot.
			run(fixture.Select("SELECT string_agg(v,nextval('sa_seq')::text ORDER BY nextval('sa_seq')),string_agg(v,nextval('sa_seq')::text ORDER BY nextval('sa_seq')) FROM volatile_sa", []string{"string_agg", "string_agg"}, []string{"TEXT", "TEXT"}, [][]any{{"x5x", "x7x"}}))
			run(fixture.Select("SELECT currval('sa_seq')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(12)}}))
		})
	}
}
