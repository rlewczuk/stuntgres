package stuntgres_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres"
	"testing"
	"time"
)

func sequencePreparedMetadata(t *testing.T) {
	t.Run("TemporalParameterMetadata", func(t *testing.T) {
		ctx := context.Background()
		c := sequenceConn(t, openLocal(t, stuntgres.NewDriver(), t.Name()))
		for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10", "CREATE SEQUENCE b.t START 20", "SET search_path TO a,b"} {
			sequenceExec(t, c, q)
		}
		s := sequenceHeld(t, c, "SELECT nextval('s'),TIMESTAMP 'now',setval('t',$1,false)")
		query := func(want int64) (time.Time, []*sql.ColumnType) {
			t.Helper()
			rows, err := s.QueryContext(ctx, int64(70))
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			types, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if len(types) != 3 || types[0].DatabaseTypeName() != "INT8" || types[1].DatabaseTypeName() != "TIMESTAMP" || types[2].DatabaseTypeName() != "INT8" {
				t.Fatal(types)
			}
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			var value, set int64
			var temporal time.Time
			if err := rows.Scan(&value, &temporal, &set); err != nil || value != want || set != 70 {
				t.Fatal(value, set, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("unexpected extra row", rows.Err())
			}
			return temporal, types
		}
		before, types := query(10)
		for _, q := range []string{"CREATE SEQUENCE a.s START 100", "CREATE SEQUENCE a.t START 200", "ALTER SEQUENCE b.s CACHE 1"} {
			sequenceExec(t, c, q)
		}
		after, freshTypes := query(100)
		if !before.Equal(after) {
			t.Fatal("sequence reanalysis recaptured temporal now", before, after)
		}
		for i := range types {
			if !reflect.DeepEqual(types[i], freshTypes[i]) {
				t.Fatal("result metadata changed", types[i], freshTypes[i])
			}
		}
		sequenceValue(t, c, "SELECT nextval('a.t')", 70)
		sequenceValue(t, c, "SELECT nextval('b.t')", 70)
	})
	t.Run("FailedGroupPublishesNothing", func(t *testing.T) {
		ctx := context.Background()
		c := sequenceConn(t, openLocal(t, stuntgres.NewDriver(), t.Name()))
		for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10", "CREATE SEQUENCE b.t START 20", "SET search_path TO a,b"} {
			sequenceExec(t, c, q)
		}
		s := sequenceHeld(t, c, "SELECT nextval('s'),nextval('t')")
		sequenceExec(t, c, "CREATE SEQUENCE a.s START 100")
		sequenceExec(t, c, "ALTER SEQUENCE b.t RENAME TO identity")
		var a, b int64
		err := s.QueryRowContext(ctx).Scan(&a, &b)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "t" does not exist`, Position: "29"})
		sequenceExec(t, c, "CREATE SEQUENCE a.t START 200")
		if err := s.QueryRowContext(ctx).Scan(&a, &b); err != nil || a != 100 || b != 200 {
			t.Fatal("failed group consumed/published partial capture", a, b, err)
		}
	})
}
