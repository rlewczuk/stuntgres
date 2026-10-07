package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
)

func TestRandomSessionSavepointReadOnlyAndPool(t *testing.T) {
	db, c := randomConnection(t)
	ctx := context.Background()
	randomSeed(t, c, .25)
	first := randomOne(t, c, "SELECT random()")
	second := randomOne(t, c, "SELECT random()")
	exec := func(q string) {
		t.Helper()
		if _, e := c.ExecContext(ctx, q); e != nil {
			t.Fatal(q, e)
		}
	}
	randomSeed(t, c, .25)
	exec("BEGIN")
	exec("SAVEPOINT mark")
	randomOne(t, c, "SELECT random()")
	exec("ROLLBACK TO SAVEPOINT mark")
	if randomOne(t, c, "SELECT random()") != second {
		t.Fatal("savepoint rewound draw")
	}
	exec("ROLLBACK")
	exec("BEGIN")
	exec("SAVEPOINT mark")
	randomSeed(t, c, .25)
	exec("ROLLBACK TO SAVEPOINT mark")
	if randomOne(t, c, "SELECT random()") != first {
		t.Fatal("savepoint undid seed")
	}
	exec("ROLLBACK")
	exec("BEGIN")
	randomSeed(t, c, .25)
	exec("ROLLBACK")
	if randomOne(t, c, "SELECT random()") != first {
		t.Fatal("rollback undid seed")
	}
	exec("BEGIN READ ONLY")
	randomSeed(t, c, .25)
	randomOne(t, c, "SELECT random()")
	randomOne(t, c, "SELECT random_normal()")
	exec("COMMIT")
	identity := func(conn *sql.Conn) any {
		t.Helper()
		var id any
		if e := conn.Raw(func(raw any) error { id = raw; return nil }); e != nil {
			t.Fatal(e)
		}
		return id
	}
	before := identity(c)
	db.SetMaxIdleConns(0)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	randomSeed(t, c, .25)
	if randomOne(t, c, "SELECT random()") != first {
		t.Fatal("baseline")
	}
	if e := c.Close(); e != nil {
		t.Fatal(e)
	}
	next, e := db.Conn(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	if identity(next) != before {
		t.Fatal("pool replaced seeded physical connection")
	}
	if randomOne(t, next, "SELECT random()") != second {
		t.Fatal("pool return reset stream")
	}
	// Discarding the old physical connection must not discard catalog data.
	if _, e = next.ExecContext(ctx, "CREATE TABLE sentinel(id INT)"); e != nil {
		t.Fatal(e)
	}
	if _, e = next.ExecContext(ctx, "INSERT INTO sentinel VALUES(7)"); e != nil {
		t.Fatal(e)
	}
	db.SetMaxIdleConns(0)
	next.Close()
	replacement, e := db.Conn(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer replacement.Close()
	if identity(replacement) == before {
		t.Fatal("forced physical replacement reused closed session")
	}
	randomSeed(t, replacement, .25)
	if randomOne(t, replacement, "SELECT random()") != first {
		t.Fatal("replacement independent seed")
	}
	if randomOne(t, replacement, "SELECT id FROM sentinel") != int64(7) {
		t.Fatal("physical close lost catalog")
	}
}

func TestRandomControlsPreparedAndCancellation(t *testing.T) {
	db, c := randomConnection(t)
	ctx := context.Background()
	if _, e := c.ExecContext(ctx, "CREATE TABLE materialized(id INT,n NUMERIC,f DOUBLE PRECISION)"); e != nil {
		t.Fatal(e)
	}
	if _, e := c.ExecContext(ctx, "CREATE TABLE ids(id INT)"); e != nil {
		t.Fatal(e)
	}
	if _, e := c.ExecContext(ctx, "INSERT INTO ids VALUES(7)"); e != nil {
		t.Fatal(e)
	}
	if _, e := c.ExecContext(ctx, "INSERT INTO materialized SELECT id,random(1.0,1.000),random_normal(5,0) FROM ids"); e != nil {
		t.Fatal(e)
	}
	randomSeed(t, c, .25)
	first := randomOne(t, c, "SELECT random()")
	second := randomOne(t, c, "SELECT random()")
	randomSeed(t, c, .25)
	stmt, e := c.PrepareContext(ctx, "SELECT random()")
	if e != nil {
		t.Fatal(e)
	}
	defer stmt.Close()
	held, e := c.QueryContext(ctx, "SELECT random(1.0,1.000)")
	if e != nil {
		t.Fatal(e)
	}
	defer held.Close()
	var dump bytes.Buffer
	if e = stuntgres.Dump(ctx, db, &dump); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(dump.String(), "setseed") || strings.Contains(dump.String(), "random(") {
		t.Fatal("dump contains live generator state")
	}
	if e = stuntgres.Reset(ctx, db); e != nil {
		t.Fatal(e)
	}
	if e = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); e != nil {
		t.Fatal(e)
	}
	types, e := held.ColumnTypes()
	if e != nil || types[0].DatabaseTypeName() != "NUMERIC" {
		t.Fatal(types, e)
	}
	if !held.Next() {
		t.Fatal(held.Err())
	}
	var n string
	if e = held.Scan(&n); e != nil || n != "1.000" {
		t.Fatal(n, e)
	}
	held.Close()
	var draw any
	if e = stmt.QueryRow().Scan(&draw); e != nil || draw != first {
		t.Fatal("prepare/control advanced or reset stream", draw, first, e)
	}
	if randomOne(t, c, "SELECT random()") != second {
		t.Fatal("catalog replacement reset stream")
	}
	wantTuples(t, db, "SELECT id,n,f FROM materialized", [][]any{{int64(7), "1.000", float64(5)}})
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	randomSeed(t, c, .25)
	if _, e = c.ExecContext(canceled, "SELECT setseed(-1)"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if randomOne(t, c, "SELECT random()") != first {
		t.Fatal("pre-canceled seed mutated stream")
	}
}

func TestRandomNormalSpecialsAndSeedEndpoints(t *testing.T) {
	_, c := randomConnection(t)
	for _, seed := range []float64{-1, 1, 0, math.Copysign(0, -1)} {
		randomSeed(t, c, seed)
		a := randomOne(t, c, "SELECT random()")
		randomSeed(t, c, seed)
		if randomOne(t, c, "SELECT random()") != a {
			t.Fatal("seed endpoint")
		}
	}
	randomSeed(t, c, 0)
	zero := randomOne(t, c, "SELECT random()")
	randomSeed(t, c, math.Copysign(0, -1))
	if randomOne(t, c, "SELECT random()") != zero {
		t.Fatal("signed-zero seed")
	}
	randomSeed(t, c, .25)
	positive := randomOne(t, c, "SELECT random_normal(0,2)").(float64)
	randomSeed(t, c, .25)
	negative := randomOne(t, c, "SELECT random_normal(0,-2)").(float64)
	if negative != -positive {
		t.Fatal("negative deviation", negative, positive)
	}
	for _, q := range []string{"SELECT random_normal('NaN',1)", "SELECT random_normal(0,'NaN')"} {
		if !math.IsNaN(randomOne(t, c, q).(float64)) {
			t.Fatal(q)
		}
	}
	for _, tc := range []struct {
		q    string
		sign int
	}{{"SELECT random_normal('Infinity',1)", 1}, {"SELECT random_normal('-Infinity',1)", -1}} {
		if !math.IsInf(randomOne(t, c, tc.q).(float64), tc.sign) {
			t.Fatal(tc.q)
		}
	}
	for _, q := range []string{"SELECT random(NULL,1)", "SELECT random(1,NULL)", "SELECT random_normal(0,NULL)", "SELECT random(COALESCE('NaN',1.0),NULL)"} {
		if v := randomOne(t, c, q); v != nil {
			t.Fatal(q, v)
		}
	}
	if _, e := c.ExecContext(context.Background(), "CREATE TABLE bad(v void)"); e == nil {
		t.Fatal("result-only VOID admitted storage")
	}
}

func TestRandomRowsGroupsAndAtomicity(t *testing.T) {
	_, c := randomConnection(t)
	ctx := context.Background()
	exec := func(q string) {
		t.Helper()
		if _, e := c.ExecContext(ctx, q); e != nil {
			t.Fatal(q, e)
		}
	}
	exec("CREATE TABLE source(id INT,lo INT,hi INT)")
	exec("INSERT INTO source VALUES(1,-7,3),(2,10,20),(3,30,40)")
	collect := func(q string) []any {
		t.Helper()
		rows, e := c.QueryContext(ctx, q)
		if e != nil {
			t.Fatal(q, e)
		}
		defer rows.Close()
		var values []any
		for rows.Next() {
			var x any
			if e = rows.Scan(&x); e != nil {
				t.Fatal(e)
			}
			values = append(values, x)
		}
		if rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		return values
	}
	randomSeed(t, c, .25)
	rows := collect("SELECT random(lo,hi) FROM source ORDER BY id")
	if len(rows) != 3 {
		t.Fatal(rows)
	}
	randomSeed(t, c, .25)
	for i, bounds := range [][2]int64{{-7, 3}, {10, 20}, {30, 40}} {
		n, ok := rows[i].(int64)
		if !ok || n < bounds[0] || n > bounds[1] {
			t.Fatal("row range", i, rows[i])
		}
		draw := randomOne(t, c, "SELECT random(lo,hi) FROM source WHERE id=$1", i+1)
		if draw != rows[i] {
			t.Fatal("per-row volatile draw", i, draw, rows[i])
		}
	}
	// Reseeded ordered per-row execution repeats without mutating held analysis.
	randomSeed(t, c, .25)
	if again := collect("SELECT random(lo,hi) FROM source ORDER BY id"); !reflect.DeepEqual(rows, again) {
		t.Fatal(rows, again)
	}
	if v := randomOne(t, c, "SELECT random(count(*),count(*)) FROM source"); v != int64(3) {
		t.Fatal("aggregate-child rewrite", v)
	}
	randomSeed(t, c, .25)
	baseline := randomOne(t, c, "SELECT random()")
	randomSeed(t, c, .25)
	prepared, e := c.PrepareContext(ctx, "SELECT setseed(2) FROM source")
	if e != nil {
		t.Fatal("prepare invoked seed", e)
	}
	prepared.Close()
	if randomOne(t, c, "SELECT random()") != baseline {
		t.Fatal("prepare consumed state")
	}
	exec("CREATE TABLE dest(v INT)")
	exec("INSERT INTO dest VALUES(99)")
	exec("INSERT INTO source VALUES(4,2,1)")
	randomSeed(t, c, .25)
	for i := range 3 {
		randomOne(t, c, "SELECT random(lo,hi) FROM source WHERE id=$1", i+1)
	}
	continuation := randomOne(t, c, "SELECT random()")
	randomSeed(t, c, .25)
	if _, e := c.ExecContext(ctx, "INSERT INTO dest SELECT random(lo,hi) FROM source ORDER BY id"); e == nil {
		t.Fatal("invalid final row accepted")
	}
	if v := randomOne(t, c, "SELECT v FROM dest"); v != int64(99) {
		t.Fatal("partial INSERT publication", v)
	}
	if randomOne(t, c, "SELECT random()") != continuation {
		t.Fatal("statement failure undid consumed draws")
	}
}
