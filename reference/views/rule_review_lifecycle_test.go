package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func ruleReviewLifecycleRoutes(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, bool)) {
	t.Helper()
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held=%v", held), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) { body(t, p, b, held) })
			}
		})
	}
}

func ruleReviewHeld(t *testing.T, p *fixture.Pair, conn *sql.Conn, q string, held bool) fixture.Executor {
	t.Helper()
	if !held {
		return conn
	}
	stmt, err := conn.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stmt.Close() })
	return fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
}

func TestReferenceRuleReviewResultSignature(t *testing.T) {
	ruleReviewLifecycleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE review_signature_src(id INT,v INT)", 0))
		run(fixture.Exec("CREATE TABLE review_signature_sink(id INT,v INT,extra INT)", 0))
		q := "INSERT INTO review_signature_src(id,v) VALUES($1,$2) RETURNING *"
		// Retain the actual consumer before installing the rule and changing layout.
		executor := ruleReviewHeld(t, p, conn, q, held)
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}, 1, 10))
		run(fixture.Exec("ALTER TABLE review_signature_src ADD COLUMN extra INT", 0))
		run(fixture.Exec("CREATE RULE redirect AS ON INSERT TO review_signature_src DO INSTEAD INSERT INTO review_signature_sink VALUES(NEW.id,NEW.v,99) RETURNING id,v,extra", 0))
		if held {
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "cached plan must not change result type"}, 2, 20))
		} else {
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"id", "v", "extra"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(20), int64(99)}}, 2, 20))
		}
		run(fixture.Select("SELECT id,v,extra FROM review_signature_src", []string{"id", "v", "extra"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), nil}}))
		var sink [][]any
		if !held {
			sink = [][]any{{int64(2), int64(20), int64(99)}}
		}
		run(fixture.Select("SELECT id,v,extra FROM review_signature_sink", []string{"id", "v", "extra"}, []string{"INT4", "INT4", "INT4"}, sink))
		// A fresh consumer remains usable after rejection; rejection cannot write.
		run(fixture.Select(q, []string{"id", "v", "extra"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(3), int64(30), int64(99)}}, 3, 30))
	})
}

// Delivers a real complete rule-bearing stream in small chunks, then fails or
// cancels instead of returning EOF. The marker proves the failure is not precancel.
type ruleReviewReader struct {
	payload []byte
	read    int
	calls   int
	failure error
	cancel  context.CancelFunc
}

func (r *ruleReviewReader) Read(dst []byte) (int, error) {
	r.calls++
	if r.read == len(r.payload) {
		if r.cancel != nil {
			r.cancel()
		}
		return 0, r.failure
	}
	n := min(len(dst), 17, len(r.payload)-r.read)
	copy(dst, r.payload[r.read:r.read+n])
	r.read += n
	return n, nil
}

func TestReferenceRuleReviewReplacementLifetime(t *testing.T) {
	ruleReviewLifecycleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE review_lifetime_src(id INT,v INT)", 0),
			fixture.Exec("CREATE TABLE review_lifetime_sink(id INT,v INT)", 0),
			fixture.Exec("CREATE RULE lifetime_audit AS ON INSERT TO review_lifetime_src DO INSERT INTO review_lifetime_sink VALUES(NEW.id,NEW.v)", 0),
			fixture.Exec("INSERT INTO review_lifetime_src VALUES(1,10)", 1),
		} {
			run(s)
		}
		q := "INSERT INTO review_lifetime_src VALUES($1,$2) RETURNING id,v"
		executor := ruleReviewHeld(t, p, conn, q, held)
		if !b.PG {
			var before bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
			var oid int64
			if err := conn.QueryRowContext(p.Ctx, "SELECT oid FROM pg_rewrite WHERE rulename='lifetime_audit'").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			for _, canceled := range []bool{false, true} {
				ctx, cancel := context.WithCancel(p.Ctx)
				failure := errors.New("rule-bearing restore reader failed after payload")
				reader := &ruleReviewReader{payload: before.Bytes(), failure: failure}
				want := error(failure)
				if canceled {
					reader.cancel = cancel
					reader.failure = io.EOF
					want = context.Canceled
				}
				err := stuntgres.Restore(ctx, p.Native, reader)
				cancel()
				if !errors.Is(err, want) || reader.read != before.Len() || reader.calls < 2 {
					t.Fatalf("midstream restore: error=%v want=%v delivered=%d/%d calls=%d", err, want, reader.read, before.Len(), reader.calls)
				}
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatalf("failed restore changed persistent snapshot: %v", err)
				}
				var afterOID int64
				if err := conn.QueryRowContext(p.Ctx, "SELECT oid FROM pg_rewrite WHERE rulename='lifetime_audit'").Scan(&afterOID); err != nil || afterOID != oid {
					t.Fatalf("failed restore changed rule identity: %d -> %d: %v", oid, afterOID, err)
				}
			}
		}
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}}, 2, 20))
		if !b.PG {
			rows, err := executor.QueryContext(p.Ctx, q, 3, 30)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			// Eager RETURNING publishes before replacement, but its unread rows
			// remain owned by this consumer rather than the replacement catalog.
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString("CREATE TABLE review_lifetime_src(id INT,v INT); CREATE TABLE review_lifetime_sink(id INT,v INT); CREATE RULE lifetime_audit AS ON INSERT TO review_lifetime_src DO INSERT INTO review_lifetime_sink VALUES(NEW.id,NEW.v+100); INSERT INTO review_lifetime_sink VALUES(9,90);")); err != nil {
				t.Fatal(err)
			}
			fixture.DeleteReferenceConsume(t, false, rows, nil, ruleInts("", []any{int64(3), int64(30)}))
			run(ruleInts("SELECT id,v FROM review_lifetime_src"))
			run(ruleInts("SELECT id,v FROM review_lifetime_sink", []any{int64(9), int64(90)}))
		} else {
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(30)}}, 3, 30))
			run(ruleInts("SELECT id,v FROM review_lifetime_sink ORDER BY id", []any{int64(1), int64(10)}, []any{int64(2), int64(20)}, []any{int64(3), int64(30)}))
		}
	})
}

