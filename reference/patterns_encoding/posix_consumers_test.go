package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePosixConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec(`CREATE TABLE t(id INT,v TEXT,p TEXT,sentinel INT)`, 0))
			run(fixture.Exec(`INSERT INTO t VALUES(1,'abc','b',7),(2,'XYZ','x',7),(3,NULL,'[',7)`, 3))
			for _, c := range []struct {
				q            string
				names, types []string
				rows         [][]any
			}{
				{`SELECT substring(v,p) AS piece FROM t WHERE v ~ p ORDER BY id`, []string{"piece"}, []string{"TEXT"}, [][]any{{"b"}}},
				{`SELECT DISTINCT v ~* p AS m FROM t ORDER BY m`, []string{"m"}, []string{"BOOL"}, [][]any{{true}, {nil}}},
				{`SELECT a.v ~ b.p AS m FROM t a JOIN t b ON a.id=b.id ORDER BY a.id`, []string{"m"}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}}},
				{`SELECT v ~ p AS m,count(*) AS n FROM t GROUP BY v ~ p HAVING v ~ p ORDER BY m`, []string{"m", "n"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(1)}}},
				{`SELECT CASE WHEN v ~ p THEN substring(v,p) ELSE 'no' END AS piece FROM t ORDER BY id`, []string{"piece"}, []string{"TEXT"}, [][]any{{"b"}, {"no"}, {"no"}}},
				{`SELECT COALESCE(substring(v,p),'missing') AS piece FROM t ORDER BY id`, []string{"piece"}, []string{"TEXT"}, [][]any{{"b"}, {"missing"}, {"missing"}}},
				{`SELECT NULLIF(substring(v,p),'b') AS piece FROM t ORDER BY id`, []string{"piece"}, []string{"TEXT"}, [][]any{{nil}, {nil}, {nil}}},
				{`SELECT greatest(substring(v,p),'a') AS piece FROM t ORDER BY id`, []string{"piece"}, []string{"TEXT"}, [][]any{{"b"}, {"a"}, {"a"}}},
				{`SELECT min(substring(v,p)) AS piece FROM t`, []string{"piece"}, []string{"TEXT"}, [][]any{{"b"}}},
			} {
				s := fixture.Select(c.q, c.names, c.types, c.rows)
				if len(c.types) == 1 && (c.types[0] == "BOOL" || c.types[0] == "TEXT") {
					s.CheckMetadata = similarReferenceMetadata
					if c.q == `SELECT min(substring(v,p)) AS piece FROM t` {
						s.CheckMetadata = similarReferenceDeclaredMetadata(b.PG)
					}
				}
				similarReferenceRoutes(t, p, b, s)
			}
			run(fixture.Exec(`CREATE TABLE copied(piece TEXT)`, 0))
			run(fixture.Exec(`INSERT INTO copied SELECT substring(v,p) FROM t WHERE v ~ p`, 1))
			similarReferenceRoutes(t, p, b, fixture.Select(`SELECT piece FROM copied`, []string{"piece"}, []string{"TEXT"}, [][]any{{"b"}}))
			for _, prepared := range []bool{false, true} {
				s := posixError(`UPDATE t SET sentinel=99 WHERE v ~ CASE WHEN id=2 THEN '[' ELSE p END`, "brackets [] not balanced")
				s.SelectRows = false
				s.Prepared = prepared
				run(s)
				run(fixture.Select(`SELECT sentinel FROM t ORDER BY id`, []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}, {int64(7)}}))
			}
			run(fixture.Exec(`UPDATE t SET v=substring(v,p),sentinel=8 WHERE v ~ p`, 1))
			run(fixture.Exec(`DELETE FROM t WHERE v !~* p`, 0))
		})
	}
}

