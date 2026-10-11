package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTemporaryTableControlDependencies(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		s.Exec(s.C, "CREATE TABLE public.retained_persistent(v INT)", 0)
		s.Exec(s.C, "INSERT INTO public.retained_persistent VALUES(7)", 1)
		s.Exec(s.C, "CREATE TEMP SEQUENCE default_seed START 31", 0)
		s.Exec(s.C, "CREATE TEMP SEQUENCE check_seed START 41", 0)
		s.Utility("CREATE TEMP TABLE retained_defaults(id SERIAL UNIQUE,v INT DEFAULT nextval('default_seed') CHECK(nextval('check_seed') > 0),g INT GENERATED ALWAYS AS (id + 1) STORED,CONSTRAINT zzz_retained_positive CHECK(v>=0))", nil)
		s.Exec(s.C, "INSERT INTO retained_defaults DEFAULT VALUES", 1)
		fixture.TemporaryTableRows(s, s.C, "retained_defaults", 31)
		s.Exec(s.Peer, "CREATE TEMP TABLE retained_defaults(v INT)", 0)
		s.Exec(s.Peer, "INSERT INTO retained_defaults VALUES(81)", 1)
		var retained *sql.Rows
		var dump bytes.Buffer
		if !s.Postgres {
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
				s.T.Fatal(err)
			}
			for _, name := range []string{"retained_defaults", "default_seed", "check_seed", "pg_temp_"} {
				if strings.Contains(dump.String(), name) {
					s.T.Fatal("TEMP dependency leaked into dump", dump.String())
				}
			}
			var err error
			retained, err = s.C.QueryContext(s.P.Ctx, "SELECT id,v,g FROM retained_defaults ORDER BY id")
			if err != nil {
				s.T.Fatal(err)
			}
			defer retained.Close()
			if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
				s.T.Fatal(err)
			}
			temporaryTableControlSnapshot(s, retained, [][3]int64{{1, 31, 2}})
		}
		fixture.TemporaryTableRows(s, s.C, "retained_defaults", 31)
		s.Exec(s.C, "INSERT INTO retained_defaults DEFAULT VALUES", 1)
		fixture.TemporaryTableRows(s, s.C, "retained_defaults", 31, 32)
		fixture.TemporaryTableRows(s, s.Peer, "retained_defaults", 81)
		s.Exec(s.C, "UPDATE retained_defaults SET v=v+0 WHERE id=1", 1)
		if !s.Postgres {
			var err error
			retained, err = s.C.QueryContext(s.P.Ctx, "SELECT id,v,g FROM retained_defaults ORDER BY id")
			if err != nil {
				s.T.Fatal(err)
			}
			defer retained.Close()
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump.Bytes())); err != nil {
				s.T.Fatal(err)
			}
			temporaryTableControlSnapshot(s, retained, [][3]int64{{1, 31, 2}, {2, 32, 3}})
		}
		s.Exec(s.C, "INSERT INTO retained_defaults DEFAULT VALUES", 1)
		fixture.TemporaryTableRows(s, s.C, "retained_defaults", 31, 32, 33)
		fixture.TemporaryTableRows(s, s.Peer, "retained_defaults", 81)
		s.Exec(s.C, "UPDATE retained_defaults SET v=v+0 WHERE id=2", 1)
		// Definition replacement must preserve unrelated captured CHECK/default and
		// owned-sequence provenance rather than remapping the retained TEMP object.
		s.Utility("ALTER TABLE retained_defaults ALTER COLUMN v SET DEFAULT nextval('default_seed')", nil)
		s.Exec(s.C, "INSERT INTO retained_defaults DEFAULT VALUES", 1)
		fixture.TemporaryTableRows(s, s.C, "retained_defaults", 31, 32, 33, 34)
		fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, fixture.SequenceInt64Step(fixture.Step{Query: "SELECT id,g FROM retained_defaults ORDER BY id", Prepared: s.Prepared, SelectRows: true, Columns: []string{"id", "g"}, Types: []string{"INT4", "INT4"}, Rows: [][]any{{int64(1), int64(2)}, {int64(2), int64(3)}, {int64(3), int64(4)}, {int64(4), int64(5)}}}))
		s.Exec(s.C, "SET search_path=pg_temp,public", 0)
		namespace := fixture.TempReferenceNamespace(s.T, s, false)
		s.Exec(s.C, "SET search_path=public", 0)
		want := fixture.TempReferenceDiagnostic("23514", `new row for relation "retained_defaults" violates check constraint "zzz_retained_positive"`, "", "")
		want.Detail, want.Schema, want.Table, want.Constraint = "Failing row contains (5, -1, 6).", namespace, "retained_defaults", "zzz_retained_positive"
		failure := fixture.ImplicitError("INSERT INTO retained_defaults(v) VALUES(-1)", *want)
		failure.Prepared, failure.ErrorStage = s.Prepared, "execute"
		fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, failure)
		fixture.TemporaryTableRows(s, s.C, "retained_defaults", 31, 32, 33, 34)
		s.Value(s.C, "SELECT currval('check_seed')", 47)
		s.Value(s.C, "SELECT currval('retained_defaults_id_seq')", 5)
		fixture.TemporaryTableRows(s, s.C, "public.retained_persistent", 7)
	})
}

func temporaryTableControlSnapshot(s *fixture.TempReferenceSession, rows *sql.Rows, want [][3]int64) {
	s.T.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"id", "v", "g"}) {
		s.T.Fatal("retained columns", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 3 {
		s.T.Fatal("retained metadata", types, err)
	}
	for _, column := range types {
		if column.DatabaseTypeName() != "INT4" {
			s.T.Fatal("retained type", column.DatabaseTypeName())
		}
	}
	var got [][3]int64
	for rows.Next() {
		var row [3]int64
		if err := rows.Scan(&row[0], &row[1], &row[2]); err != nil {
			s.T.Fatal(err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		s.T.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		s.T.Fatalf("retained rows got%v want%v", got, want)
	}
	if err := rows.Close(); err != nil {
		s.T.Fatal(err)
	}
}
