package reference_test

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterDefaultControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, populated := range []bool{false, true} {
			for _, drop := range []bool{false, true} {
				t.Run(fmt.Sprintf("prepared=%v/populated=%v/drop=%v", prepared, populated, drop), func(t *testing.T) {
					p := fixture.NewScenario(t)
					run := func(s fixture.Step) { s.Prepared = prepared; fixture.DefaultReferenceStep(t, p, s) }
					run(fixture.Exec("CREATE TABLE d(id INT,i INT DEFAULT 11,b BOOL DEFAULT FALSE)", 0))
					var want [][]any
					if populated {
						run(fixture.Exec("INSERT INTO d(id) VALUES(0)", 1))
						want = append(want, []any{int64(0), int64(11), false})
					}
					run(fixture.Exec("ALTER TABLE d ALTER i SET DEFAULT 7", 0))
					run(fixture.Exec("ALTER TABLE d ALTER b SET DEFAULT TRUE OR FALSE", 0))
					var future any = int64(7)
					if drop {
						run(fixture.Exec("ALTER TABLE d ALTER i DROP DEFAULT", 0))
						future = nil
					}
					held, err := p.Native.QueryContext(p.Ctx, "SELECT * FROM d ORDER BY id")
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					for _, script := range []string{"CREATE TABLE bad(i INT DEFAULT 'oops')", "CREATE TABLE partial(i INT);ALTER TABLE d ALTER i SET DEFAULT 99"} {
						if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
							t.Fatal("invalid Restore accepted")
						}
						var same bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &same); err != nil || !bytes.Equal(same.Bytes(), dump.Bytes()) {
							t.Fatal("failed Restore mutated", err)
						}
					}
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					columns, err := held.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					fixture.TableDDLColumnTypes(t, false, columns, []string{"id", "i", "b"}, []string{"INT4", "INT4", "BOOL"}, []bool{true, true, true})
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
					if p.Postgres != nil {
						if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE d"); err != nil {
							t.Fatal(err)
						}
						if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
							t.Fatal("PostgreSQL replay", err)
						}
					}
					var again bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(again.Bytes(), dump.Bytes()) {
						t.Fatal("redump", err)
					}
					run(fixture.Select("SELECT * FROM d ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "BOOL"}, want))
					run(fixture.Exec("INSERT INTO d(id) VALUES(1)", 1))
					run(fixture.Exec("INSERT INTO d VALUES(2,DEFAULT,DEFAULT)", 1))
					run(fixture.Exec("CREATE TABLE source(id INT)", 0))
					run(fixture.Exec("INSERT INTO source VALUES(3)", 1))
					run(fixture.Exec("INSERT INTO d(id) SELECT id FROM source", 1))
					for _, id := range []int64{1, 2, 3} {
						want = append(want, []any{id, future, true})
					}
					run(fixture.Select("SELECT * FROM d ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "BOOL"}, want))
				})
			}
		}
	}
}

func TestReferenceAlterDefaultDeferredControls(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE d(id INT,i INT DEFAULT 11)", 0))
	p.Step(t, fixture.Exec("INSERT INTO d(id) VALUES(0)", 1))
	p.Step(t, fixture.Exec("ALTER TABLE d ALTER i SET DEFAULT 1/0", 0))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE d"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(again.Bytes(), dump.Bytes()) {
		t.Fatal("redump", err)
	}
	p.Step(t, fixture.Exec("INSERT INTO d VALUES(1,99),(2,NULL)", 2))
	p.Step(t, fixture.Prepared(fixture.ImplicitError("INSERT INTO d VALUES(3,7),(4,DEFAULT)", fixture.Diagnostic{Code: "22012", Message: "division by zero"})))
	p.Step(t, fixture.Select("SELECT * FROM d ORDER BY id", []string{"id", "i"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(11)}, {int64(1), int64(99)}, {int64(2), nil}}))
}

func TestReferenceAlterDefaultBooleanGrammar(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.DefaultReferenceStep(t, p, fixture.Exec("CREATE TABLE d(id INT,b BOOL DEFAULT FALSE)", 0))
	fixture.DefaultReferenceStep(t, p, fixture.Exec("INSERT INTO d(id) VALUES(0)", 1))
	for i, expr := range []string{"TRUE OR FALSE", "NOT FALSE", "NULL IS NULL"} {
		fixture.DefaultReferenceStep(t, p, fixture.Exec("ALTER TABLE d ALTER b SET DEFAULT "+expr, 0))
		fixture.DefaultReferenceStep(t, p, fixture.Exec(fmt.Sprintf("INSERT INTO d(id) VALUES(%d)", i+1), 1))
	}
	fixture.DefaultReferenceStep(t, p, fixture.Select("SELECT * FROM d ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOOL"}, [][]any{{int64(0), false}, {int64(1), true}, {int64(2), true}, {int64(3), true}}))
}