func TestReferenceRuleReviewTemporaryProvenance(t *testing.T) {
	ruleReviewLifecycleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE review_provenance_public(id INT,v INT)", 0),
			fixture.Exec("CREATE TEMP TABLE review_provenance_temp(id INT,v INT)", 0),
			fixture.Exec("CREATE RULE persistent_to_temp AS ON INSERT TO review_provenance_public DO INSERT INTO review_provenance_temp VALUES(NEW.id,NEW.v)", 0),
		} {
			run(s)
		}
		q := "INSERT INTO review_provenance_public VALUES($1,$2)"
		executor := ruleReviewHeld(t, p, conn, q, held)
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 1, 1, 10))
		if !b.PG {
			var refused bytes.Buffer
			err := stuntgres.Dump(p.Ctx, p.Native, &refused)
			if err == nil || err.Error() != "stuntgres: dump cannot serialize a rule referencing a temporary relation" || refused.Len() != 0 {
				t.Fatalf("persistent-to-TEMP export: %v bytes=%d", err, refused.Len())
			}
		}
		run(fixture.Exec("DROP RULE persistent_to_temp ON review_provenance_public", 0))
		run(fixture.Exec("CREATE RULE temp_to_public AS ON INSERT TO review_provenance_temp DO INSERT INTO review_provenance_public VALUES(NEW.id,NEW.v+1)", 0))
		q = "INSERT INTO review_provenance_temp VALUES($1,$2)"
		executor = ruleReviewHeld(t, p, conn, q, held)
		var ruleOID, targetOID int64
		if err := conn.QueryRowContext(p.Ctx, "SELECT oid FROM pg_rewrite WHERE rulename='temp_to_public'").Scan(&ruleOID); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='review_provenance_public'").Scan(&targetOID); err != nil {
			t.Fatal(err)
		}
		checkEdge := func() {
			run(fixture.Select("SELECT refobjsubid FROM pg_depend WHERE classid=2618 AND objid=$1 AND refclassid=1259 AND refobjid=$2 AND deptype='n' ORDER BY refobjsubid", []string{"refobjsubid"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}, ruleOID, targetOID))
		}
		checkEdge()
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			for _, replace := range []func() error{
				func() error { return stuntgres.Reset(p.Ctx, p.Native) },
				func() error { return stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())) },
			} {
				err := replace()
				if err == nil || err.Error() != "stuntgres: catalog replacement cannot preserve temporary rule \"temp_to_public\" dependencies" {
					t.Fatalf("TEMP dependency replacement refusal: %v", err)
				}
				checkEdge()
				var current int64
				if err := conn.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='review_provenance_public'").Scan(&current); err != nil || current != targetOID {
					t.Fatalf("TEMP dependency provider retargeted: %d -> %d: %v", targetOID, current, err)
				}
			}
		}
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 1, 2, 20))
		run(ruleInts("SELECT id,v FROM review_provenance_public ORDER BY id", []any{int64(1), int64(10)}, []any{int64(2), int64(21)}))
		run(ruleInts("SELECT id,v FROM review_provenance_temp ORDER BY id", []any{int64(1), int64(10)}, []any{int64(2), int64(20)}))
	})
}

