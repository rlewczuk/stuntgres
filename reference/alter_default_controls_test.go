//go:build reference

package reference_test

import (
	"bytes"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceAlterDefaultControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, populated := range []bool{false, true} {
			for _, drop := range []bool{false, true} {
				t.Run(fmt.Sprintf("prepared=%v/populated=%v/drop=%v", prepared, populated, drop), func(t *testing.T) {
					p := newReferencePair(t)
					run := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
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
					if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE d"); err != nil {
						t.Fatal(err)
					}
					if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
						t.Fatal("PostgreSQL replay", err)
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

// Native INHERITS remains explicitly unsupported. These independent release
// witnesses exercise the real PostgreSQL descendant contract, not native parity.
func TestReferenceAlterDefaultInheritanceRecursion(t *testing.T) {
	p := newReferencePair(t)
	record := &referenceRecorder{}
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, p.postgres, record, s) }
	run(referenceExec("CREATE TABLE p(id INT,i INT DEFAULT 1)", 0))
	run(referenceExec("CREATE TABLE c(i INT DEFAULT 9) INHERITS(p)", 0))
	run(referenceExec("INSERT INTO p VALUES(0,1)", 1))
	run(referenceExec("INSERT INTO c VALUES(10,9)", 1))
	run(referenceExec("ALTER TABLE p ALTER i SET DEFAULT 7", 0))
	run(referenceExec("INSERT INTO p(id) VALUES(1)", 1))
	run(referenceExec("INSERT INTO c(id) VALUES(11)", 1))
	run(referenceExec("ALTER TABLE ONLY p ALTER i SET DEFAULT 8", 0))
	run(referenceExec("INSERT INTO p(id) VALUES(2)", 1))
	run(referenceExec("INSERT INTO c(id) VALUES(12)", 1))
	run(referenceExec("ALTER TABLE p ALTER i DROP DEFAULT", 0))
	run(referenceExec("INSERT INTO p(id) VALUES(3)", 1))
	run(referenceExec("INSERT INTO c(id) VALUES(13)", 1))
	run(referenceExec("ALTER TABLE p ALTER i SET DEFAULT 6", 0))
	run(referenceExec("ALTER TABLE ONLY p ALTER i DROP DEFAULT", 0))
	run(referenceExec("INSERT INTO p(id) VALUES(4)", 1))
	run(referenceExec("INSERT INTO c(id) VALUES(14)", 1))
	run(referenceSelect("SELECT * FROM ONLY p ORDER BY id", []string{"id", "i"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(1)}, {int64(1), int64(7)}, {int64(2), int64(8)}, {int64(3), nil}, {int64(4), nil}}))
	run(referenceSelect("SELECT * FROM ONLY c ORDER BY id", []string{"id", "i"}, []string{"INT4", "INT4"}, [][]any{{int64(10), int64(9)}, {int64(11), int64(7)}, {int64(12), int64(7)}, {int64(13), nil}, {int64(14), int64(6)}}))
	run(referenceImplicitError("ALTER TABLE p ALTER i SET DEFAULT (SELECT 1)", referenceDiagnostic{Code: "0A000", Message: "cannot use subquery in DEFAULT expression"}))
}

func TestReferenceAlterDefaultDeferredControls(t *testing.T) {
	p := newReferencePair(t)
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
	if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE d"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || !bytes.Equal(again.Bytes(), dump.Bytes()) {
		t.Fatal("redump", err)
	}
	p.step(t, referenceExec("INSERT INTO d VALUES(1,99),(2,NULL)", 2))
	p.step(t, referencePrepared(referenceImplicitError("INSERT INTO d VALUES(3,7),(4,DEFAULT)", referenceDiagnostic{Code: "22012", Message: "division by zero"})))
	p.step(t, referenceSelect("SELECT * FROM d ORDER BY id", []string{"id", "i"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(11)}, {int64(1), int64(99)}, {int64(2), nil}}))
}
