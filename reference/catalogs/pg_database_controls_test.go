package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGDatabaseControls(t *testing.T) {
	pgDatabaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgDatabaseFacts(t, p, b, b.DB)
		if b.PG {
			return
		}
		// Public controls are native-only; the SQL contract above is shared.
		q := "SELECT * FROM pg_database WHERE datname=current_database()"
		held := fixture.ViewHold(t, p, b, q)
		var name string
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&name); err != nil {
			t.Fatal(err)
		}
		want := []any{int64(16383), name, int64(10), int64(6), "c", false, true, false, int64(-1), nil, nil, nil, "C", "C", nil, nil, nil, nil}
		// The logical horizon is real and may advance across replacement.
		// Keep every other cell/metadata exact, but validate this dynamic XID
		// independently rather than copying a runtime value into a golden row.
		validate := func(values, expected []any) uint64 {
			t.Helper()
			var horizon uint64
			for i, value := range values {
				if pgDatabaseTypes[i] == "XID" && value != nil {
					raw, ok := value.([]byte)
					if !ok {
						t.Fatalf("XID raw column%d: %T", i, value)
					}
					n, err := strconv.ParseUint(string(raw), 10, 32)
					if err != nil {
						t.Fatal(err)
					}
					if i == 9 {
						if n < 3 {
							t.Fatal("invalid actual logical horizon", n)
						}
						horizon = n
						continue
					}
				} else {
					fixture.AssertRawValue(t, false, pgDatabaseTypes[i], value)
				}
				if !reflect.DeepEqual(fixture.Normalize(value), expected[i]) {
					t.Fatalf("column%d got%#v want%#v", i, value, expected[i])
				}
			}
			if horizon == 0 {
				t.Fatal("logical database horizon unavailable")
			}
			return horizon
		}
		check := func(db fixture.Executor) uint64 {
			t.Helper()
			rows, err := db.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			pgDatabaseMetadata(t, rows, false)
			if !rows.Next() {
				t.Fatal("current attachment row absent", rows.Err())
			}
			values, pointers := make([]any, 18), make([]any, 18)
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			horizon := validate(values, want)
			if rows.Next() || rows.Err() != nil {
				t.Fatal("attachment row multiplicity", rows.Err())
			}
			return horizon
		}
		originalHorizon := check(held)
		old, err := b.DB.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		pgDatabaseMetadata(t, old, false)
		saved, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fixture.PGClassExec(t, p, b, b.DB, "CREATE TABLE pgdb18_portable(id integer)")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pgdb18_portable VALUES(18)", 1))
		dump := fixture.ViewDump(t, p)
		if strings.Contains(string(dump), "CREATE DATABASE") || strings.Contains(string(dump), "INSERT INTO pg_database") {
			t.Fatal("dump serialized attachment/catalog source", string(dump))
		}
		fixture.PGClassExec(t, p, b, b.DB, "GRANT CREATE ON DATABASE \""+escapeDatabaseIdentifier(name)+"\" TO PUBLIC")
		want[17] = "{=CTc/postgres,postgres=CTc/postgres}"
		check(held)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(string(dump)+"CREATE SEQUENCE pgdb18_bad CACHE 0;")); err == nil {
			t.Fatal("late invalid restore succeeded")
		} else {
			want := fixture.ControlsReferenceDiagnostic("22023", "CACHE (0) must be greater than zero")
			fixture.AssertDiagnostic(t, err, &want)
		}
		check(held)
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		check(held)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		check(held)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		check(held)
		fixture.PGDatabaseCheck(t, p, b, b.DB, fixture.Select("SELECT id FROM pgdb18_portable", []string{"id"}, []string{"INT4"}, [][]any{{int64(18)}}))
		if !old.Next() {
			t.Fatal("retained row absent", old.Err())
		}
		values, pointers := make([]any, 18), make([]any, 18)
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := old.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		original := append([]any(nil), want...)
		original[17] = nil
		if horizon := validate(values, original); horizon != originalHorizon {
			t.Fatal("retained pre-grant horizon changed", horizon, originalHorizon)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("retained row multiplicity", old.Err())
		}
		if err := old.Close(); err != nil {
			t.Fatal(err)
		}
		for i, typ := range saved {
			if typ.Name() != fixture.PGDatabaseColumns[i] || typ.DatabaseTypeName() != pgDatabaseTypes[i] {
				t.Fatal("saved metadata after EOF/Close", i, typ)
			}
		}
		c, err := stuntgres.NewDriver().OpenConnector("  pgdb18 destination  ")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(c)
		defer destination.Close()
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		var destinationName string
		var acl sql.NullString
		var owner int64
		if err := destination.QueryRowContext(p.Ctx, "SELECT datname,datdba,datacl FROM pg_database").Scan(&destinationName, &owner, &acl); err != nil || destinationName != "  pgdb18 destination  " || owner != 10 || acl.Valid {
			t.Fatal("destination attachment facts", destinationName, owner, acl, err)
		}
		if err := destination.QueryRowContext(p.Ctx, "SELECT id FROM pgdb18_portable").Scan(&owner); err != nil || owner != 18 {
			t.Fatal("destination portable data", owner, err)
		}
	})
}
