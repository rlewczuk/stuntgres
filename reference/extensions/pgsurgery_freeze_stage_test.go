package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Repair precedes both producers. The genuine successor owner then blocks the
// original producer before its BEFORE trigger, establishing an ordinary released
// EPQ boundary on the repaired lineage. This is NOT surgery during BEFORE:
// PostgreSQL's UPDATE old-tuple slot retains a buffer pin there, preventing the
// surgery cleanup lock. Exact native final-Stage admission needs separate proof.
func TestReferencePgsurgeryFreezeProducerRecheck(t *testing.T) {
	for _, operation := range []string{"update", "delete"} {
		for _, predicate := range []string{"keep", "reject"} {
			for _, route := range []string{"returning", "affected"} {
				t.Run(operation+"/"+predicate+"/"+route, func(t *testing.T) {
					freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						freezeProducerRecheck(t, p, b, c, operation, predicate == "keep", route == "returning")
					})
				})
			}
		}
	}
}

func freezeProducerRecheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, operation string, keep, returning bool) {
	t.Helper()
	for _, q := range []string{
		"CREATE SEQUENCE fs_part.rhs_seq CACHE 1",
		"CREATE SEQUENCE fs_part.before_seq CACHE 1",
		"CREATE SEQUENCE fs_part.after_seq CACHE 1",
		"CREATE TABLE fs_part.producer_audit(old_value text,new_value text)",
		`CREATE FUNCTION fs_part.producer_before() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF current_setting('application_name')='freeze-producer' THEN
    PERFORM nextval('fs_part.before_seq');
    IF TG_OP='UPDATE' THEN
      INSERT INTO fs_part.producer_audit VALUES(OLD.v,NEW.v);
      NEW.v:=NEW.v||':T';
    ELSE
      INSERT INTO fs_part.producer_audit VALUES(OLD.v,NULL);
    END IF;
  END IF;
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END$$`,
		"CREATE TRIGGER producer_before BEFORE UPDATE OR DELETE ON fs_part.heap FOR EACH ROW EXECUTE FUNCTION fs_part.producer_before()",
		`CREATE FUNCTION fs_part.producer_after() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF current_setting('application_name')='freeze-producer' THEN
    PERFORM nextval('fs_part.after_seq');
  END IF;
  RETURN NULL;
END$$`,
		"CREATE TRIGGER producer_after AFTER UPDATE OR DELETE ON fs_part.heap FOR EACH ROW EXECUTE FUNCTION fs_part.producer_after()",
	} {
		freezeRun(t, p, b, c, fixture.Exec(q, 0))
	}
	a := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(1,'A') RETURNING k,v,ctid,xmin,xmax")
	sentinel := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(2,'sentinel') RETURNING k,v,ctid,xmin,xmax")
	freezeApply(t, p, b, c, a.tid)
	freezeExpect(t, p, b, c, freezeRepaired(a), sentinel)

	peer, producer := freezeConn(t, p, b), freezeConn(t, p, b)
	defer peer.Close()
	defer producer.Close()
	defer peer.ExecContext(p.Ctx, "ROLLBACK")
	defer producer.ExecContext(p.Ctx, "ROLLBACK")
	freezeRun(t, p, b, producer, fixture.Exec("SET application_name='freeze-producer'", 0))
	defer producer.ExecContext(p.Ctx, "RESET application_name")

	query := "UPDATE fs_part.heap SET v=v||':'||nextval('fs_part.rhs_seq')::text WHERE k=1 AND v<>'skip'"
	if operation == "delete" {
		query = "DELETE FROM fs_part.heap WHERE k=1 AND v<>'skip'"
	}
	if returning {
		query += " RETURNING k,v,old.v AS previous,new.v AS replacement"
	}
	// The RETURNING route holds its statement across the peer's genuine write;
	// the affected-count route exercises direct Exec on the same SQL producer.
	var statement *sql.Stmt
	if returning {
		var err error
		statement, err = producer.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
	}
	freezeRun(t, p, b, peer, fixture.Exec("BEGIN", 0))
	peerOwner := freezeAssignedOwner(t, p, b, peer)
	peerValue := "B"
	if !keep {
		peerValue = "skip"
	}
	freezeRun(t, p, b, peer, fixture.Exec("UPDATE fs_part.heap SET v=$1::text WHERE k=1", 1, peerValue))
	successor := freezeOne(t, p, peer, "SELECT k,v,ctid,xmin,xmax FROM fs_part.heap WHERE k=1")
	if successor.k != 1 || successor.v != peerValue || successor.tid == a.tid || successor.xmin != peerOwner.xid || successor.xmax != peerOwner.xid {
		t.Fatalf("genuine repaired-lineage successor %+v, original %+v, owner %s", successor, a, peerOwner.xid)
	}
	freezeRun(t, p, b, producer, fixture.Exec("BEGIN", 0))
	producerPID := fixture.LockPID(t, p.Ctx, producer)
	ctx, cancel := context.WithCancel(p.Ctx)
	defer cancel()
	finished := make(chan struct{})
	await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
		defer close(finished)
		if returning {
			rows, err := statement.QueryContext(ctx)
			return fixture.RelationOutcome{Rows: rows, Err: err}
		}
		result, err := producer.ExecContext(ctx, query)
		return fixture.RelationOutcome{Result: result, Err: err}
	})
	joined := false
	defer func() {
		if !joined {
			cancel()
			out := await()
			if out.Rows != nil {
				out.Rows.Close()
			}
		}
	}()
	if b.PG {
		fixture.TransactionLockPending(t, p, producerPID, peerOwner.pid, finished)
	} else if operation == "update" {
		fixture.NativeUpdateRowWait(t, p.Ctx, finished)
	} else {
		fixture.NativeDeleteRowWait(t, p.Ctx, finished)
	}
	// The UPDATE projection precedes this actual pre-BEFORE wait. Sequence
	// state distinguishes re-evaluation from merely using a constant new value.
	freezeProducerSequence(t, p, b, c, "rhs_seq", 1, operation == "update")
	freezeProducerSequence(t, p, b, c, "before_seq", 1, false)
	freezeProducerSequence(t, p, b, c, "after_seq", 1, false)
	freezeRun(t, p, b, c, fixture.Select("SELECT old_value,new_value FROM fs_part.producer_audit", []string{"old_value", "new_value"}, []string{"TEXT", "TEXT"}, nil))
	freezeRun(t, p, b, peer, fixture.Exec("COMMIT", 0))
	out := await()
	joined = true
	if out.Err != nil {
		if out.Rows != nil {
			out.Rows.Close()
		}
		t.Fatal(out.Err)
	}
	var expected [][]any
	var audit [][]any
	finalValue := peerValue
	if keep {
		if operation == "update" {
			finalValue = "B:2:T"
			expected = [][]any{{int64(1), finalValue, "B", finalValue}}
			audit = [][]any{{"B", "B:2"}}
		} else {
			expected = [][]any{{int64(1), "B", "B", nil}}
			audit = [][]any{{"B", nil}}
		}
	}
	if returning {
		freezeProducerReturning(t, out.Rows, expected)
	} else {
		var affected int64
		if keep {
			affected = 1
		}
		fixture.LockAffected(t, out.Result, affected)
	}
	freezeRun(t, p, b, producer, fixture.Exec("COMMIT", 0))
	var rhsValue int64 = 1
	if operation == "update" && keep {
		rhsValue = 2
	}
	freezeProducerSequence(t, p, b, c, "rhs_seq", rhsValue, operation == "update")
	freezeProducerSequence(t, p, b, c, "before_seq", 1, keep)
	freezeProducerSequence(t, p, b, c, "after_seq", 1, keep)
	freezeBoth(t, p, b, c, fixture.Select("SELECT old_value,new_value FROM fs_part.producer_audit", []string{"old_value", "new_value"}, []string{"TEXT", "TEXT"}, audit))
	values := [][]any{{int64(1), finalValue}, {int64(2), "sentinel"}}
	if operation == "delete" && keep {
		values = values[1:]
	}
	freezeBoth(t, p, b, c, fixture.Select("SELECT k,v FROM fs_part.heap ORDER BY k", []string{"k", "v"}, []string{"INT4", "TEXT"}, values))
	freezeBoth(t, p, b, c, fixture.Select("SELECT k,v,ctid,xmin,xmax FROM fs_part.heap WHERE k=2", []string{"k", "v", "ctid", "xmin", "xmax"}, []string{"INT4", "TEXT", "TID", "XID", "XID"}, [][]any{{sentinel.k, sentinel.v, sentinel.tid, sentinel.xmin, sentinel.xmax}}))
}

func freezeProducerSequence(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string, value int64, called bool) {
	t.Helper()
	freezeBoth(t, p, b, c, fixture.Select("SELECT last_value,is_called FROM fs_part."+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{value, called}}))
}

func freezeProducerReturning(t *testing.T, rows *sql.Rows, expected [][]any) {
	t.Helper()
	if rows == nil {
		t.Fatal("producer returned no cursor")
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"k", "v", "previous", "replacement"}) {
		t.Fatalf("RETURNING columns %v: %v", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 4 {
		t.Fatalf("RETURNING metadata %v: %v", types, err)
	}
	for i, want := range []string{"INT4", "TEXT", "TEXT", "TEXT"} {
		if types[i].DatabaseTypeName() != want {
			t.Fatalf("RETURNING column %d type %s, want %s", i, types[i].DatabaseTypeName(), want)
		}
	}
	var got [][]any
	for rows.Next() {
		values := make([]any, 4)
		if err := rows.Scan(&values[0], &values[1], &values[2], &values[3]); err != nil {
			t.Fatal(err)
		}
		for i := range values {
			values[i] = fixture.Normalize(values[i])
		}
		got = append(got, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("RETURNING got %#v, want %#v", got, expected)
	}
}
