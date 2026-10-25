package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func foreignKeyScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, func(fixture.Step))) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			body(t, p, b, func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) })
		})
	}
}

func foreignKeyIntRows(q, column string, values ...int64) fixture.Step {
	var rows [][]any
	for _, v := range values {
		rows = append(rows, []any{v})
	}
	return fixture.Select(q, []string{column}, []string{"INT4"}, rows)
}

func TestReferenceForeignKeyDefinition(t *testing.T) {
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		for _, q := range []string{
			"CREATE TABLE p(a INT,b TEXT,PRIMARY KEY(a,b))",
			"CREATE TABLE implicit(a INT,b TEXT,FOREIGN KEY(a,b) REFERENCES p)",
			"CREATE TABLE reversed(b TEXT,a INT,CONSTRAINT reversed_fk FOREIGN KEY(b,a) REFERENCES p(b,a))",
			"CREATE TABLE indexed(a INT,b TEXT,payload TEXT)",
			"CREATE UNIQUE INDEX indexed_key ON indexed(a,b) INCLUDE(payload)",
			"CREATE TABLE by_index(a INT,b TEXT,FOREIGN KEY(a,b) REFERENCES indexed(a,b))",
			"CREATE TABLE single(id INT UNIQUE)",
			"CREATE TABLE multiple(a INT REFERENCES single(id),b INT REFERENCES single(id))",
			"CREATE TABLE self(id INT PRIMARY KEY,pid INT REFERENCES self)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO p VALUES(1,'x')", 1))
		run(fixture.Exec("INSERT INTO implicit VALUES(1,'x')", 1))
		run(fixture.Exec("INSERT INTO reversed VALUES('x',1)", 1))
		run(fixture.Exec("INSERT INTO indexed VALUES(2,'y','payload')", 1))
		run(fixture.Exec("INSERT INTO by_index VALUES(2,'y')", 1))
		run(fixture.Exec("INSERT INTO single VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO multiple VALUES(1,1)", 1))
		for _, q := range []string{"INSERT INTO multiple VALUES(9,1)", "INSERT INTO multiple VALUES(1,9)"} {
			column := "a"
			if strings.Contains(q, "(1,9)") {
				column = "b"
			}
			run(fixture.ForeignKeyFail(q, fixture.ForeignKeyChild("multiple", "multiple_"+column+"_fkey", "Key ("+column+")=(9) is not present in table \"single\"."), false))
		}
		run(fixture.Select("SELECT c.conname,c.contype FROM pg_constraint c JOIN pg_class r ON c.conrelid=r.oid WHERE r.relname='reversed' ORDER BY c.conname", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, [][]any{{"reversed_fk", "f"}}))
		run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='reversed_fk'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"FOREIGN KEY (b, a) REFERENCES p(b, a)"}}))
		run(fixture.Select("SELECT indexname FROM pg_indexes WHERE tablename='reversed'", []string{"indexname"}, []string{"NAME"}, nil))
		run(fixture.Exec("INSERT INTO self VALUES(2,1),(1,NULL)", 2))
		run(fixture.Exec("DELETE FROM self", 2))
	})
}

func TestReferenceForeignKeyDiagnostics(t *testing.T) {
	cases := []struct{ setup, query, code, message, detail string }{
		{"CREATE TABLE p(id INT)", "CREATE TABLE c(pid INT REFERENCES p(id))", "42830", `there is no unique constraint matching given keys for referenced table "p"`, ""},
		{"CREATE TABLE p(id INT UNIQUE)", "CREATE TABLE c(pid INT REFERENCES p)", "42704", `there is no primary key for referenced table "p"`, ""},
		{"CREATE TABLE p(a INT,b INT,UNIQUE(a,b))", "CREATE TABLE c(pid INT REFERENCES p(a,b))", "42830", "number of referencing and referenced columns for foreign key disagree", ""},
		{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE c(a INT,b INT,FOREIGN KEY(a,b) REFERENCES p(id,id))", "42830", "foreign key referenced-columns list must not contain duplicates", ""},
		{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE c(pid INT REFERENCES p(missing))", "42703", `column "missing" referenced in foreign key constraint does not exist`, ""},
		{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE c(pid TEXT CONSTRAINT fk REFERENCES p(id))", "42804", `foreign key constraint "fk" cannot be implemented`, `Key columns "pid" of the referencing table and "id" of the referenced table are of incompatible types: text and integer.`},
		{"CREATE TABLE base(id INT);", "CREATE TABLE c(pid INT REFERENCES p(id))", "42809", `referenced relation "p" is not a table`, ""},
	}
	for i, tc := range cases {
		t.Run(tc.code+"/"+tc.query, func(t *testing.T) {
			foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec(tc.setup, 0))
				if i == len(cases)-1 {
					run(fixture.Exec("CREATE VIEW p AS SELECT id FROM base", 0))
				}
				for _, prepared := range []bool{false, true} {
					run(fixture.ForeignKeyFail(tc.query, fixture.ForeignKeyError(tc.code, tc.message, tc.detail, "", ""), prepared))
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname='c'", []string{"relname"}, []string{"NAME"}, nil))
				}
			})
		})
	}
}

