package reference_test

import (
	"reflect"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceCopyTextCodecs(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TYPE copy_mood AS ENUM('sad','ok','happy')")
		c.exec(t, "CREATE DOMAIN copy_integer AS int")
		c.exec(t, "CREATE TABLE copy_symbol(id int)")
		c.exec(t, "CREATE TABLE copy_types(s int2,i int4,l int8,n numeric(6,2),v varchar(4),b bool,x bytea,d date,t timestamp,z timestamptz,r interval,a int[],p copy_integer,e copy_mood,u uuid,c regclass,y regtype)")
		c.exec(t, "SET TIME ZONE 'UTC'")
		input := "12\t34\t56\t12.345\ttext\ttrue\t\\\\x00ff\t2020-02-03\t2020-02-03 04:05:06\t2020-02-03 04:05:06+00\t1 day 02:03:04\t{1,NULL,3}\t7\thappy\t12345678-1234-5678-9abc-123456789abc\tcopy_symbol\tinteger\n"
		result, err := c.from("COPY copy_types FROM STDIN", strings.NewReader(input))
		copyTextSuccess(t, result, err, 1)
		golden := "12\t34\t56\t12.35\ttext\tt\t\\\\x00ff\t2020-02-03\t2020-02-03 04:05:06\t2020-02-03 04:05:06+00\t1 day 02:03:04\t{1,NULL,3}\t7\thappy\t12345678-1234-5678-9abc-123456789abc\tcopy_symbol\tinteger\n"
		c.output(t, "COPY copy_types TO STDOUT", golden, 1)
		copyTextRawCodecs(t, c)
		c.exec(t, "SET DateStyle TO 'SQL, DMY'")
		c.exec(t, "SET IntervalStyle TO 'iso_8601'")
		c.exec(t, "SET bytea_output TO 'escape'")
		c.output(t, "COPY (SELECT d,r,x FROM copy_types) TO STDOUT", "03/02/2020\tP1DT2H3M4S\t\\\\000\\\\377\n", 1)
		c.exec(t, "CREATE TABLE copy_typmod(v varchar(3))")
		result, err = c.from("COPY copy_typmod FROM STDIN", strings.NewReader("ok\ntoolong\n"))
		copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "22001", Message: "value too long for type character varying(3)", Context: `COPY copy_typmod, line 2, column v: "toolong"`})
		c.selectRows(t, "SELECT * FROM copy_typmod", []string{"v"}, []string{"VARCHAR"}, [][]any{})
		c.exec(t, "CREATE TABLE copy_domain(v copy_integer)")
		result, err = c.from("COPY copy_domain FROM STDIN", strings.NewReader("1\nbad\n"))
		copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: `COPY copy_domain, line 2, column v: "bad"`})
		c.selectRows(t, "SELECT * FROM copy_domain", []string{"v"}, []string{"INT4"}, [][]any{})
	})
}

func TestReferenceCopyTextIdentityGenerated(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_identity(id int GENERATED ALWAYS AS IDENTITY,v text,g int GENERATED ALWAYS AS(id+1) STORED)")
		result, err := c.from("COPY copy_identity FROM STDIN", strings.NewReader("99\texplicit\n"))
		copyTextSuccess(t, result, err, 1)
		result, err = c.from("COPY copy_identity(v) FROM STDIN", strings.NewReader("omitted\n"))
		copyTextSuccess(t, result, err, 1)
		result, err = c.from("COPY copy_identity FROM STDIN WITH(DEFAULT 'DEF')", strings.NewReader("DEF\tmarked\n"))
		copyTextSuccess(t, result, err, 1)
		c.selectRows(t, "SELECT * FROM copy_identity ORDER BY id", []string{"id", "v", "g"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), "omitted", int64(2)}, {int64(2), "marked", int64(3)}, {int64(99), "explicit", int64(100)}})
		c.output(t, "COPY (SELECT * FROM copy_identity ORDER BY id) TO STDOUT", "1\tomitted\t2\n2\tmarked\t3\n99\texplicit\t100\n", 3)
		for _, q := range []string{"COPY copy_identity(g) FROM STDIN", "COPY copy_identity(g) TO STDOUT"} {
			if strings.Contains(q, "FROM") {
				result, err = c.from(q, strings.NewReader("1\n"))
			} else {
				result, err = c.to(q, &strings.Builder{})
			}
			copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "42P10", Message: `column "g" is a generated column`, Detail: "Generated columns cannot be used in COPY."})
		}
	})
}

