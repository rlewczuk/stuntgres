//go:build reference

package reference_test

import (
	"database/sql"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestReferenceRandomVoid(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name string
		db   *sql.DB
	}{{"postgres", p.postgres}, {"native", p.memory}} {
		t.Run(backend.name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				q := "SELECT setseed(0),setseed(NULL)"
				var rows *sql.Rows
				var err error
				if prepared {
					s, e := backend.db.PrepareContext(p.ctx, q)
					if e != nil {
						t.Fatal(e)
					}
					defer s.Close()
					rows, err = s.QueryContext(p.ctx)
				} else {
					rows, err = backend.db.QueryContext(p.ctx, q)
				}
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 2 {
					t.Fatal(types, err)
				}
				for _, typ := range types {
					if typ.Name() != "setseed" || typ.DatabaseTypeName() != "VOID" {
						t.Fatal(typ)
					}
					want := reflect.TypeFor[string]()
					if backend.name == "postgres" {
						want = reflect.TypeFor[any]()
					}
					if typ.ScanType() != want {
						t.Fatal("adapter ScanType", typ.ScanType(), want)
					}
					if _, ok := typ.Length(); ok {
						t.Fatal("VOID length")
					}
					if _, _, ok := typ.DecimalSize(); ok {
						t.Fatal("VOID decimal size")
					}
				}
				if !rows.Next() {
					t.Fatal(rows.Err())
				}
				var value, null any
				if err = rows.Scan(&value, &null); err != nil {
					t.Fatal(err)
				}
				if null != nil {
					t.Fatal("VOID NULL", null)
				}
				if backend.name == "postgres" {
					raw, ok := value.([]byte)
					if !ok || raw == nil || len(raw) != 0 {
						t.Fatalf("pq nonnull VOID %T %#v", value, value)
					}
				} else if value != "" {
					t.Fatal("native nonnull VOID", value)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal(rows.Err())
				}
				rows.Close()
			}
		})
	}
}

func TestReferenceRandomSessionLifecycle(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE null_inputs(id INT,mean DOUBLE PRECISION)", 0))
	p.step(t, referenceExec("INSERT INTO null_inputs VALUES(1,NULL),(2,1)", 2))
	for _, backend := range []struct {
		name string
		db   *sql.DB
	}{{"postgres", p.postgres}, {"native", p.memory}} {
		t.Run(backend.name, func(t *testing.T) {
			a, e := backend.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer a.Close()
			b, e := backend.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer b.Close()
			one := func(c *sql.Conn, q string, args ...any) any {
				t.Helper()
				var v any
				if e := c.QueryRowContext(p.ctx, q, args...).Scan(&v); e != nil {
					t.Fatal(q, e)
				}
				return v
			}
			exec := func(c *sql.Conn, q string) {
				t.Helper()
				if _, e := c.ExecContext(p.ctx, q); e != nil {
					t.Fatal(q, e)
				}
			}
			seed := func(c *sql.Conn) { exec(c, "SELECT setseed(0.25)") }
			seed(a)
			first := one(a, "SELECT random()")
			second := one(a, "SELECT random()")
			seed(a)
			seed(b)
			if one(b, "SELECT random()") != first {
				t.Fatal("peer seeded baseline")
			}
			one(a, "SELECT random()")
			exec(a, "SELECT setseed(-1)")
			if one(b, "SELECT random()") != second {
				t.Fatal("cross-session seed leaked")
			}
			seed(a)
			exec(a, "BEGIN")
			exec(a, "SAVEPOINT mark")
			one(a, "SELECT random()")
			exec(a, "ROLLBACK TO SAVEPOINT mark")
			if one(a, "SELECT random()") != second {
				t.Fatal("savepoint undid draw")
			}
			exec(a, "ROLLBACK")
			exec(a, "BEGIN")
			exec(a, "SAVEPOINT mark")
			seed(a)
			exec(a, "ROLLBACK TO SAVEPOINT mark")
			if one(a, "SELECT random()") != first {
				t.Fatal("savepoint undid seed")
			}
			exec(a, "ROLLBACK")
			exec(a, "BEGIN")
			seed(a)
			exec(a, "ROLLBACK")
			if one(a, "SELECT random()") != first {
				t.Fatal("rollback undid seed")
			}
			exec(a, "BEGIN READ ONLY")
			seed(a)
			one(a, "SELECT random()")
			one(a, "SELECT random_normal()")
			exec(a, "COMMIT")
			seed(a)
			if one(a, "SELECT random_normal(mean,random()) FROM null_inputs WHERE id=1") != nil {
				t.Fatal("runtime NULL")
			}
			if one(a, "SELECT random()") != second {
				t.Fatal("runtime NULL sibling was skipped")
			}
			// Retain exactly one physical session across pool return, independently
			// checking its identity (native through public Raw; PG through backend PID).
			backend.db.SetMaxIdleConns(0)
			b.Close()
			backend.db.SetMaxOpenConns(1)
			backend.db.SetMaxIdleConns(1)
			identity := func(c *sql.Conn) any {
				t.Helper()
				if backend.name == "postgres" {
					return one(c, "SELECT pg_backend_pid()")
				}
				var id any
				if e := c.Raw(func(raw any) error { id = raw; return nil }); e != nil {
					t.Fatal(e)
				}
				return id
			}
			before := identity(a)
			seed(a)
			one(a, "SELECT random()")
			a.Close()
			reused, e := backend.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer reused.Close()
			if identity(reused) != before || one(reused, "SELECT random()") != second {
				t.Fatal("physical pool reuse reset stream")
			}
		})
	}
}

