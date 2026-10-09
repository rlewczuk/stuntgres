package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func deparseRelationsScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func deparseRelationsSeed(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	for _, s := range []referenceStep{
		referenceExec("CREATE TABLE t(id INT,txt TEXT,visible BOOL)", 0),
		referenceExec("INSERT INTO t VALUES(1,'a',true),(2,'b',false),(3,NULL,true),(3,NULL,true)", 4),
		referenceExec("CREATE VIEW v(k,label) AS SELECT id,txt FROM t WHERE visible", 0),
		referenceExec("CREATE INDEX ix ON t(id ASC NULLS LAST,txt DESC NULLS LAST,id ASC NULLS FIRST)", 0),
	} {
		referenceRunStep(t, p.ctx, c, b.recorder, s)
	}
}

func deparseRelationsTextStep(t *testing.T, b referenceBackend, q string, want any, args ...any) referenceStep {
	t.Helper()
	s := referenceSelect(q, []string{"definition"}, []string{"TEXT"}, [][]any{{want}}, args...)
	s.checkMetadata = func(t *testing.T, r *sql.Rows) { deparseRelationsMetadata(t, b.pg, r) }
	return s
}

func deparseRelationsMetadata(t *testing.T, pg bool, r *sql.Rows) {
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
func deparseRelationsRead(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, q string, args ...any) string {
	t.Helper()
	r, err := c.QueryContext(p.ctx, q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	deparseRelationsMetadata(t, b.pg, r)
	if !r.Next() {
		t.Fatal("missing definition", r.Err())
	}
	var raw any
	if err := r.Scan(&raw); err != nil {
		t.Fatal(err)
	}
	referenceAssertRawValue(t, b.pg, "TEXT", raw)
	definition, ok := referenceNormalize(raw).(string)
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

func deparseRelationsBag(q string, extra bool) referenceStep {
	rows := [][]any{{int64(1), "a"}, {int64(3), nil}, {int64(3), nil}}
	if extra {
		rows = append(rows, []any{int64(4), "d"})
	}
	s := referenceSelect(q, []string{"k", "label"}, []string{"INT4", "TEXT"}, rows)
	s.bag = true
	return s
}
