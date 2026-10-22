package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

var partitionInheritedHistories = []string{"creation_merge", "recursive_add_drop", "recursive_null_drop", "only_check", "only_named_null", "only_column_null", "existing_set"}

// A single SQL/history authority supplies controls and unchanged-stream replay.
// Expectations describe final semantics, never dump bytes or source-engine OIDs.
func partitionInheritedHistory(s *partitionInheritedCase, history string) {
	s.t.Helper()
	s.exec("CREATE TABLE pi_p(k INT,a INT CONSTRAINT pi_nn NOT NULL,b INT,CONSTRAINT pi_ck CHECK(b=1)) PARTITION BY RANGE(k)", 0)
	mid := ""
	if history == "creation_merge" {
		mid = "(a CONSTRAINT pi_nn NOT NULL,CONSTRAINT pi_ck CHECK(b=1))"
	}
	s.notices = nil
	s.exec("CREATE TABLE pi_mid PARTITION OF pi_p"+mid+" DEFAULT PARTITION BY RANGE(k)", 0)
	if history == "creation_merge" {
		s.expectNotices(partitionInheritedMergeNotice("pi_ck"))
	}
	s.exec("CREATE TABLE pi_lo PARTITION OF pi_mid FOR VALUES FROM(0) TO(10)", 0)
	hi := ""
	if history == "recursive_null_drop" {
		hi = "(a CONSTRAINT pi_nn NOT NULL)"
	}
	s.exec("CREATE TABLE pi_hi PARTITION OF pi_mid"+hi+" FOR VALUES FROM(10) TO(20)", 0)
	s.exec("INSERT INTO pi_p VALUES($1,$2,$3)", 1, 1, 7, nil)
	s.exec("INSERT INTO pi_hi VALUES($1,$2,$3)", 1, 11, 8, 1)
	switch history {
	case "recursive_add_drop":
		s.exec("ALTER TABLE pi_p ADD CONSTRAINT pi_extra CHECK(a>0),DROP CONSTRAINT pi_ck", 0)
	case "recursive_null_drop":
		s.exec("ALTER TABLE pi_p DROP CONSTRAINT pi_nn", 0)
	case "only_check":
		s.exec("ALTER TABLE ONLY pi_p DROP CONSTRAINT pi_ck", 0)
	case "only_named_null":
		s.exec("ALTER TABLE ONLY pi_p DROP CONSTRAINT pi_nn", 0)
	case "only_column_null":
		s.exec("ALTER TABLE ONLY pi_p ALTER COLUMN a DROP NOT NULL", 0)
	case "existing_set":
		s.exec("ALTER TABLE pi_mid ALTER COLUMN a SET NOT NULL", 0)
	}
	partitionInheritedHistoryCheck(s, history)
}

func partitionInheritedHistoryCheck(s *partitionInheritedCase, history string) {
	s.t.Helper()
	checkName, checkKey := "pi_ck", "{3}"
	if history == "recursive_add_drop" {
		checkName, checkKey = "pi_extra", "{2}"
	}
	rootNull := history != "only_named_null" && history != "only_column_null" && history != "recursive_null_drop"
	seen := make(map[string]bool)
	for i, table := range []string{"pi_p", "pi_mid", "pi_lo", "pi_hi"} {
		facts := make([][]any, 0, 2)
		if i != 0 || history != "only_check" {
			local, count := i == 0, int64(min(i, 1))
			if history == "only_check" && i == 1 {
				local, count = true, 0
			}
			facts = append(facts, partitionInheritedFact(checkName, "c", local, count, checkKey))
		}
		if (i != 0 || rootNull) && (history != "recursive_null_drop" || i == 3) {
			local, count := i == 0, int64(min(i, 1))
			if i == 1 && (history == "creation_merge" || history == "existing_set" || !rootNull) {
				local = true
			}
			if i == 1 && !rootNull {
				count = 0
			}
			if history == "recursive_null_drop" {
				local, count = true, 0
			}
			facts = append(facts, partitionInheritedFact("pi_nn", "n", local, count, "{2}"))
		}
		s.facts(table, facts)
		notnull := i != 0 || rootNull
		if history == "recursive_null_drop" {
			notnull = i == 3
		}
		s.nullability(table, notnull)
		for _, fact := range facts {
			oid := s.oid(table, fact[0].(string))
			if seen[oid] {
				s.t.Fatal("replayed physical constraints reused OID", oid)
			}
			seen[oid] = true
		}
		checks := int64(1)
		if history == "only_check" && i == 0 {
			checks = 0
		}
		s.run(fixture.Select("SELECT relchecks FROM pg_class WHERE oid='"+table+"'::regclass::oid", []string{"relchecks"}, []string{"INT2"}, [][]any{{checks}}))
	}
	s.values("SELECT k,a,b FROM pi_p ORDER BY k", [][]any{{int64(1), int64(7), nil}, {int64(11), int64(8), int64(1)}}, []bool{true, !rootNull, true})
	s.values("SELECT k,a,b FROM pi_hi WHERE false", nil, []bool{true, false, true})
	bad, row := "INSERT INTO pi_p VALUES(12,7,2)", "12, 7, 2"
	if history == "recursive_add_drop" {
		bad, row = "INSERT INTO pi_p VALUES(12,0,1)", "12, 0, 1"
	}
	s.fail(bad, partitionInheritedCheckFailure("pi_hi", checkName, row))
	s.fail("INSERT INTO pi_hi VALUES(12,NULL,1)", partitionInheritedNullFailure("pi_hi", "12, null, 1"))
}