func TestReferenceRandomNumericAndNormalEdges(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE rounded(lo NUMERIC(2,-3),hi NUMERIC(2,-3))", 0))
	p.step(t, referenceExec("INSERT INTO rounded VALUES(1500,1500)", 1))
	p.step(t, referenceSelect("SELECT random(lo,hi) FROM rounded", []string{"random"}, []string{"NUMERIC"}, [][]any{{"2000"}}))
	p.step(t, referenceExec("CREATE TABLE extremes(id INT,lo NUMERIC,hi NUMERIC)", 0))
	for _, backend := range []struct {
		name string
		db   *sql.DB
	}{{"postgres", p.postgres}, {"native", p.memory}} {
		t.Run(backend.name, func(t *testing.T) {
			c, e := backend.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			one := func(q string) any {
				t.Helper()
				var v any
				if e := c.QueryRowContext(p.ctx, q).Scan(&v); e != nil {
					t.Fatal(q[:min(len(q), 200)], e)
				}
				if raw, ok := v.([]byte); ok {
					return string(raw)
				}
				return v
			}
			seed := func() {
				t.Helper()
				if _, e := c.ExecContext(p.ctx, "SELECT setseed(0.25)"); e != nil {
					t.Fatal(e)
				}
			}
			for _, tc := range []struct {
				lo, hi string
				values []string
			}{
				{"100000000000000000000.000", "100000000000000000000.002", []string{"100000000000000000000.000", "100000000000000000000.001", "100000000000000000000.002"}},
				{"0.00000000000000000000", "0.00000000000000000002", []string{"0.00000000000000000000", "0.00000000000000000001", "0.00000000000000000002"}},
			} {
				for range 8 {
					v := one("SELECT random(" + tc.lo + "," + tc.hi + ")")
					found := false
					for _, want := range tc.values {
						found = found || v == want
					}
					if !found {
						t.Fatal("exact lattice", v)
					}
				}
			}
			for range 8 {
				text := one("SELECT random(-0.499,0.499)").(string)
				value, ok := new(big.Rat).SetString(text)
				dot := strings.IndexByte(text, '.')
				if !ok || dot < 0 || len(text)-dot-1 != 3 || value.Cmp(big.NewRat(-499, 1000)) < 0 || value.Cmp(big.NewRat(499, 1000)) > 0 {
					t.Fatal("exact negative/cross-zero lattice", text)
				}
			}
			for i, value := range []string{"0." + strings.Repeat("0", 16382) + "1", "1" + strings.Repeat("0", 131071)} {
				if _, e := c.ExecContext(p.ctx, "INSERT INTO extremes VALUES($1,$2,$3)", i+1, value, value); e != nil {
					t.Fatal("extreme numeric input", e)
				}
				v := one("SELECT random(lo,hi) FROM extremes WHERE id=" + strconv.Itoa(i+1))
				if v != value {
					t.Fatal("extreme numeric equal bound changed")
				}
			}
			seed()
			positive := one("SELECT random_normal(0,2)").(float64)
			seed()
			if negative := one("SELECT random_normal(0,-2)").(float64); negative != -positive {
				t.Fatal("negative deviation", positive, negative)
			}
			for _, q := range []string{"SELECT random_normal('NaN',1)", "SELECT random_normal(0,'NaN')"} {
				if !math.IsNaN(one(q).(float64)) {
					t.Fatal(q)
				}
			}
			for _, tc := range []struct {
				q    string
				sign int
			}{{"SELECT random_normal('Infinity',1)", 1}, {"SELECT random_normal('-Infinity',1)", -1}} {
				if !math.IsInf(one(tc.q).(float64), tc.sign) {
					t.Fatal(tc.q)
				}
			}
			seed()
			implicit := one("SELECT random_normal(5)")
			seed()
			if one("SELECT random_normal(5,1)") != implicit {
				t.Fatal("one-argument defaults")
			}
		})
	}
}