func copyTextRawCodecs(t *testing.T, c copyTextConnection) {
	t.Helper()
	names := []string{"s", "i", "l", "n", "v", "b", "x", "d", "t", "z", "r", "a", "p", "e", "u", "c", "y"}
	types := []string{"INT2", "INT4", "INT8", "NUMERIC", "VARCHAR", "BOOL", "BYTEA", "DATE", "TIMESTAMP", "TIMESTAMPTZ", "INTERVAL", "_INT4", "INT4", "", "UUID", "REGCLASS", "REGTYPE"}
	var oid int64
	var name string
	if err := c.conn.QueryRowContext(c.ctx, "SELECT oid,typname FROM pg_type WHERE typname='copy_mood'").Scan(&oid, &name); err != nil {
		t.Fatal(err)
	}
	if oid <= 0 || name != "copy_mood" {
		t.Fatal("oracle enum catalog identity", oid, name)
	}
	if c.pg {
		// pgx leaves these built-in symbolic OIDs unregistered as well.
		types[15], types[16] = "2205", "2206"
		// stdlib's unregistered custom-type tag is its genuine server OID.
		types[13] = strconv.FormatInt(oid, 10)
	}
	rows, err := c.conn.QueryContext(c.ctx, "SELECT * FROM copy_types")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	gotNames, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(gotNames, names) {
		t.Fatal("raw codec columns", gotNames, err)
	}
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, column := range columns {
		if column.DatabaseTypeName() != types[i] {
			t.Fatalf("raw codec metadata %s=%q want=%q", names[i], column.DatabaseTypeName(), types[i])
		}
	}
	if !rows.Next() {
		t.Fatal("missing raw codec row", rows.Err())
	}
	actual := make([]any, len(names))
	destinations := make([]any, len(names))
	for i := range actual {
		destinations[i] = &actual[i]
	}
	if err = rows.Scan(destinations...); err != nil {
		t.Fatal(err)
	}
	if c.pg {
		date, ok := actual[7].(time.Time)
		if !ok || !date.Equal(time.Date(2020, 2, 3, 0, 0, 0, 0, time.UTC)) {
			t.Fatalf("pgx raw DATE=%#v", actual[7])
		}
		actual[7] = date.Format("2006-01-02")
	} else if actual[7] != "2020-02-03" {
		t.Fatalf("native raw DATE=%#v", actual[7])
	}
	instant := time.Date(2020, 2, 3, 4, 5, 6, 0, time.UTC)
	for _, index := range []int{8, 9} {
		value, ok := actual[index].(time.Time)
		if !ok || !value.Equal(instant) {
			t.Fatalf("raw %s=%#v", names[index], actual[index])
		}
		_, offset := value.Zone()
		wantOffset := 0
		if c.pg && index == 9 {
			// stdlib prefers binary TIMESTAMPTZ; pgtype's nil ScanLocation
			// preserves time.Unix's host-local location, not server TimeZone.
			_, wantOffset = instant.In(time.Local).Zone()
			if value.Location() != time.Local {
				t.Fatal("pgx binary TIMESTAMPTZ location", value.Location())
			}
		}
		if offset != wantOffset {
			t.Fatal("raw codec zone", names[index], offset, wantOffset)
		}
		actual[index] = value.UTC()
	}
	want := []any{int64(12), int64(34), int64(56), "12.35", "text", true, []byte{0, 255}, "2020-02-03", instant, instant, "1 day 02:03:04", "{1,NULL,3}", int64(7), "happy", "12345678-1234-5678-9abc-123456789abc", "copy_symbol", "integer"}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("raw codec row=%#v want=%#v", actual, want)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("raw codec multiplicity/end", rows.Err())
	}
}