func TestReferenceRuleReviewTemporaryCollision(t *testing.T) {
	ruleReviewLifecycleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TEMP TABLE review_collision_src(id INT,v INT)", 0),
			fixture.Exec("CREATE TEMP TABLE review_collision_sink(id INT,v INT)", 0),
			fixture.Exec("CREATE RULE retained_audit AS ON INSERT TO review_collision_src DO INSERT INTO review_collision_sink VALUES(NEW.id,NEW.v+1)", 0),
		} {
			run(s)
		}
		q := "INSERT INTO review_collision_src VALUES($1,$2)"
		executor := ruleReviewHeld(t, p, conn, q, held)
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 1, 1, 10))
		var srcOID, sinkOID, retainedOID int64
		for name, destination := range map[string]*int64{"review_collision_src": &srcOID, "review_collision_sink": &sinkOID} {
			if err := conn.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname=$1", name).Scan(destination); err != nil {
				t.Fatal(err)
			}
		}
		if err := conn.QueryRowContext(p.Ctx, "SELECT oid FROM pg_rewrite WHERE rulename='retained_audit'").Scan(&retainedOID); err != nil {
			t.Fatal(err)
		}
		if !b.PG {
			// Build real source catalogs until an ordinary-rule identity overlaps
			// an observed live TEMP identity. No numeric allocator golden is used.
			var source *fixture.Pair
			var imported string
			for padding := range 8 {
				if source != nil {
					break
				}
				candidate := fixture.NewFixture(t, false, true)
				for i := range padding {
					fixture.RunStep(t, p.Ctx, candidate.Memory, candidate.MemoryRecord, fixture.Exec(fmt.Sprintf("CREATE SCHEMA review_padding_%d", i), 0))
				}
				for i := range 24 {
					name := fmt.Sprintf("review_imported_%d", i)
					for _, s := range []fixture.Step{
						fixture.Exec("CREATE TABLE "+name+"(id INT,v INT)", 0),
						fixture.Exec("CREATE RULE imported_audit AS ON INSERT TO "+name+" DO INSTEAD NOTHING", 0),
					} {
						fixture.RunStep(t, p.Ctx, candidate.Memory, candidate.MemoryRecord, s)
					}
					var oid int64
					if err := candidate.Memory.QueryRowContext(p.Ctx, "SELECT r.oid FROM pg_rewrite r JOIN pg_class c ON c.oid=r.ev_class WHERE c.relname=$1 AND r.rulename='imported_audit'", name).Scan(&oid); err != nil {
						t.Fatal(err)
					}
					if oid == srcOID || oid == sinkOID || oid == retainedOID {
						source, imported = candidate, name
						break
					}
				}
			}
			if source == nil {
				t.Fatal("fixture did not establish ordinary-rule OID collision against live TEMP")
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, source.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("INSERT INTO "+imported+" VALUES(7,70)", 0))
			run(ruleInts("SELECT id,v FROM " + imported))
			var importedOID int64
			if err := conn.QueryRowContext(p.Ctx, "SELECT r.oid FROM pg_rewrite r JOIN pg_class c ON c.oid=r.ev_class WHERE c.relname=$1 AND r.rulename='imported_audit'", imported).Scan(&importedOID); err != nil {
				t.Fatal(err)
			}
			if importedOID == srcOID || importedOID == sinkOID || importedOID == retainedOID {
				t.Fatalf("imported ordinary rule kept colliding identity %d", importedOID)
			}
			var after int64
			if err := conn.QueryRowContext(p.Ctx, "SELECT oid FROM pg_rewrite WHERE rulename='retained_audit'").Scan(&after); err != nil || after != retainedOID {
				t.Fatalf("retained TEMP rule identity changed: %d -> %d: %v", retainedOID, after, err)
			}
			run(fixture.Select("SELECT count(*) AS n FROM pg_rewrite WHERE oid=$1", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, retainedOID))
			for name, oid := range map[string]int64{"review_collision_src": srcOID, "review_collision_sink": sinkOID} {
				if err := conn.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname=$1", name).Scan(&after); err != nil || after != oid {
					t.Fatalf("retained TEMP relation %s changed: %d -> %d: %v", name, oid, after, err)
				}
			}
		}
		run(fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid=2618 AND objid=$1 AND refclassid=1259 AND refobjid=$2 AND deptype='a'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, retainedOID, srcOID))
		run(fixture.Select("SELECT refobjsubid FROM pg_depend WHERE classid=2618 AND objid=$1 AND refclassid=1259 AND refobjid=$2 AND deptype='n' ORDER BY refobjsubid", []string{"refobjsubid"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}, retainedOID, sinkOID))
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 1, 2, 20))
		run(ruleInts("SELECT id,v FROM review_collision_src ORDER BY id", []any{int64(1), int64(10)}, []any{int64(2), int64(20)}))
		run(ruleInts("SELECT id,v FROM review_collision_sink ORDER BY id", []any{int64(1), int64(11)}, []any{int64(2), int64(21)}))
	})
}
