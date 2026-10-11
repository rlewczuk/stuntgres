package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceUpdateCoreFloatIntegerCompanions(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,f DOUBLE PRECISION,n SMALLINT,i INT,b BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,2.5,1,1,1),(2,-2.5,1,1,1),(3,3.5,1,1,1),(4,-3.5,1,1,1)", 4))
	p.Step(t, fixture.Prepared(fixture.Exec("UPDATE t SET n=f,i=f,b=f", 4)))
	snapshot := fixture.Select("SELECT id,n,i,b FROM t ORDER BY id", []string{"id", "n", "i", "b"}, []string{"INT4", "INT2", "INT4", "INT8"}, [][]any{{int64(1), int64(2), int64(2), int64(2)}, {int64(2), int64(-2), int64(-2), int64(-2)}, {int64(3), int64(4), int64(4), int64(4)}, {int64(4), int64(-4), int64(-4), int64(-4)}})
	p.Step(t, snapshot)
	p.Step(t, fixture.Exec("UPDATE t SET n=1", 4))
	snapshot.Rows = [][]any{{int64(1), int64(1), int64(2), int64(2)}, {int64(2), int64(1), int64(-2), int64(-2)}, {int64(3), int64(1), int64(4), int64(4)}, {int64(4), int64(1), int64(-4), int64(-4)}}
	for _, input := range []string{"32767.5", "NaN", "Infinity", "-Infinity"} {
		p.Step(t, fixture.Exec("UPDATE t SET f='"+input+"' WHERE id=4", 1))
		p.Step(t, fixture.ImplicitError("UPDATE t SET n=f", fixture.Diagnostic{Code: "22003", Message: "smallint out of range"}))
		p.Step(t, snapshot)
	}
}

func TestReferenceUpdateCoreDestinationParameters(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INT,src INT,s TEXT,u UUID,tm TIME(2),ts TIMESTAMP(2),n NUMERIC(3,1),fn NUMERIC)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,7,'old',NULL,NULL,NULL,1.0,1.25),(2,-7,'old',NULL,NULL,NULL,1.0,-1.25)", 2))
			q := "UPDATE t SET s=src,u=$1,tm=$2,ts=$3,n=fn"
			uuid := "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"
			var clock any = "04:05:06.13"
			if backend.PG {
				clock = time.Date(0, 1, 1, 4, 5, 6, 130000000, time.UTC)
			}
			stamp := time.Date(2024, 1, 2, 3, 4, 5, 130000000, time.UTC)
			check := func(u, tm, ts any) {
				raw, err := backend.DB.QueryContext(p.Ctx, "SELECT u,tm,ts FROM t ORDER BY id")
				if err != nil {
					t.Fatal(err)
				}
				defer raw.Close()
				metadata, err := raw.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				scan := reflect.TypeFor[string]()
				if backend.PG {
					scan = reflect.TypeFor[time.Time]()
				}
				if len(metadata) != 3 || metadata[1].DatabaseTypeName() != "TIME" || metadata[1].ScanType() != scan || metadata[2].DatabaseTypeName() != "TIMESTAMP" || metadata[2].ScanType() != reflect.TypeFor[time.Time]() {
					t.Fatal(metadata)
				}
				for _, c := range metadata[1:] {
					if _, known := c.Length(); known {
						t.Fatal("unexpected length", c)
					}
					nullable, known := c.Nullable()
					if backend.PG {
						if known {
							t.Fatal(nullable, known)
						}
					} else if !known || !nullable {
						t.Fatal(nullable, known)
					}
				}
				rawUUID := u
				if backend.PG && u != nil {
					rawUUID = []byte(u.(string))
				}
				count := 0
				for raw.Next() {
					var ru, rtm, rts any
					if err := raw.Scan(&ru, &rtm, &rts); err != nil {
						t.Fatal(err)
					}
					timeMatches, stampMatches := reflect.DeepEqual(rtm, tm), reflect.DeepEqual(rts, ts)
					if want, ok := tm.(time.Time); ok {
						got, ok := rtm.(time.Time)
						timeMatches = ok && got.Equal(want)
					}
					if want, ok := ts.(time.Time); ok {
						got, ok := rts.(time.Time)
						stampMatches = ok && got.Equal(want)
					}
					if !reflect.DeepEqual(ru, rawUUID) || !timeMatches || !stampMatches {
						t.Fatal("raw destination", ru, rtm, rts)
					}
					count++
				}
				if count != 2 || raw.Err() != nil {
					t.Fatal(count, raw.Err())
				}
				if err := raw.Close(); err != nil {
					t.Fatal(err)
				}
				run(fixture.Select("SELECT id,s,u,tm,ts,n FROM t ORDER BY id", []string{"id", "s", "u", "tm", "ts", "n"}, []string{"INT4", "TEXT", "UUID", "TIME", "TIMESTAMP", "NUMERIC"}, [][]any{{int64(1), "7", u, tm, ts, "1.3"}, {int64(2), "-7", u, tm, ts, "-1.3"}}))
			}
			args := []any{uuid, "04:05:06.126", "2024-01-02 03:04:05.126"}
			run(fixture.Exec(q, 2, args...))
			check(uuid, clock, stamp)
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			result, err := stmt.ExecContext(p.Ctx, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			fixture.LockAffected(t, result, 2)
			check(nil, nil, nil)
			result, err = stmt.ExecContext(p.Ctx, args...)
			if err != nil {
				t.Fatal(err)
			}
			fixture.LockAffected(t, result, 2)
			check(uuid, clock, stamp)
			run(fixture.Exec("UPDATE t SET fn=99.95 WHERE id=2", 1))
			late, err := backend.DB.PrepareContext(p.Ctx, "UPDATE t SET n=fn,s='discard'")
			if err != nil {
				t.Fatal(err)
			}
			defer late.Close()
			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "numeric field overflow", Detail: "A field with precision 3, scale 1 must round to an absolute value less than 10^2."}
			for _, prepared := range []bool{false, true} {
				if prepared {
					result, err = late.ExecContext(p.Ctx)
				} else {
					result, err = backend.DB.ExecContext(p.Ctx, "UPDATE t SET n=fn,s='discard'")
				}
				if result != nil {
					t.Fatal("failed assignment returned result")
				}
				fixture.AssertDiagnostic(t, err, &want)
				check(uuid, clock, stamp)
			}
		})
	}
}