func TestReferenceRandomGroupingAndTyping(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE random_source(id INT,lo INT,hi INT)", 0))
	p.step(t, referenceExec("INSERT INTO random_source VALUES(1,7,7),(2,7,7),(3,9,9)", 3))
	for _, s := range []referenceStep{
		referenceSelect("SELECT random(lo,hi) FROM random_source ORDER BY id", []string{"random"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}, {int64(9)}}),
		referenceSelect("SELECT random(count(*),count(*)) FROM random_source", []string{"random"}, []string{"INT8"}, [][]any{{int64(3)}}),
		referenceSelect("SELECT random(lo,hi),count(*) FROM random_source GROUP BY 1", []string{"random", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(7), int64(2)}, {int64(9), int64(1)}}),
		referenceSelect("SELECT random(7,$1)", []string{"random"}, []string{"INT4"}, [][]any{{int64(7)}}, 7),
		referenceSelect("SELECT random(CASE WHEN TRUE THEN 7 ELSE 1/0 END,7) FROM random_source WHERE id=1", []string{"random"}, []string{"INT4"}, [][]any{{int64(7)}}),
	} {
		s.bag = strings.Contains(s.query, "GROUP BY")
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	for _, backend := range []struct {
		name string
		db   *sql.DB
	}{{"postgres", p.postgres}, {"native", p.memory}} {
		t.Run(backend.name, func(t *testing.T) {
			rows, e := backend.db.QueryContext(p.ctx, "SELECT random(7,2147483648)")
			if e != nil {
				t.Fatal(e)
			}
			defer rows.Close()
			types, e := rows.ColumnTypes()
			if e != nil || types[0].DatabaseTypeName() != "INT8" {
				t.Fatal(types, e)
			}
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			var n int64
			if e = rows.Scan(&n); e != nil || n < 7 || n > 2147483648 {
				t.Fatal(n, e)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("mixed overload extra rows", rows.Err())
			}
			rows.Close()
			c, e := backend.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			one := func(q string) any {
				t.Helper()
				var v any
				if e := c.QueryRowContext(p.ctx, q).Scan(&v); e != nil {
					t.Fatal(q, e)
				}
				return v
			}
			seed := func() {
				t.Helper()
				if _, e := c.ExecContext(p.ctx, "SELECT setseed(0.25)"); e != nil {
					t.Fatal(e)
				}
			}
			seed()
			counts := map[int64]int64{}
			for range 3 {
				counts[one("SELECT random(0,2147483647)").(int64)]++
			}
			continuation := one("SELECT random()")
			seed()
			grouped, e := c.QueryContext(p.ctx, "SELECT random(0,2147483647),count(*) FROM random_source GROUP BY 1")
			if e != nil {
				t.Fatal(e)
			}
			got := map[int64]int64{}
			for grouped.Next() {
				var key, count int64
				if e = grouped.Scan(&key, &count); e != nil {
					t.Fatal(e)
				}
				got[key] += count
			}
			if grouped.Err() != nil {
				t.Fatal(grouped.Err())
			}
			grouped.Close()
			if !reflect.DeepEqual(got, counts) || one("SELECT random()") != continuation {
				t.Fatal("group key was redrawn", got, counts)
			}
			for _, q := range []string{"SELECT random(-2147483648,2147483647)", "SELECT random(-9223372036854775808,9223372036854775807)"} {
				if _, ok := one(q).(int64); !ok {
					t.Fatal("full signed range", q)
				}
			}
		})
	}
}
