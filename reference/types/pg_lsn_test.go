package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func pgLSNScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func pgLSNMetadata(t *testing.T, rows *sql.Rows, pg, declared bool, name string) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != 1 {
		t.Fatal(columns, err)
	}
	c := columns[0]
	scan := reflect.TypeFor[string]()
	if pg {
		scan = reflect.TypeFor[any]()
	}
	if c.Name() != name || c.DatabaseTypeName() != "PG_LSN" || c.ScanType() != scan {
		t.Fatalf("pg_lsn metadata: %s/%s/%v", c.Name(), c.DatabaseTypeName(), c.ScanType())
	}
	if _, ok := c.Length(); ok {
		t.Fatal("unexpected pg_lsn length")
	}
	if _, _, ok := c.DecimalSize(); ok {
		t.Fatal("unexpected pg_lsn precision")
	}
	n, known := c.Nullable()
	if pg || !declared {
		if n || known {
			t.Fatal("unexpected nullability", n, known)
		}
	} else if !n || !known {
		t.Fatal("missing declared nullability", n, known)
	}
}

func TestReferencePGLSNValues(t *testing.T) {
	pgLSNScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		vectors := [][2]string{{"0/0", "0/0"}, {"00000000/00000001", "0/1"}, {"a/b", "A/B"}, {"16/b374d848", "16/B374D848"}, {"0/FFFFFFFF", "0/FFFFFFFF"}, {"1/0", "1/0"}, {"7FFFFFFF/FFFFFFFF", "7FFFFFFF/FFFFFFFF"}, {"80000000/0", "80000000/0"}, {"ffffffff/ffffffff", "FFFFFFFF/FFFFFFFF"}}
		run(fixture.Exec("CREATE TABLE pglsn_values(id integer,v pg_catalog.pg_lsn)", 0))
		for i, v := range vectors {
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{"SELECT " + tidQuote(v[0]) + "::pg_lsn AS v", "SELECT pg_lsn " + tidQuote(v[0]) + " AS v"} {
					s := fixture.Select(q, []string{"v"}, []string{"PG_LSN"}, [][]any{{v[1]}})
					s.Prepared = prepared
					run(s)
				}
				s := fixture.Select("SELECT $1::pg_lsn::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{v[1]}}, v[0])
				s.Prepared = prepared
				run(s)
			}
			run(fixture.Exec("INSERT INTO pglsn_values VALUES($1,$2)", 1, i, v[0]))
		}
		run(fixture.Exec("INSERT INTO pglsn_values VALUES(10,NULL),(11,'0/0')", 2))
		var want [][]any
		for _, v := range vectors {
			want = append(want, []any{v[1]})
		}
		want = append(want, []any{nil}, []any{"0/0"})
		run(fixture.Select("SELECT v AS position FROM pglsn_values ORDER BY id", []string{"position"}, []string{"PG_LSN"}, want))
	})
}

