package reference_test

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceAlterDefaultControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, populated := range []bool{false, true} {
			for _, drop := range []bool{false, true} {
				t.Run(fmt.Sprintf("prepared=%v/populated=%v/drop=%v", prepared, populated, drop), func(t *testing.T) {
					p := newReferenceScenario(t)
					run := func(s referenceStep) { s.prepared = prepared; defaultReferenceStep(t, p, s) }
					run(referenceExec("CREATE TABLE d(id INT,i INT DEFAULT 11,b BOOL DEFAULT FALSE)", 0))
					var want [][]any
					if populated {
						run(referenceExec("INSERT INTO d(id) VALUES(0)", 1))
						want = append(want, []any{int64(0), int64(11), false})
					}
					run(referenceExec("ALTER TABLE d ALTER i SET DEFAULT 7", 0))
					run(referenceExec("ALTER TABLE d ALTER b SET DEFAULT TRUE OR FALSE", 0))
					var future any = int64(7)
					if drop {
						run(referenceExec("ALTER TABLE d ALTER i DROP DEFAULT", 0))
						future = nil
					}
					held, err := p.native.QueryContext(p.ctx, "SELECT * FROM d ORDER BY id")
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
						t.Fatal(err)
					}
					for _, script := range []string{"CREATE TABLE bad(i INT DEFAULT 'oops')", "CREATE TABLE partial(i INT);ALTER TABLE d ALTER i SET DEFAULT 99"} {
						if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err == nil {
							t.Fatal("invalid Restore accepted")
						}
						var same bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &same); err != nil || !bytes.Equal(same.Bytes(), dump.Bytes()) {
							t.Fatal("failed Restore mutated", err)
						}
					}
					if err := stuntgres.Reset(p.ctx, p.native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					columns, err := held.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					referenceTableDDLColumnTypes(t, false, columns, []string{"id", "i", "b"}, []string{"INT4", "INT4", "BOOL"}, []bool{true, true, true})
					captured := make([][]any, 0)
					for held.Next() {
						row := make([]any, 3)
						if err := held.Scan(&row[0], &row[1], &row[2]); err != nil {
							t.Fatal(err)
						}
						captured = append(captured, row)
					}
					expected := want
					if expected == nil {
						expected = make([][]any, 0)
					}
					if err := held.Err(); err != nil || !reflect.DeepEqual(captured, expected) {
						t.Fatal("retained cursor", captured, expected, err)
					}
					if err := held.Close(); err != nil {
						t.Fatal(err)
					}
					if p.postgres != nil {
						if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE d"); err != nil {
							t.Fatal(err)
						}
						if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
							t.Fatal("PostgreSQL replay", err)
						}
					}
					var again bytes.Buffer
					if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || !bytes.Equal(again.Bytes(), dump.Bytes()) {
						t.Fatal("redump", err)
					}
					run(referenceSelect("SELECT * FROM d ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "BOOL"}, want))
					run(referenceExec("INSERT INTO d(id) VALUES(1)", 1))
					run(referenceExec("INSERT INTO d VALUES(2,DEFAULT,DEFAULT)", 1))
					run(referenceExec("CREATE TABLE source(id INT)", 0))
					run(referenceExec("INSERT INTO source VALUES(3)", 1))
					run(referenceExec("INSERT INTO d(id) SELECT id FROM source", 1))
					for _, id := range []int64{1, 2, 3} {
						want = append(want, []any{id, future, true})
					}
					run(referenceSelect("SELECT * FROM d ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "BOOL"}, want))
				})
			}
		}
	}
}

func TestReferenceAlterDefaultDeferredControls(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE d(id INT,i INT DEFAULT 11)", 0))
	p.step(t, referenceExec("INSERT INTO d(id) VALUES(0)", 1))
	p.step(t, referenceExec("ALTER TABLE d ALTER i SET DEFAULT 1/0", 0))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE d"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || !bytes.Equal(again.Bytes(), dump.Bytes()) {
		t.Fatal("redump", err)
	}
	p.step(t, referenceExec("INSERT INTO d VALUES(1,99),(2,NULL)", 2))
	p.step(t, referencePrepared(referenceImplicitError("INSERT INTO d VALUES(3,7),(4,DEFAULT)", referenceDiagnostic{Code: "22012", Message: "division by zero"})))
	p.step(t, referenceSelect("SELECT * FROM d ORDER BY id", []string{"id", "i"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(11)}, {int64(1), int64(99)}, {int64(2), nil}}))
}

func TestReferenceAlterDefaultBooleanGrammar(t *testing.T) {
	p := newReferenceScenario(t)
	defaultReferenceStep(t, p, referenceExec("CREATE TABLE d(id INT,b BOOL DEFAULT FALSE)", 0))
	defaultReferenceStep(t, p, referenceExec("INSERT INTO d(id) VALUES(0)", 1))
	for i, expr := range []string{"TRUE OR FALSE", "NOT FALSE", "NULL IS NULL"} {
		defaultReferenceStep(t, p, referenceExec("ALTER TABLE d ALTER b SET DEFAULT "+expr, 0))
		defaultReferenceStep(t, p, referenceExec(fmt.Sprintf("INSERT INTO d(id) VALUES(%d)", i+1), 1))
	}
	defaultReferenceStep(t, p, referenceSelect("SELECT * FROM d ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOOL"}, [][]any{{int64(0), false}, {int64(1), true}, {int64(2), true}, {int64(3), true}}))
}