func TestReferenceForeignKeyValues(t *testing.T) {
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(a INT,b TEXT,PRIMARY KEY(a,b))", 0))
		run(fixture.Exec("CREATE TABLE simple(a INT,b TEXT,FOREIGN KEY(a,b) REFERENCES p MATCH SIMPLE)", 0))
		run(fixture.Exec("CREATE TABLE fullkey(a INT,b TEXT,CONSTRAINT fk FOREIGN KEY(a,b) REFERENCES p MATCH FULL)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(1,'x'),(2,'y')", 2))
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("INSERT INTO simple VALUES($1,$2)", 1, 1, "x")
			s.Prepared = prepared
			run(s)
			s = fixture.ForeignKeyFail("INSERT INTO simple VALUES($1,$2)", fixture.ForeignKeyChild("simple", "simple_a_b_fkey", `Key (a, b)=(3, z) is not present in table "p".`), prepared)
			s.Args = []any{3, "z"}
			run(s)
		}
		run(fixture.Exec("INSERT INTO simple VALUES(NULL,'z'),(1,NULL)", 2))
		run(fixture.Exec("INSERT INTO fullkey VALUES(NULL,NULL),(1,'x')", 2))
		for _, prepared := range []bool{false, true} {
			run(fixture.ForeignKeyFail("INSERT INTO fullkey VALUES(NULL,'x')", fixture.ForeignKeyChild("fullkey", "fk", "MATCH FULL does not allow mixing of null and nonnull key values."), prepared))
			run(fixture.ForeignKeyFail("INSERT INTO fullkey VALUES(3,'z')", fixture.ForeignKeyChild("fullkey", "fk", `Key (a, b)=(3, z) is not present in table "p".`), prepared))
			run(fixture.ForeignKeyFail("INSERT INTO fullkey VALUES(2,'y'),(NULL,'x')", fixture.ForeignKeyChild("fullkey", "fk", "MATCH FULL does not allow mixing of null and nonnull key values."), prepared))
		}
		s := fixture.Select("SELECT a,b FROM simple", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(1), "x"}, {nil, "z"}, {int64(1), nil}})
		s.Bag = true
		run(s)
		s = fixture.Select("SELECT a,b FROM fullkey", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{nil, nil}, {int64(1), "x"}})
		s.Bag = true
		run(s)
	})
}

func TestReferenceForeignKeyMetadata(t *testing.T) {
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE c(pid INT REFERENCES p,note TEXT)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
		run(fixture.Prepared(fixture.Exec("INSERT INTO c VALUES($1,$2)", 1, 1, "x")))
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT pid,note FROM c", []string{"pid", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}})
			s.Prepared = prepared
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				cols, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				for i, col := range cols {
					nullable, ok := col.Nullable()
					if b.PG {
						if ok {
							t.Fatal("pq nullability unexpectedly known")
						}
					} else if !ok || !nullable {
						t.Fatal("child keys must remain nullable", i, nullable, ok)
					}
				}
				want := reflect.TypeOf(int64(0))
				if b.PG {
					want = reflect.TypeOf(int32(0))
				}
				if cols[0].ScanType() != want {
					t.Fatal(cols[0].ScanType(), want)
				}
			}
			run(s)
			s = foreignKeyIntRows("SELECT id FROM p", "id", 1)
			s.Prepared = prepared
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				cols, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				nullable, ok := cols[0].Nullable()
				if b.PG {
					if ok {
						t.Fatal("pq nullability unexpectedly known")
					}
				} else if !ok || nullable {
					t.Fatal("parent primary key lost NOT NULL", nullable, ok)
				}
			}
			run(s)
		}
	})
}

func TestReferenceForeignKeyBoundaries(t *testing.T) {
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		q := `CREATE TABLE "é"(pid INT REFERENCES p MATCH PARTIAL)`
		d := fixture.ForeignKeyError("0A000", "MATCH PARTIAL not yet implemented", "", "", "")
		d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "MATCH")]) + 1)
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(q, d)
			s.Prepared = prepared
			if prepared {
				s.ErrorStage = "prepare"
			}
			run(s)
		}
		for i, clause := range []string{"ON DELETE NO ACTION ON UPDATE NO ACTION NOT DEFERRABLE INITIALLY IMMEDIATE", "INITIALLY IMMEDIATE NOT DEFERRABLE"} {
			name := []string{"defaults1", "defaults2"}[i]
			run(fixture.Exec("CREATE TABLE "+name+"(pid INT REFERENCES p "+clause+")", 0))
		}
	})
}

func TestReferenceForeignKeyDefinitionNames(t *testing.T) {
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE "p""é"(id INT PRIMARY KEY)`, 0))
		run(fixture.Exec(`CREATE TABLE c(a INT,CONSTRAINT explicit FOREIGN KEY(a) REFERENCES "p""é",FOREIGN KEY(a) REFERENCES "p""é",FOREIGN KEY(a) REFERENCES "p""é")`, 0))
		run(fixture.Select("SELECT c.conname,c.contype FROM pg_constraint c JOIN pg_class r ON c.conrelid=r.oid WHERE r.relname='c' ORDER BY c.conname", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, [][]any{{"c_a_fkey", "f"}, {"c_a_fkey1", "f"}, {"explicit", "f"}}))
		run(fixture.Exec(`INSERT INTO "p""é" VALUES(1)`, 1))
		run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
		run(fixture.ForeignKeyFail("INSERT INTO c VALUES(9)", fixture.ForeignKeyChild("c", "explicit", `Key (a)=(9) is not present in table "p"é".`), false))
	})
}
