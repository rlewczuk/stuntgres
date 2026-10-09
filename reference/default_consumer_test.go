package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

// Run PostgreSQL first in a separate subtest: a native failure must not prevent
// the independently specified pinned-release expectation from being exercised.
func defaultConsumerReferenceEach(t *testing.T, p *referencePair, f func(*testing.T, *sql.DB, *referenceRecorder)) {
	t.Helper()
	for _, b := range []struct {
		name   string
		db     *sql.DB
		record *referenceRecorder
	}{
		{"postgres-oracle", p.postgres, p.postgresRecord},
		{"native", p.memory, p.memoryRecord},
	} {
		if b.db == nil {
			continue
		}
		t.Run(b.name, func(t *testing.T) { f(t, b.db, b.record) })
	}
}

func defaultConsumerReferenceRoundtrip(t *testing.T, p *referencePair, witness string, tables ...string) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), witness) {
		t.Errorf("lost default structure %q in %s", witness, dump.String())
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("default restore", err, dump.String())
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || again.String() != dump.String() {
		t.Fatal("default redump", err, again.String(), dump.String())
	}
	if p.postgres == nil {
		return
	}
	for _, table := range tables {
		if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE "+table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal("PostgreSQL replay", err, dump.String())
	}
}

func defaultConsumerReferenceControls(t *testing.T, create, witness string, names, types []string, nativeDefault, postgresDefault []any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
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
			selectRows := func(want [][]any) referenceStep {
				return referenceSelect("SELECT * FROM d ORDER BY id", names, types, want)
			}
			if p.postgres != nil {
				t.Run("postgres-oracle", func(t *testing.T) {
					run := func(s referenceStep) {
						s.prepared = prepared
						defaultReferenceRunStep(t, p, p.postgres, p.postgresRecord, s)
					}
					run(referenceExec(create, 0))
					run(selectRows(nil))
					run(referenceExec("INSERT INTO d(id) VALUES(1),(2)", 2))
					run(referenceExec(insert, 2))
					run(selectRows(postgresRows))
				})
			}
			t.Run("native-controls-and-replay", func(t *testing.T) {
				native := func(s referenceStep) {
					s.prepared = prepared
					defaultReferenceRunStep(t, p, p.memory, p.memoryRecord, s)
				}
				both := func(s referenceStep) { s.prepared = prepared; defaultReferenceStep(t, p, s) }
				check := func() {
					native(selectRows(nativeRows))
					s := selectRows(postgresRows)
					s.prepared = prepared
					if p.postgres != nil {
						defaultReferenceRunStep(t, p, p.postgres, p.postgresRecord, s)
					}
				}
				native(referenceExec(create, 0))
				native(selectRows(nil))
				defaultConsumerReferenceRoundtrip(t, p, witness, "d")
				native(selectRows(nil))
				s := selectRows(nil)
				s.prepared = prepared
				if p.postgres != nil {
					defaultReferenceRunStep(t, p, p.postgres, p.postgresRecord, s)
				}
				both(referenceExec("INSERT INTO d(id) VALUES(1),(2)", 2))
				both(referenceExec(insert, 2))
				check()
				defaultConsumerReferenceRoundtrip(t, p, witness, "d")
				check()
				both(referenceExec("INSERT INTO d(id) VALUES(5)", 1))
				both(referenceExec("INSERT INTO d VALUES(6,"+requested+")", 1))
				for id := int64(5); id <= 6; id++ {
					nativeRows = append(nativeRows, row(id, nativeDefault))
					postgresRows = append(postgresRows, row(id, postgresDefault))
				}
				check()
			})
		})
	}
}
