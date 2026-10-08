package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"stuntgres"
	"testing"
)

func TestIntervalClockEndpointControls(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE iv(v INTERVAL)")
	for _, tc := range []struct{ input, out string }{
		{"-2562047788:00:54.775807", "-2562047788:00:54.775807"},
		{"2562047788:00:54.775807", "2562047788:00:54.775807"},
		{"+2562047788:00:54.775807", "2562047788:00:54.775807"},
		{"-9223372036854775808 microseconds", "-2562047788:00:54.775808"},
		{"+9223372036854775807 microseconds", "2562047788:00:54.775807"},
	} {
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{"SELECT INTERVAL '" + tc.input + "' FROM iv", "SELECT v FROM iv"} {
				execSQL(t, db, "DELETE FROM iv")
				insert := "INSERT INTO iv VALUES('" + tc.input + "')"
				if prepared {
					stmt, err := db.Prepare(insert)
					if err != nil {
						t.Fatal(err)
					}
					result, err := stmt.Exec()
					stmt.Close()
					if err != nil {
						t.Fatal(err)
					}
					if n, err := result.RowsAffected(); err != nil || n != 1 {
						t.Fatal(n, err)
					}
				} else {
					result := execSQL(t, db, insert)
					if n, err := result.RowsAffected(); err != nil || n != 1 {
						t.Fatal(n, err)
					}
				}
				var rows *sql.Rows
				var err error
				if prepared {
					stmt, e := db.Prepare(q)
					if e != nil {
						t.Fatal(e)
					}
					rows, err = stmt.Query()
					stmt.Close()
				} else {
					rows, err = db.Query(q)
				}
				if err != nil {
					t.Fatal(err)
				}
				intervalRows(t, rows, [][]any{{tc.out}})
			}
			execSQL(t, db, "DELETE FROM iv")
			q := "INSERT INTO iv VALUES($1)"
			if prepared {
				stmt, err := db.Prepare(q)
				if err != nil {
					t.Fatal(err)
				}
				result, err := stmt.Exec(tc.input)
				stmt.Close()
				if err != nil {
					t.Fatal(err)
				}
				if n, err := result.RowsAffected(); err != nil || n != 1 {
					t.Fatal(n, err)
				}
			} else {
				execSQL(t, db, q, tc.input)
			}
			rows, err := db.Query("SELECT v FROM iv")
			if err != nil {
				t.Fatal(err)
			}
			intervalRows(t, rows, [][]any{{tc.out}})
		}
	}
}

func TestIntervalClockEndpointSessionAtomicity(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE iv(v INTERVAL)")
	execSQL(t, db, "INSERT INTO iv VALUES('1 second')")
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, own := range []struct{ name, out string }{
		{"postgres", "00:00:01"},
		{"postgres_verbose", "@ 1 sec"},
		{"sql_standard", "0:00:01"},
		{"iso_8601", "PT1S"},
	} {
		if _, err = a.ExecContext(ctx, "SET IntervalStyle="+own.name); err != nil {
			t.Fatal(err)
		}
		if _, err = b.ExecContext(ctx, "SET IntervalStyle=iso_8601"); err != nil {
			t.Fatal(err)
		}
		for _, prepared := range []bool{false, true} {
			q := "INSERT INTO iv VALUES($1),($2)"
			if prepared {
				stmt, e := a.PrepareContext(ctx, q)
				if e != nil {
					t.Fatal(e)
				}
				_, err = stmt.ExecContext(ctx, "1 second", "-2562047788:00:54.775808")
				stmt.Close()
			} else {
				_, err = a.ExecContext(ctx, q, "1 second", "-2562047788:00:54.775808")
			}
			intervalPublicBoundaryError(t, err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22007", Message: "invalid input syntax for type interval: \"-2562047788:00:54.775808\"",Context:"unnamed portal parameter $2 = '...'"})
			var style, out string
			if err := a.QueryRowContext(ctx, "SHOW IntervalStyle").Scan(&style); err != nil || style != own.name {
				t.Fatal(style, err)
			}
			if err := b.QueryRowContext(ctx, "SHOW IntervalStyle").Scan(&style); err != nil || style != "iso_8601" {
				t.Fatal(style, err)
			}
			rows, err := a.QueryContext(ctx, "SELECT v FROM iv")
			if err != nil {
				t.Fatal(err)
			}
			intervalRows(t, rows, [][]any{{own.out}})
			if err := b.QueryRowContext(ctx, "SELECT v FROM iv").Scan(&out); err != nil || out != "PT1S" {
				t.Fatal(out, err)
			}
		}
	}
}

