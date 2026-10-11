package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Run PostgreSQL first in a separate subtest: a native failure must not prevent
// the independently specified pinned-release expectation from being exercised.
func defaultConsumerReferenceEach(t *testing.T, p *fixture.Pair, f func(*testing.T, *sql.DB, *fixture.Recorder)) {
	t.Helper()
	for _, b := range []struct {
		name   string
		db     *sql.DB
		record *fixture.Recorder
	}{
		{"postgres-oracle", p.Postgres, p.PostgresRecord},
		{"native", p.Memory, p.MemoryRecord},
	} {
		if b.db == nil {
			continue
		}
		t.Run(b.name, func(t *testing.T) { f(t, b.db, b.record) })
	}
}

func defaultConsumerReferenceRoundtrip(t *testing.T, p *fixture.Pair, witness string, tables ...string) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), witness) {
		t.Errorf("lost default structure %q in %s", witness, dump.String())
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("default restore", err, dump.String())
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || again.String() != dump.String() {
		t.Fatal("default redump", err, again.String(), dump.String())
	}
	if p.Postgres == nil {
		return
	}
	for _, table := range tables {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE "+table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
		t.Fatal("PostgreSQL replay", err, dump.String())
	}
}

func defaultConsumerReferenceControls(t *testing.T, create, witness string, names, types []string, nativeDefault, postgresDefault []any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			row := func(id int64, values []any) []any { return append([]any{id}, values...) }
			nativeRows, postgresRows := [][]any{}, [][]any{}
			for id := int64(1); id <= 3; id++ {
				nativeRows = append(nativeRows, row(id, nativeDefault))
				postgresRows = append(postgresRows, row(id, postgresDefault))
			}
			nulls := make([]any, len(nativeDefault))
			nativeRows = append(nativeRows, row(4, nulls))
			postgresRows = append(postgresRows, row(4, nulls))
			requested := strings.TrimSuffix(strings.Repeat("DEFAULT,", len(nativeDefault)), ",")
			nullSQL := strings.TrimSuffix(strings.Repeat("NULL,", len(nativeDefault)), ",")
			insert := "INSERT INTO d VALUES(3," + requested + "),(4," + nullSQL + ")"
			selectRows := func(want [][]any) fixture.Step {
				return fixture.Select("SELECT * FROM d ORDER BY id", names, types, want)
			}
			if p.Postgres != nil {
				t.Run("postgres-oracle", func(t *testing.T) {
					run := func(s fixture.Step) {
						s.Prepared = prepared
						fixture.DefaultReferenceRunStep(t, p, p.Postgres, p.PostgresRecord, s)
					}
					run(fixture.Exec(create, 0))
					run(selectRows(nil))
					run(fixture.Exec("INSERT INTO d(id) VALUES(1),(2)", 2))
					run(fixture.Exec(insert, 2))
					run(selectRows(postgresRows))
				})
			}
			t.Run("native-controls-and-replay", func(t *testing.T) {
				native := func(s fixture.Step) {
					s.Prepared = prepared
					fixture.DefaultReferenceRunStep(t, p, p.Memory, p.MemoryRecord, s)
				}
				both := func(s fixture.Step) { s.Prepared = prepared; fixture.DefaultReferenceStep(t, p, s) }
				check := func() {
					native(selectRows(nativeRows))
					s := selectRows(postgresRows)
					s.Prepared = prepared
					if p.Postgres != nil {
						fixture.DefaultReferenceRunStep(t, p, p.Postgres, p.PostgresRecord, s)
					}
				}
				native(fixture.Exec(create, 0))
				native(selectRows(nil))
				defaultConsumerReferenceRoundtrip(t, p, witness, "d")
				native(selectRows(nil))
				s := selectRows(nil)
				s.Prepared = prepared
				if p.Postgres != nil {
					fixture.DefaultReferenceRunStep(t, p, p.Postgres, p.PostgresRecord, s)
				}
				both(fixture.Exec("INSERT INTO d(id) VALUES(1),(2)", 2))
				both(fixture.Exec(insert, 2))
				check()
				defaultConsumerReferenceRoundtrip(t, p, witness, "d")
				check()
				both(fixture.Exec("INSERT INTO d(id) VALUES(5)", 1))
				both(fixture.Exec("INSERT INTO d VALUES(6,"+requested+")", 1))
				for id := int64(5); id <= 6; id++ {
					nativeRows = append(nativeRows, row(id, nativeDefault))
					postgresRows = append(postgresRows, row(id, postgresDefault))
				}
				check()
			})
		})
	}
}
