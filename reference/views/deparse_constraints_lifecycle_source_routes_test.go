package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceDeparseConstraintsReparseSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) fixture.Observed {
				s.NonContext = true
				if s.SelectRows && s.Diagnostic == nil {
					previous := s.CheckMetadata
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						fixture.DeparseReviewMetadata(t, b.PG, rows)
						if previous != nil {
							previous(t, rows)
						}
					}
				}
				return fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
			run(fixture.Exec("CREATE TABLE original(a INT,CONSTRAINT ck CHECK(a::bigint>0))", 0))
			for _, mode := range []struct {
				pretty bool
				want   string
			}{{false, "CHECK (((a)::bigint > 0))"}, {true, "CHECK (a::bigint > 0)"}} {
				run(fixture.Prepared(fixture.Select("SELECT pg_get_constraintdef(oid,$1) AS d FROM pg_catalog.pg_constraint WHERE conname='ck'", []string{"d"}, []string{"TEXT"}, [][]any{{mode.want}}, mode.pretty)))
				// Consume the actual scalar return, after independently checking its golden.
				var suffix string
				if err := b.DB.QueryRow("SELECT pg_get_constraintdef(oid,$1) AS d FROM pg_catalog.pg_constraint WHERE conname='ck'", mode.pretty).Scan(&suffix); err != nil || suffix != mode.want {
					t.Fatal(suffix, mode.want, err)
				}
				name := fmt.Sprintf("copy_%t", mode.pretty)
				run(fixture.Exec("CREATE TABLE "+name+"(a INT,CONSTRAINT copied "+suffix+")", 0))
				for _, prepared := range []bool{false, true} {
					good := fixture.Exec("INSERT INTO "+name+" VALUES($1)", 1, 1)
					good.Prepared = prepared
					run(good)
					null := fixture.Exec("INSERT INTO "+name+" VALUES($1)", 1, nil)
					null.Prepared = prepared
					run(null)
					for _, value := range []int{0, -1} {
						bad := fixture.ImplicitError("INSERT INTO "+name+" VALUES($1)", fixture.CheckViolation(name, "copied", fmt.Sprint(value)), value)
						bad.Prepared = prepared
						run(bad)
					}
				}
				run(fixture.ImplicitError("INSERT INTO "+name+" VALUES(1),(0),(1)", fixture.CheckViolation(name, "copied", "0")))
				run(fixture.ImplicitError("UPDATE "+name+" SET a=-1", fixture.CheckViolation(name, "copied", "-1")))
				run(fixture.Select("SELECT a FROM "+name+" ORDER BY a NULLS FIRST", []string{"a"}, []string{"INT4"}, [][]any{{nil}, {nil}, {int64(1)}, {int64(1)}}))
			}
		})
	}
}

func TestReferenceDeparseConstraintsStableAttributesSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			db := b.DB
			postgres := b.PG
			run := func(s fixture.Step) fixture.Observed {
				s.NonContext = true
				if s.SelectRows && s.Diagnostic == nil {
					previous := s.CheckMetadata
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						fixture.DeparseReviewMetadata(t, b.PG, rows)
						if previous != nil {
							previous(t, rows)
						}
					}
				}
				return fixture.RunStep(t, p.Ctx, db, b.Recorder, s)
			}
			run(fixture.Exec("CREATE TABLE original(prefix TEXT,a INT,b INT DEFAULT 7,CONSTRAINT c CHECK(b>0))", 0))
			cid := fixture.DeparseSourceDefinitionID(t, db, "SELECT oid FROM pg_catalog.pg_constraint WHERE conname='c'")
			did := fixture.DeparseSourceDefinitionID(t, db, "SELECT oid FROM pg_catalog.pg_attrdef")
			var saved *sql.Rows
			if !postgres {
				var err error
				saved, err = db.Query("SELECT pg_get_constraintdef(oid,true) FROM pg_catalog.pg_constraint WHERE conname='c'")
				if err != nil {
					t.Fatal(err)
				}
				defer saved.Close()
			} else {
				run(fixture.Select("SELECT pg_get_constraintdef(oid,true) FROM pg_catalog.pg_constraint WHERE conname='c'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"CHECK (b > 0)"}}))
			}
			run(fixture.Exec("ALTER TABLE original DROP prefix", 0))
			run(fixture.Select("SELECT adnum FROM pg_catalog.pg_attrdef", []string{"adnum"}, []string{"INT2"}, [][]any{{int64(3)}}))
			if fixture.DeparseSourceDefinitionID(t, db, "SELECT oid FROM pg_catalog.pg_attrdef") != did || fixture.DeparseSourceDefinitionID(t, db, "SELECT oid FROM pg_catalog.pg_constraint WHERE conname='c'") != cid {
				t.Fatal("remap changed definition identity")
			}
			run(fixture.Exec("CREATE TABLE supplied(x TEXT,y TEXT,z TEXT)", 0))
			run(fixture.Select("SELECT pg_get_expr(conbin,'supplied'::regclass) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", []string{"d"}, []string{"TEXT"}, [][]any{{"(z > 0)"}}))
			run(fixture.Exec("CREATE TABLE wrongshape(q TEXT)", 0))
			failure := fixture.ImplicitError("SELECT pg_get_expr(conbin,'wrongshape'::regclass) FROM pg_catalog.pg_constraint WHERE conname='c'", fixture.Diagnostic{Code: "XX000", Message: `invalid attnum 3 for relation "wrongshape"`})
			failure.SelectRows = true
			run(failure)
			run(fixture.Exec("ALTER TABLE original RENAME b TO renamed", 0))
			run(fixture.Select("SELECT pg_get_constraintdef($1,true) AS d", []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (renamed > 0)"}}, cid))
			run(fixture.Exec("DROP TABLE original", 0))
			run(fixture.Select("SELECT pg_get_constraintdef($1) AS d", []string{"d"}, []string{"TEXT"}, [][]any{{nil}}, cid))
			if saved != nil {
				if !saved.Next() {
					t.Fatal(saved.Err())
				}
				var old string
				if err := saved.Scan(&old); err != nil || old != "CHECK (b > 0)" {
					t.Fatal(old, err)
				}
				if saved.Next() || saved.Err() != nil {
					t.Fatal(saved.Err())
				}
				if err := saved.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
