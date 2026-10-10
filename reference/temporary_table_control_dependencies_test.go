package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceTemporaryTableControlDependencies(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		s.exec(s.c, "CREATE TABLE public.retained_persistent(v INT)", 0)
		s.exec(s.c, "INSERT INTO public.retained_persistent VALUES(7)", 1)
		s.exec(s.c, "CREATE TEMP SEQUENCE default_seed START 31", 0)
		s.exec(s.c, "CREATE TEMP SEQUENCE check_seed START 41", 0)
		s.utility("CREATE TEMP TABLE retained_defaults(id SERIAL UNIQUE,v INT DEFAULT nextval('default_seed') CHECK(nextval('check_seed') > 0),g INT GENERATED ALWAYS AS (id + 1) STORED,CONSTRAINT zzz_retained_positive CHECK(v>=0))", nil)
		s.exec(s.c, "INSERT INTO retained_defaults DEFAULT VALUES", 1)
		temporaryTableRows(s, s.c, "retained_defaults", 31)
		s.exec(s.peer, "CREATE TEMP TABLE retained_defaults(v INT)", 0)
		s.exec(s.peer, "INSERT INTO retained_defaults VALUES(81)", 1)
		var retained *sql.Rows
		var dump bytes.Buffer
		if !s.postgres {
			if err := stuntgres.Dump(s.p.ctx, s.p.native, &dump); err != nil {
				s.t.Fatal(err)
			}
			for _, name := range []string{"retained_defaults", "default_seed", "check_seed", "pg_temp_"} {
				if strings.Contains(dump.String(), name) {
					s.t.Fatal("TEMP dependency leaked into dump", dump.String())
				}
			}
			var err error
			retained, err = s.c.QueryContext(s.p.ctx, "SELECT id,v,g FROM retained_defaults ORDER BY id")
			if err != nil {
				s.t.Fatal(err)
			}
			defer retained.Close()
			if err := stuntgres.Reset(s.p.ctx, s.p.native); err != nil {
				s.t.Fatal(err)
			}
			temporaryTableControlSnapshot(s, retained, [][3]int64{{1, 31, 2}})
		}
		temporaryTableRows(s, s.c, "retained_defaults", 31)
		s.exec(s.c, "INSERT INTO retained_defaults DEFAULT VALUES", 1)
		temporaryTableRows(s, s.c, "retained_defaults", 31, 32)
		temporaryTableRows(s, s.peer, "retained_defaults", 81)
		s.exec(s.c, "UPDATE retained_defaults SET v=v+0 WHERE id=1", 1)
		if !s.postgres {
			var err error
			retained, err = s.c.QueryContext(s.p.ctx, "SELECT id,v,g FROM retained_defaults ORDER BY id")
			if err != nil {
				s.t.Fatal(err)
			}
			defer retained.Close()
			if err := stuntgres.Restore(s.p.ctx, s.p.native, bytes.NewReader(dump.Bytes())); err != nil {
				s.t.Fatal(err)
			}
			temporaryTableControlSnapshot(s, retained, [][3]int64{{1, 31, 2}, {2, 32, 3}})
		}
		s.exec(s.c, "INSERT INTO retained_defaults DEFAULT VALUES", 1)
		temporaryTableRows(s, s.c, "retained_defaults", 31, 32, 33)
		temporaryTableRows(s, s.peer, "retained_defaults", 81)
		s.exec(s.c, "UPDATE retained_defaults SET v=v+0 WHERE id=2", 1)
		// Definition replacement must preserve unrelated captured CHECK/default and
		// owned-sequence provenance rather than remapping the retained TEMP object.
		s.utility("ALTER TABLE retained_defaults ALTER COLUMN v SET DEFAULT nextval('default_seed')", nil)
		s.exec(s.c, "INSERT INTO retained_defaults DEFAULT VALUES", 1)
		temporaryTableRows(s, s.c, "retained_defaults", 31, 32, 33, 34)
		referenceRunStep(s.t, s.p.ctx, s.c, s.record, sequenceInt64Step(referenceStep{query: "SELECT id,g FROM retained_defaults ORDER BY id", prepared: s.prepared, selectRows: true, columns: []string{"id", "g"}, types: []string{"INT4", "INT4"}, rows: [][]any{{int64(1), int64(2)}, {int64(2), int64(3)}, {int64(3), int64(4)}, {int64(4), int64(5)}}}))
		s.exec(s.c, "SET search_path=pg_temp,public", 0)
		namespace := tempReferenceNamespace(s.t, s, false)
		s.exec(s.c, "SET search_path=public", 0)
		want := tempReferenceDiagnostic("23514", `new row for relation "retained_defaults" violates check constraint "zzz_retained_positive"`, "", "")
		want.Detail, want.Schema, want.Table, want.Constraint = "Failing row contains (5, -1, 6).", namespace, "retained_defaults", "zzz_retained_positive"
		failure := referenceImplicitError("INSERT INTO retained_defaults(v) VALUES(-1)", *want)
		failure.prepared, failure.errorStage = s.prepared, "execute"
		referenceRunStep(s.t, s.p.ctx, s.c, s.record, failure)
		temporaryTableRows(s, s.c, "retained_defaults", 31, 32, 33, 34)
		s.value(s.c, "SELECT currval('check_seed')", 47)
		s.value(s.c, "SELECT currval('retained_defaults_id_seq')", 5)
		temporaryTableRows(s, s.c, "public.retained_persistent", 7)
	})
}

func temporaryTableControlSnapshot(s *tempReferenceSession, rows *sql.Rows, want [][3]int64) {
	s.t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"id", "v", "g"}) {
		s.t.Fatal("retained columns", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 3 {
		s.t.Fatal("retained metadata", types, err)
	}
	for _, column := range types {
		if column.DatabaseTypeName() != "INT4" {
			s.t.Fatal("retained type", column.DatabaseTypeName())
		}
	}
	var got [][3]int64
	for rows.Next() {
		var row [3]int64
		if err := rows.Scan(&row[0], &row[1], &row[2]); err != nil {
			s.t.Fatal(err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		s.t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		s.t.Fatalf("retained rows got%v want%v", got, want)
	}
	if err := rows.Close(); err != nil {
		s.t.Fatal(err)
	}
}
