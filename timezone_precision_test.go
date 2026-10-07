package stuntgres_test

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestTimezonePrecisionDeclarationsAndOccurrenceSizing(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c := timezoneConn(t, db)
	timezoneExec(t, c, "CREATE TABLE tz_witness(z TIMESTAMPTZ,z0 TIMESTAMPTZ(0),z3 TIMESTAMP(3) WITH TIME ZONE,z6 TIMESTAMPTZ(6),q TIMETZ,q0 TIMETZ(0),q3 TIME(3) WITH TIME ZONE,q6 TIMETZ(6))", 0)
	p, err := c.PrepareContext(ctx, "INSERT INTO tz_witness VALUES($1,$1,$1,$1,$2,$2,$2,$2)")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	r, err := p.ExecContext(ctx, "2004-01-01 00:00:00.123456+02", "12:00:00.123456-05")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := r.RowsAffected(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	timezoneRows(t, c, "SELECT z::TEXT,z0::TEXT,z3::TEXT,z6::TEXT,q,q0,q3,q6 FROM tz_witness", []string{"TEXT", "TEXT", "TEXT", "TEXT", "TIMETZ", "TIMETZ", "TIMETZ", "TIMETZ"}, [][]any{{"2003-12-31 22:00:00.123456+00", "2003-12-31 22:00:00+00", "2003-12-31 22:00:00.123+00", "2003-12-31 22:00:00.123456+00", "12:00:00.123456-05", "12:00:00-05", "12:00:00.123-05", "12:00:00.123456-05"}})
	for _, head := range []string{"TIMESTAMP", "TIME"} {
		suffix := " WITH TIME ZONE"
		sql := "CREATE TABLE bad(v " + head + "(-1)" + suffix + ")"
		want := stuntgres.Error{Code: "42601", Message: "syntax error at or near \"-\"", Position: strconv.Itoa(strings.Index(sql, "-") + 1)}
		_, err := c.ExecContext(ctx, sql)
		timeDiagnostic(t, err, want)
		prepared, err := c.PrepareContext(ctx, sql)
		if prepared != nil {
			prepared.Close()
			t.Fatal("negative precision prepared")
		}
		timeDiagnostic(t, err, want)
	}
	_, err = c.PrepareContext(ctx, "SELECT $1 FROM tz_witness WHERE z=$1")
	timeDiagnostic(t, err, stuntgres.Error{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "timestamp with time zone versus text", Position: "8"})
}

func TestTimezonePrecisionWarnings(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	raw, err := d.OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	var notices []stuntgres.Error
	db := sql.OpenDB(raw.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) { notices = append(notices, n) }))
	defer db.Close()
	c := timezoneConn(t, db)
	timezoneExec(t, c, "CREATE TABLE tz_witness(v INTEGER)", 0)
	timezoneExec(t, c, "INSERT INTO tz_witness VALUES(1)", 1)
	for _, tc := range []struct{ sql, typ, want, message string }{
		{"SELECT TIMESTAMPTZ(7) '2004-01-01 00:00:00.123456+00'::TEXT FROM tz_witness", "TEXT", "2004-01-01 00:00:00.123456+00", "TIMESTAMP(7) WITH TIME ZONE precision reduced to maximum allowed, 6"},
		{"SELECT TIMETZ(7) '12:00:00.123456+02' FROM tz_witness", "TIMETZ", "12:00:00.123456+02", "TIME(7) WITH TIME ZONE precision reduced to maximum allowed, 6"},
	} {
		notices = nil
		timezoneRows(t, c, tc.sql, []string{tc.typ}, [][]any{{tc.want}})
		if len(notices) != 1 || notices[0].Severity != "WARNING" || notices[0].SeverityUnlocalized != "WARNING" || notices[0].Code != "22023" || notices[0].Message != tc.message {
			t.Fatal(notices, tc.message)
		}
	}
	notices = nil
	timezoneExec(t, c, "SET TIME ZONE INTERVAL (7) '1 second'", 0)
	if len(notices) != 1 || notices[0].Severity != "WARNING" || notices[0].SeverityUnlocalized != "WARNING" || notices[0].Code != "22023" || notices[0].Message != "INTERVAL(7) precision reduced to maximum allowed, 6" {
		t.Fatal(notices)
	}
	timezoneRows(t, c, "SELECT TIMESTAMPTZ '2004-01-01 12:00+00'::TEXT FROM tz_witness", []string{"TEXT"}, [][]any{{"2004-01-01 12:00:01+00:00:01"}})
	sql := "SET TIME ZONE INTERVAL (-1) '1 second'"
	_, err = c.ExecContext(ctx, sql)
	timeDiagnostic(t, err, stuntgres.Error{Code: "42601", Message: "syntax error at or near \"-\"", Position: strconv.Itoa(strings.Index(sql, "-") + 1)})
}

func TestTimezoneFiniteUpperTypmodCarry(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c := timezoneConn(t, db)
	timezoneExec(t, c, "CREATE TABLE tz_witness(v INTEGER)", 0)
	timezoneExec(t, c, "INSERT INTO tz_witness VALUES(1)", 1)
	sql := "SELECT (TIMESTAMPTZ '294276-12-31 23:59:59.999999+00')::TIMESTAMPTZ(0)::TEXT FROM tz_witness"
	// Root observed this successful carry independently on pinned PostgreSQL18.0.
	// Initial input range validation and typmod rounding do not have identical bounds.
	timezoneRows(t, c, sql, []string{"TEXT"}, [][]any{{"294277-01-01 00:00:00+00"}})
	prepared, err := c.PrepareContext(ctx, sql)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	var got string
	if err = prepared.QueryRowContext(ctx).Scan(&got); err != nil || got != "294277-01-01 00:00:00+00" {
		t.Fatal(got, err)
	}
}
