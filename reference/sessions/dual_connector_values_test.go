package reference_test

import (
	"bytes"
	"database/sql"
	"math"
	"slices"
	"stuntgres/dual"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// TestReferenceDualConnectorValues checks that the default normalization
// bridges the value representations of the native driver and lib/pq: with
// REFERENCE=1 no record may differ.
func TestReferenceDualConnectorValues(t *testing.T) {
	p := fixture.NewScenario(t)
	d := newDualPair(t, p, dual.Options{}, nil)
	ctx := p.Ctx
	// The int4[] item is aliased so this test stays about value normalization:
	// its default column name is a separate native naming question.
	const direct = `SELECT 1::int2, 2::int4, 3::int8, true, false, 0.1::float4, 0.1::float8, 'NaN'::float8,
		1.50::numeric, 'abc'::text, 'x'::varchar, '\x00ff'::bytea, '2024-02-29'::date, '12:34:56.789'::time,
		'12:34:56+05:30'::timetz, '2024-01-02 03:04:05.123456'::timestamp, '2024-01-02 03:04:05+02'::timestamptz,
		'infinity'::timestamp, '1 day 02:00:00'::interval, 'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11'::uuid,
		'{"a": 1}'::json, '{"b":2,"a":1}'::jsonb, '{1,2}'::int4[] AS int4_array, NULL::text, 'int4'::regtype::oid`
	var (
		i2, i4, i8, oid                     int64
		yes, no                             bool
		f4, f8, nan                         float64
		numeric, text, varchar              string
		bytea                               []byte
		date, clock, clockTZ                string
		stamp, stampTZ                      time.Time
		infinity, interval, uuid, js, jsonb string
		array                               string
		null                                sql.NullString
	)
	err := d.DB.QueryRowContext(ctx, direct).Scan(&i2, &i4, &i8, &yes, &no, &f4, &f8, &nan, &numeric, &text, &varchar,
		&bytea, &date, &clock, &clockTZ, &stamp, &stampTZ, &infinity, &interval, &uuid, &js, &jsonb, &array, &null, &oid)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		ok   bool
	}{
		{"int2", i2 == 1}, {"int4", i4 == 2}, {"int8", i8 == 3}, {"bool", yes && !no},
		{"float4", f4 == float64(float32(0.1))}, {"float8", f8 == 0.1}, {"NaN", math.IsNaN(nan)},
		{"numeric", numeric == "1.50"}, {"text", text == "abc"}, {"varchar", varchar == "x"},
		{"bytea", bytes.Equal(bytea, []byte{0, 0xff})}, {"date", date == "2024-02-29"},
		{"time", clock == "12:34:56.789"}, {"timetz", clockTZ == "12:34:56+05:30"},
		{"timestamp", stamp.Equal(time.Date(2024, 1, 2, 3, 4, 5, 123456000, time.UTC))},
		{"timestamptz", stampTZ.Equal(time.Date(2024, 1, 2, 1, 4, 5, 0, time.UTC))},
		{"infinity", infinity == "infinity"}, {"interval", interval == "1 day 02:00:00"},
		{"uuid", uuid == "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"}, {"json", js == `{"a": 1}`},
		{"jsonb", jsonb == `{"a": 1, "b": 2}`}, {"int4[]", array == "{1,2}"}, {"NULL", !null.Valid}, {"oid", oid == 23},
	} {
		if !c.ok {
			t.Errorf("caller %s value: i2 %d i4 %d i8 %d bool %t/%t f4 %v f8 %v nan %v numeric %q text %q varchar %q bytea %x date %q time %q timetz %q stamp %v stamptz %v infinity %q interval %q uuid %q json %q jsonb %q array %q null %v oid %d",
				c.name, i2, i4, i8, yes, no, f4, f8, nan, numeric, text, varchar, bytea, date, clock, clockTZ, stamp, stampTZ, infinity, interval, uuid, js, jsonb, array, null, oid)
		}
	}
	r := lastQuery(t, d.Log.records(t), "query", direct)
	wantDiff(t, r)
	types := []string{"INT2", "INT4", "INT8", "BOOL", "BOOL", "FLOAT4", "FLOAT8", "FLOAT8", "NUMERIC", "TEXT",
		"VARCHAR", "BYTEA", "DATE", "TIME", "TIMETZ", "TIMESTAMP", "TIMESTAMPTZ", "TIMESTAMP", "INTERVAL", "UUID",
		"JSON", "JSONB", "_INT4", "TEXT", "OID"}
	if sets := decodeSets(t, r.Response).Sets; len(sets) != 1 || !slices.Equal(sets[0].Types, types) {
		t.Errorf("native types %+v, want %q", sets, types)
	}

	// Parameters make lib/pq use binary result formats for these types.
	const prepared = "SELECT $1::bytea, $2::uuid, $3::int8, $4::timestamptz"
	c, err := d.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	stmt, err := c.PrepareContext(ctx, prepared)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	at := time.Date(2024, 1, 2, 3, 4, 5, 0, time.FixedZone("", 2*3600))
	var pb []byte
	var pu string
	var pi int64
	var pt time.Time
	if err := stmt.QueryRowContext(ctx, []byte{0, 0xff}, "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", int64(42), at).Scan(&pb, &pu, &pi, &pt); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pb, []byte{0, 0xff}) || pu != "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11" || pi != 42 || !pt.Equal(at) {
		t.Errorf("prepared caller values %x %q %d %v", pb, pu, pi, pt)
	}
	r = lastQuery(t, d.Log.records(t), "stmt_query", prepared)
	wantDiff(t, r)
	if sets := decodeSets(t, r.Response).Sets; len(sets) != 1 || !slices.Equal(sets[0].Types, []string{"BYTEA", "UUID", "INT8", "TIMESTAMPTZ"}) {
		t.Errorf("prepared native types %+v", sets)
	}
}