func TestReferencePartitionInheritedControls(t *testing.T) {
	for _, history := range partitionInheritedHistories {
		t.Run(history, func(t *testing.T) {
			partitionInheritedScenario(t, func(s *partitionInheritedCase) {
				t := s.t
				partitionInheritedHistory(s, history)
				if s.b.PG {
					return
				} // Explicit non-engine boundary, not a PG control API.
				var dump bytes.Buffer
				if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				before := make(map[string]string)
				for _, table := range []string{"pi_p", "pi_mid", "pi_lo", "pi_hi"} {
					for _, name := range []string{"pi_ck", "pi_extra", "pi_nn"} {
						var oid string
						err := s.conn.QueryRowContext(s.p.Ctx, "SELECT oid::text FROM pg_constraint WHERE conrelid='"+table+"'::regclass::oid AND conname=$1", name).Scan(&oid)
						if err == nil {
							before[table+"/"+name] = oid
						} else if !errors.Is(err, sql.ErrNoRows) {
							t.Fatal(err)
						}
					}
				}
				// Restore must replace a nonempty destination atomically. Both failed
				// imports retain this independent sentinel and an eager native cursor.
				s.exec("CREATE TABLE pi_sentinel(k INT,a INT,b INT)", 0)
				s.exec("INSERT INTO pi_sentinel VALUES(99,NULL,3)", 1)
				old, err := s.b.DB.QueryContext(s.p.Ctx, "SELECT k,a,b FROM pi_sentinel")
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				columns, err := old.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				preserved := func() {
					partitionInheritedHistoryCheck(s, history)
					for key, oid := range before {
						table, name, _ := strings.Cut(key, "/")
						s.sameOID(table, name, oid)
					}
					s.values("SELECT k,a,b FROM pi_sentinel", [][]any{{int64(99), nil, int64(3)}}, []bool{true, true, true})
				}
				// The whole stream is valid SQL; its final selected constraint failure
				// happens after replay has privately constructed the hierarchy/data.
				bad := dump.String() + "\nINSERT INTO pi_hi VALUES(12,NULL,1);"
				err = stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(bad))
				want := partitionInheritedNullFailure("pi_hi", "12, null, 1")
				want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
				fixture.AssertDiagnostic(t, err, &want)
				preserved()
				// Malformed input must not publish a valid replay prefix either.
				malformed := dump.String() + "\nCREATE TABLE pi_broken("
				err = stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(malformed))
				want = fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of input")
				want.Position = fixture.Position(len(malformed) + 1)
				fixture.AssertDiagnostic(t, err, &want)
				preserved()
				ctx, cancel := context.WithCancel(s.p.Ctx)
				reader := &referenceRangeCancelEOF{Reader: strings.NewReader(dump.String()), cancel: cancel}
				err = stuntgres.Restore(ctx, s.p.Native, reader)
				cancel()
				if !errors.Is(err, context.Canceled) {
					t.Fatal("canceled Restore", err)
				}
				preserved()
				// Successful replacement removes the sentinel, while the already
				// captured native cursor retains its original metadata and rows.
				if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				partitionInheritedHistoryCheck(s, history)
				s.run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pi_sentinel'", []string{"relname"}, []string{"NAME"}, nil))
				fixture.TableDDLColumnTypes(t, false, columns, []string{"k", "a", "b"}, []string{"INT4", "INT4", "INT4"}, []bool{true, true, true})
				var retained [][]any
				for old.Next() {
					var k, a, b any
					if err := old.Scan(&k, &a, &b); err != nil {
						t.Fatal(err)
					}
					retained = append(retained, []any{k, a, b})
				}
				if err := old.Err(); err != nil || !reflect.DeepEqual(retained, [][]any{{int64(99), nil, int64(3)}}) {
					t.Fatalf("retained=%#v err=%v", retained, err)
				}
				if err := old.Close(); err != nil {
					t.Fatal(err)
				}
				// A second genuine roundtrip exercises remapped origins, not byte
				// equality or accidental equality of original physical OIDs.
				var redump bytes.Buffer
				if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &redump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(redump.Bytes())); err != nil {
					t.Fatal(err)
				}
				partitionInheritedHistoryCheck(s, history)
			})
		})
	}
}

func TestReferencePartitionInheritedReplay(t *testing.T) {
	for _, history := range partitionInheritedHistories {
		t.Run(history, func(t *testing.T) {
			partitionInheritedScenario(t, func(s *partitionInheritedCase) {
				t := s.t
				partitionInheritedHistory(s, history)
				if s.b.PG {
					return
				}
				var dump bytes.Buffer
				if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if s.p.Postgres == nil {
					return
				} // Quick mode has no external oracle.
				// The stream retains its named postgres owner. Use the fixture's
				// existing admin authority for unchanged SQL, never edited dump text.
				oracle, err := fixture.Admin.Conn(s.p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer oracle.Close()
				cleanup := func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					for _, q := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "RESET search_path", "DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
						if _, err := oracle.ExecContext(ctx, q); err != nil {
							t.Error(err)
						}
					}
				}
				defer cleanup()
				if _, err := oracle.ExecContext(s.p.Ctx, dump.String()); err != nil {
					t.Fatal("PG rejected unchanged partition inheritance dump", err)
				}
				replayed := *s
				// Keep middleware-owned row/codec assertions on the fixture's PG
				// pool, independently of the administrative replay session.
				read, err := s.p.Postgres.Conn(s.p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer read.Close()
				replayed.conn, replayed.recorder, replayed.b.PG = read, s.p.PostgresRecord, true
				partitionInheritedHistoryCheck(&replayed, history)
			})
		})
	}
}
