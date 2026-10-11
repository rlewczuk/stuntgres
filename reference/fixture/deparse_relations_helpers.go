package fixture

import (
	"database/sql"
	"reflect"
	"testing"
)

func DeparseRelationsScenario(t *testing.T, body func(*testing.T, *Pair, Backend, *sql.Conn)) {
	t.Helper()
	p := NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func DeparseRelationsSeed(t *testing.T, p *Pair, b Backend, c *sql.Conn) {
	t.Helper()
	for _, s := range []Step{
		Exec("CREATE TABLE t(id INT,txt TEXT,visible BOOL)", 0),
		Exec("INSERT INTO t VALUES(1,'a',true),(2,'b',false),(3,NULL,true),(3,NULL,true)", 4),
		Exec("CREATE VIEW v(k,label) AS SELECT id,txt FROM t WHERE visible", 0),
		Exec("CREATE INDEX ix ON t(id ASC NULLS LAST,txt DESC NULLS LAST,id ASC NULLS FIRST)", 0),
	} {
		RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func DeparseRelationsTextStep(t *testing.T, b Backend, q string, want any, args ...any) Step {
	t.Helper()
	s := Select(q, []string{"definition"}, []string{"TEXT"}, [][]any{{want}}, args...)
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) { DeparseRelationsMetadata(t, b.PG, r) }
	return s
}

func DeparseRelationsMetadata(t *testing.T, pg bool, r *sql.Rows) {
	t.Helper()
	cs, err := r.ColumnTypes()
	if err != nil || len(cs) != 1 {
		t.Fatal(cs, err)
	}
	c := cs[0]
	if c.Name() != "definition" || c.DatabaseTypeName() != "TEXT" {
		t.Fatal(c.Name(), c.DatabaseTypeName())
	}
	scan := reflect.TypeOf("")
	if c.ScanType() != scan {
		t.Fatalf("ScanType %v want %v", c.ScanType(), scan)
	}
	nullable, nok := c.Nullable()
	length, lok := c.Length()
	precision, scale, dok := c.DecimalSize()
	if nullable != !pg || nok != !pg || !lok || length != int64(9223372036854775807) || dok || precision != 0 || scale != 0 {
		t.Fatalf("TEXT metadata pg=%v nullable=%v/%v length=%v/%v decimal=%v,%v/%v", pg, nullable, nok, length, lok, precision, scale, dok)
	}
}

// Actual public QueryContext result, not a substituted pg_views catalog query.
// Replay independently proves the returned definition's consumer semantics.
func DeparseRelationsRead(t *testing.T, p *Pair, b Backend, c *sql.Conn, q string, args ...any) string {
	t.Helper()
	r, err := c.QueryContext(p.Ctx, q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	DeparseRelationsMetadata(t, b.PG, r)
	if !r.Next() {
		t.Fatal("missing definition", r.Err())
	}
	var raw any
	if err := r.Scan(&raw); err != nil {
		t.Fatal(err)
	}
	AssertRawValue(t, b.PG, "TEXT", raw)
	definition, ok := Normalize(raw).(string)
	if !ok {
		t.Fatalf("not nonnull TEXT: %#v", raw)
	}
	if r.Next() || r.Err() != nil {
		t.Fatal("extra definition", r.Err())
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return definition
}

func DeparseRelationsBag(q string, extra bool) Step {
	rows := [][]any{{int64(1), "a"}, {int64(3), nil}, {int64(3), nil}}
	if extra {
		rows = append(rows, []any{int64(4), "d"})
	}
	s := Select(q, []string{"k", "label"}, []string{"INT4", "TEXT"}, rows)
	s.Bag = true
	return s
}