func TestReferencePGLSNMetadata(t *testing.T) {
	pgLSNScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Select("SELECT c.oid > 0 AS valid_oid,c.opcfamily::text,c.opcintype::text,c.opckeytype::text,c.opcdefault FROM pg_catalog.pg_opclass c JOIN pg_catalog.pg_am a ON a.oid=c.opcmethod WHERE a.amname='btree' AND c.opcname='pg_lsn_ops' AND c.opcnamespace='pg_catalog'::regnamespace", []string{"valid_oid", "opcfamily", "opcintype", "opckeytype", "opcdefault"}, []string{"BOOL", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{true, "3253", "3220", "0", true}}))
		run(fixture.Select("SELECT p.amprocnum,p.amproclefttype::text,p.amprocrighttype::text,p.amproc::oid::text FROM pg_catalog.pg_amproc p JOIN pg_catalog.pg_opclass c ON c.opcfamily=p.amprocfamily JOIN pg_catalog.pg_am a ON a.oid=c.opcmethod WHERE a.amname='btree' AND c.opcname='pg_lsn_ops' ORDER BY p.amprocnum", []string{"amprocnum", "amproclefttype", "amprocrighttype", "amproc"}, []string{"INT2", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "3220", "3220", "3251"}, {int64(4), "3220", "3220", "5051"}}))
		run(fixture.Exec("CREATE TABLE pglsn_metadata(id integer,v pg_lsn)", 0))
		run(fixture.Exec("INSERT INTO pglsn_metadata VALUES(1,'80000000/0'),(2,NULL)", 2))
		for _, prepared := range []bool{false, true} {
			for _, empty := range []bool{false, true} {
				q := "SELECT v AS position FROM pglsn_metadata"
				want := [][]any{{"80000000/0"}, {nil}}
				if empty {
					q += " WHERE FALSE"
					want = nil
				}
				q += " ORDER BY id"
				s := fixture.Select(q, []string{"position"}, []string{"PG_LSN"}, want)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					pgLSNMetadata(t, rows, b.PG, true, "position")
				}
				s.CheckScan = func(t *testing.T, rows *sql.Rows, i int) {
					var raw any
					if err := rows.Scan(&raw); err != nil {
						t.Fatal(err)
					}
					if want[i][0] == nil {
						if raw != nil {
							t.Fatal(raw)
						}
					} else if b.PG {
						if v, ok := raw.([]byte); !ok || string(v) != want[i][0] {
							t.Fatalf("raw pg_lsn: %T %v", raw, raw)
						}
					} else if v, ok := raw.(string); !ok || v != want[i][0] {
						t.Fatalf("raw pg_lsn: %T %v", raw, raw)
					}
				}
				run(s)
				q = "SELECT '0/0'::pg_lsn + 1::numeric AS position,'0/1'::pg_lsn - '0/0'::pg_lsn AS difference"
				computed := [][]any{{"0/1", "1"}}
				if empty {
					q += " WHERE FALSE"
					computed = nil
				}
				s = fixture.Select(q, []string{"position", "difference"}, []string{"PG_LSN", "NUMERIC"}, computed)
				s.Prepared = prepared
				run(s)
			}
		}
		run(fixture.Select("SELECT oid::text,typarray::text,typlen,typbyval,typalign::text,typtype::text,typcategory::text,typispreferred,typcollation::text,typtypmod,typinput::oid::text,typoutput::oid::text,typreceive::oid::text,typsend::oid::text,typmodin::oid::text,typmodout::oid::text,typanalyze::oid::text,typsubscript::oid::text FROM pg_catalog.pg_type WHERE oid=3220", []string{"oid", "typarray", "typlen", "typbyval", "typalign", "typtype", "typcategory", "typispreferred", "typcollation", "typtypmod", "typinput", "typoutput", "typreceive", "typsend", "typmodin", "typmodout", "typanalyze", "typsubscript"}, []string{"TEXT", "TEXT", "INT2", "BOOL", "TEXT", "TEXT", "TEXT", "BOOL", "TEXT", "INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"3220", "3221", int64(8), true, "d", "b", "U", false, "0", int64(-1), "3229", "3230", "3238", "3239", "0", "0", "0", "0"}}))
		run(fixture.Select("SELECT atttypid::text,atttypmod,attlen,attbyval,attalign::text,attcollation::text FROM pg_catalog.pg_attribute WHERE attrelid='pglsn_metadata'::regclass AND attname='v'", []string{"atttypid", "atttypmod", "attlen", "attbyval", "attalign", "attcollation"}, []string{"TEXT", "INT4", "INT2", "BOOL", "TEXT", "TEXT"}, [][]any{{"3220", int64(-1), int64(8), true, "d", "0"}}))
	})
}

func TestReferencePGLSNParameters(t *testing.T) {
	pgLSNScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		q := "SELECT $1::pg_lsn AS v"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		for _, frame := range []struct{ arg, want any }{{"0/0", "0/0"}, {[]byte("a/b"), "A/B"}, {nil, nil}, {[]byte(nil), nil}, {"80000000/0", "80000000/0"}, {"ffffffff/ffffffff", "FFFFFFFF/FFFFFFFF"}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"PG_LSN"}, [][]any{{frame.want}}, frame.arg))
		}
		d := pgLSNDiagnostic("bad")
		d.Context = "unnamed portal parameter $1 = '...'"
		malformed := fixture.ErrorDiagnostic(q, d, "bad")
		malformed.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, malformed)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"PG_LSN"}, [][]any{{"0/1"}}, "0/1"))
		source := []byte("0/1")
		rows, err := stmt.QueryContext(p.Ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		source[2] = '9'
		pgLSNMetadata(t, rows, b.PG, false, "v")
		if !rows.Next() {
			t.Fatal("missing retained value", rows.Err())
		}
		var value string
		if err := rows.Scan(&value); err != nil || value != "0/1" {
			t.Fatal(value, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		q = "SELECT '0/1'::pg_lsn - $1 AS d"
		stmt2, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt2.Close()
		held2 := fixture.HeldStatement{Executor: b.DB, Statement: stmt2, SQL: q}
		fixture.RunStep(t, p.Ctx, held2, b.Recorder, fixture.Select(q, []string{"d"}, []string{"NUMERIC"}, [][]any{{"1"}}, "0/0"))
	})
}