func TestIntervalMinimumTimeNativeRoundTrip(t *testing.T) {
	ctx := context.Background()
	drv := stuntgres.NewDriver()
	source := openLocal(t, drv, t.Name())
	execSQL(t, source, "CREATE TABLE iv(id INTEGER,v INTERVAL DEFAULT INTERVAL '-9223372036854775808 microseconds')")
	execSQL(t, source, "INSERT INTO iv VALUES(1,'-9223372036854775808 microseconds')")
	execSQL(t, source, "INSERT INTO iv(id) VALUES(2)")
	execSQL(t, source, "CREATE VIEW ivview AS SELECT INTERVAL '-9223372036854775808 microseconds' AS v FROM iv WHERE id=1")
	execSQL(t, source, "CREATE TABLE ivcomponents(id INTEGER,v INTERVAL)")
	execSQL(t, source, "INSERT INTO ivcomponents VALUES(1,'-14 months +3 days -9223372036854775808 microseconds'),(2,'-2562047788:00:54.775807'),(3,'2562047788:00:54.775807'),(4,'0'),(5,NULL),(6,'infinity'),(7,'-infinity')")
	var canonical bytes.Buffer
	if err := stuntgres.Dump(ctx, source, &canonical); err != nil {
		t.Fatal(err)
	}
	for _, style := range []string{"postgres", "postgres_verbose", "sql_standard", "iso_8601"} {
		execSQL(t, source, "SET IntervalStyle="+style)
		var dump bytes.Buffer
		if err := stuntgres.Dump(ctx, source, &dump); err != nil || dump.String() != canonical.String() {
			t.Fatal(style, err, "style-dependent dump")
		}
		for _, restoreStyle := range []struct{ name, out string }{
			{"postgres", "-2562047788:00:54.775808"},
			{"postgres_verbose", "@ 2562047788 hours 54.775808 secs ago"},
			{"sql_standard", "-2562047788:00:54.775808"},
			{"iso_8601", "PT-2562047788H-54.775808S"},
		} {
			dest := openLocal(t, drv, t.Name()+"/"+style+"/"+restoreStyle.name)
			execSQL(t, dest, "SET IntervalStyle="+restoreStyle.name)
			if err := stuntgres.Restore(ctx, dest, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(style, err)
			}
			var redump bytes.Buffer
			if err := stuntgres.Dump(ctx, dest, &redump); err != nil || redump.String() != canonical.String() {
				t.Fatal(style, err, "changed minimum-time redump")
			}
			var gotStyle string
			if err := dest.QueryRow("SHOW IntervalStyle").Scan(&gotStyle); err != nil || gotStyle != restoreStyle.name {
				t.Fatal(gotStyle, err)
			}
			for _, q := range []string{"SELECT v FROM iv ORDER BY id", "SELECT v FROM ivview"} {
				rows, err := dest.Query(q)
				if err != nil {
					t.Fatal(err)
				}
				want := [][]any{{restoreStyle.out}}
				if q == "SELECT v FROM iv ORDER BY id" {
					want = append(want, []any{restoreStyle.out})
				}
				intervalRows(t, rows, want)
			}
			execSQL(t, dest, "SET IntervalStyle=postgres")
			for _, q := range []string{"SELECT v FROM iv ORDER BY id", "SELECT v FROM ivview"} {
				rows, err := dest.Query(q)
				if err != nil {
					t.Fatal(err)
				}
				want := [][]any{{"-2562047788:00:54.775808"}}
				if q == "SELECT v FROM iv ORDER BY id" {
					want = append(want, []any{"-2562047788:00:54.775808"})
				}
				intervalRows(t, rows, want)
			}
			componentRows, err := dest.Query("SELECT id,v FROM ivcomponents ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			intervalRows(t, componentRows, [][]any{{int64(1), "-1 years -2 mons +3 days -2562047788:00:54.775808"}, {int64(2), "-2562047788:00:54.775807"}, {int64(3), "2562047788:00:54.775807"}, {int64(4), "00:00:00"}, {int64(5), nil}, {int64(6), "infinity"}, {int64(7), "-infinity"}})
			execSQL(t, dest, "INSERT INTO iv(id) VALUES(3)")
			rows, err := dest.Query("SELECT v FROM iv WHERE id=3")
			if err != nil {
				t.Fatal(err)
			}
			intervalRows(t, rows, [][]any{{"-2562047788:00:54.775808"}})
			var before bytes.Buffer
			if err := stuntgres.Dump(ctx, dest, &before); err != nil {
				t.Fatal(err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if err := stuntgres.Restore(canceled, dest, bytes.NewReader(canonical.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := dest.ExecContext(canceled, "INSERT INTO iv VALUES(4,'-9223372036854775808 microseconds')"); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(ctx, dest, bytes.NewBufferString("CREATE TABLE broken(v INTERVAL); INSERT INTO broken VALUES('1 second'),('-2562047788:00:54.775808')")); err == nil {
				t.Fatal("invalid clock restore succeeded")
			}
			var after bytes.Buffer
			if err := stuntgres.Dump(ctx, dest, &after); err != nil || after.String() != before.String() {
				t.Fatal(err, "failed/canceled operation changed catalog")
			}
			if err := dest.QueryRow("SHOW IntervalStyle").Scan(&gotStyle); err != nil || gotStyle != "postgres" {
				t.Fatal(gotStyle, err)
			}
		}
	}
}

func TestIntervalMinimumTimeRetainedCursor(t *testing.T) {
	ctx := context.Background()
	drv := stuntgres.NewDriver()
	db := openLocal(t, drv, t.Name())
	execSQL(t, db, "CREATE TABLE iv(v INTERVAL)")
	execSQL(t, db, "INSERT INTO iv VALUES('-9223372036854775808 microseconds')")
	raw, err := drv.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.(driver.ExecerContext).ExecContext(ctx, "SET IntervalStyle=iso_8601", nil); err != nil {
		t.Fatal(err)
	}
	held, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT v FROM iv", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := stuntgres.Restore(ctx, db, bytes.NewBufferString("CREATE TABLE iv(v INTERVAL); INSERT INTO iv VALUES('9223372036854775807 microseconds')")); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.(driver.ExecerContext).ExecContext(ctx, "SET IntervalStyle=postgres", nil); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	values := make([]driver.Value, 1)
	if err := held.Next(values); err != nil || values[0] != "PT-2562047788H-54.775808S" {
		t.Fatal(values, err)
	}
	if name := held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0); name != "INTERVAL" {
		t.Fatal(name)
	}
}