func TestReferencePosixStoredDefinitions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, s := range []fixture.Step{
				fixture.Exec(`CREATE DOMAIN posix_domain AS TEXT`, 0),
				fixture.Exec(`CREATE TABLE t(id INT,v posix_domain,p TEXT DEFAULT '(b)',sentinel INT DEFAULT 7,piece TEXT GENERATED ALWAYS AS (substring(v,p)) STORED,m BOOLEAN DEFAULT ('AbC' ~* 'abc'),d TEXT DEFAULT substring('foobar','o(.)b'),CONSTRAINT valid CHECK(v ~ '^a'))`, 0),
				fixture.Exec(`INSERT INTO t(id,v) VALUES(1,'abc'),(2,NULL)`, 2),
				fixture.Exec(`CREATE INDEX posix_expression ON t ((substring(v,p))) WHERE v !~ 'z'`, 0),
				fixture.Exec(`CREATE VIEW matched AS SELECT id,v ~ p AS s,v ~* p AS i,v !~ p AS n,v !~* p AS ni,substring(v FROM p) AS piece FROM t`, 0),
				fixture.Exec(`CREATE MATERIALIZED VIEW posix_mv AS SELECT substring(v,p) AS piece FROM t`, 2),
			} {
				run(s)
			}
			want := fixture.Select(`SELECT s,i,n,ni,piece FROM matched ORDER BY id`, []string{"s", "i", "n", "ni", "piece"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "TEXT"}, [][]any{{true, true, false, false, "b"}, {nil, nil, nil, nil, nil}})
			want.CheckMetadata = similarReferenceDeclaredMetadata(b.PG)
			similarReferenceRoutes(t, p, b, want)
			run(fixture.Select(`SELECT d FROM t ORDER BY id`, []string{"d"}, []string{"TEXT"}, [][]any{{"o"}, {"o"}}))
			stmt, err := b.DB.PrepareContext(p.Ctx, want.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{b.DB, stmt, want.Query}
			run(fixture.Exec(`ALTER TABLE t RENAME COLUMN v TO value`, 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
			for _, prepared := range []bool{false, true} {
				s := posixError(`INSERT INTO t(id,value,p) VALUES(3,'abc','b'),(4,'abc','[')`, "brackets [] not balanced")
				s.SelectRows = false
				s.Prepared = prepared
				run(s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
			}
		})
	}
}

func TestReferencePosixFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`CREATE TABLE smoke(v TEXT)`, 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`INSERT INTO smoke VALUES('AbC'),(NULL)`, 2))
			similarReferenceRoutes(t, p, b, posixSelect(`SELECT v ~ 'abc' AS s,v ~* 'abc' AS i,v !~ 'abc' AS n,v !~* 'abc' AS ni FROM smoke ORDER BY v`, []string{"s", "i", "n", "ni"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, true, false}, {nil, nil, nil, nil}}))
			similarReferenceRoutes(t, p, b, posixSelect(`SELECT substring('abc01234xyz','(.*)(\d+)(.*)') AS greedy,substring('abc01234xyz','(.*?)(\d+)(.*)') AS lazy,substring('foo','foo(bar)?') AS absent,substring('bc','(a*)*') AS empty`, []string{"greedy", "lazy", "absent", "empty"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"abc0123", "abc", nil, ""}}))
			posixBool(t, p, b, "aab", `(?<=a+)b`, true)
		})
	}
}

func TestReferencePosixReturning(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`CREATE TABLE returned(v TEXT,p TEXT)`, 0))
			for _, prepared := range []bool{false, true} {
				inserted := fixture.Exec(`INSERT INTO returned VALUES(substring('foobar','o(.)b'),'o')`, 1)
				inserted.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, inserted)
				deleted := posixSelect(`DELETE FROM returned WHERE v ~ p RETURNING old.v ~ p AS sensitive,old.v ~* p AS insensitive,old.v !~ p AS negated,old.v !~* p AS insensitive_negated,substring(old.v,p) AS piece,substring(new.v,'[') AS absent`, []string{"sensitive", "insensitive", "negated", "insensitive_negated", "piece", "absent"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT"}, [][]any{{true, true, false, false, "o", nil}})
				deleted.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, deleted)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(`SELECT v FROM returned`, []string{"v"}, []string{"TEXT"}, nil))
			}
		})
	}
}
