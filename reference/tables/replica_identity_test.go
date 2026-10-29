package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func replicaSetup(run func(fixture.Step)) {
	for _, q := range []string{
		"CREATE TABLE ri_t(k INT NOT NULL,b INT NOT NULL,p TEXT, CONSTRAINT ri_pk PRIMARY KEY(k), CONSTRAINT ri_u UNIQUE(b))",
		"CREATE UNIQUE INDEX ri_k ON ri_t(k)",
		"CREATE UNIQUE INDEX ri_bk ON ri_t(b DESC NULLS FIRST,k) INCLUDE(p)",
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO ri_t VALUES(1,2,'one'),(3,4,NULL)", 2))
}

func replicaMode(table, mode string) fixture.Step {
	return fixture.Select("SELECT relreplident FROM pg_class WHERE oid='"+table+"'::regclass", []string{"relreplident"}, []string{"CHAR"}, [][]any{{mode}})
}

func replicaFlags(selected string) fixture.Step {
	rows := make([][]any, 0, 4)
	for _, name := range []string{"ri_bk", "ri_k", "ri_pk", "ri_u"} {
		rows = append(rows, []any{name, name == selected, true})
	}
	return fixture.Select("SELECT c.relname,i.indisreplident,i.indrelid='ri_t'::regclass AS parent FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid='ri_t'::regclass ORDER BY c.relname", []string{"relname", "indisreplident", "parent"}, []string{"NAME", "BOOL", "BOOL"}, rows)
}

func replicaData() fixture.Step {
	return fixture.Select("SELECT k,b,p FROM ri_t ORDER BY k", []string{"k", "b", "p"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(2), "one"}, {int64(3), int64(4), nil}})
}

func replicaMissing(q string) fixture.Step {
	return fixture.ImplicitError(q, fixture.Diagnostic{Code: "42704", Message: `index "ri_missing" for table "ri_t" does not exist`})
}

func replicaModes(t *testing.T, smoke bool) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					replicaSetup(run)
					run(replicaMode("ri_t", "d"))
					run(replicaFlags(""))
					catalogQuery := replicaFlags("").Query
					catalogStatement, err := b.DB.PrepareContext(p.Ctx, catalogQuery)
					if err != nil {
						t.Fatal(err)
					}
					defer catalogStatement.Close()
					dataStatement, err := b.DB.PrepareContext(p.Ctx, replicaData().Query)
					if err != nil {
						t.Fatal(err)
					}
					defer dataStatement.Close()
					for _, tc := range []struct{ option, mode, index string }{
						{"USING INDEX ri_pk", "i", "ri_pk"}, {"USING INDEX ri_k", "i", "ri_k"}, {"USING INDEX ri_bk", "i", "ri_bk"}, {"USING INDEX ri_bk", "i", "ri_bk"}, {"FULL", "f", ""}, {"NOTHING", "n", ""}, {"DEFAULT", "d", ""},
					} {
						step := fixture.Exec("ALTER TABLE ri_t REPLICA IDENTITY "+tc.option, 0)
						if prepared {
							step = fixture.Prepared(step)
						} else {
							step.NonContext = true
						}
						run(step)
						run(replicaMode("ri_t", tc.mode))
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: catalogStatement, SQL: catalogQuery}, b.Recorder, replicaFlags(tc.index))
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: dataStatement, SQL: replicaData().Query}, b.Recorder, replicaData())
					}
					if smoke {
						run(fixture.Exec("ALTER TABLE ri_t REPLICA IDENTITY USING INDEX ri_k", 0))
						run(replicaMissing("ALTER TABLE ri_t REPLICA IDENTITY FULL, REPLICA IDENTITY USING INDEX ri_missing"))
						run(replicaMode("ri_t", "i"))
						run(replicaFlags("ri_k"))
						run(replicaData())
					}
					run(fixture.Exec("UPDATE ri_t SET p='changed' WHERE k=1", 1))
					run(fixture.Exec("DELETE FROM ri_t WHERE k=3", 1))
					run(fixture.ErrorDiagnostic("INSERT INTO ri_t VALUES(1,8,'duplicate')", fixture.UniqueReferenceDuplicate("ri_t", "ri_pk", "Key (k)=(1) already exists.")))
				})
			}
		})
	}
}

func TestReferenceReplicaIdentityModes(t *testing.T)        { replicaModes(t, false) }
func TestReferenceReplicaIdentityFeatureSmoke(t *testing.T) { replicaModes(t, true) }

func TestReferenceReplicaIdentityMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	replicaSetup(func(s fixture.Step) { p.Step(t, s) })
	for _, backend := range p.Backends() {
		// Native OIDs are int64; lib/pq OID bytes normalize to strings.
		// Keep the actual OID projection and its raw metadata assertions.
		var boolOID, charOID any = int64(16), int64(18)
		if backend.PG {
			boolOID, charOID = "16", "18"
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT attname,attnum,atttypid,attnotnull FROM pg_attribute WHERE attrelid='pg_index'::regclass AND attname='indisreplident'", []string{"attname", "attnum", "atttypid", "attnotnull"}, []string{"NAME", "INT2", "OID", "BOOL"}, [][]any{{"indisreplident", int64(15), boolOID, true}}))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT atttypid FROM pg_attribute WHERE attrelid='pg_class'::regclass AND attname='relreplident'", []string{"atttypid"}, []string{"OID"}, [][]any{{charOID}}))
	}
	p.Step(t, fixture.Exec("ALTER TABLE ri_t REPLICA IDENTITY USING INDEX ri_bk", 0))
	p.Step(t, replicaFlags("ri_bk"))
	p.Step(t, replicaData())
}

func TestReferenceReplicaIdentityAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			run := func(s fixture.Step) { p.Step(t, s) }
			replicaSetup(run)
			run(fixture.Exec("ALTER TABLE ri_t REPLICA IDENTITY USING INDEX ri_k", 0))
			for _, q := range []string{
				"ALTER TABLE ri_t REPLICA IDENTITY FULL, REPLICA IDENTITY USING INDEX ri_missing",
				"ALTER TABLE ri_t ADD COLUMN extra INT, REPLICA IDENTITY USING INDEX ri_missing",
				"ALTER TABLE ri_t REPLICA IDENTITY USING INDEX ri_bk, REPLICA IDENTITY USING INDEX ri_missing",
			} {
				s := replicaMissing(q)
				if prepared {
					s = fixture.Prepared(s)
					s.ErrorStage = "execute"
				}
				run(s)
				run(replicaMode("ri_t", "i"))
				run(replicaFlags("ri_k"))
				run(replicaData())
			}
			run(fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid='ri_t'::regclass AND attname='extra'", []string{"attname"}, []string{"NAME"}, nil))
			run(fixture.Exec("ALTER TABLE ri_t ADD COLUMN extra INT, REPLICA IDENTITY FULL", 0))
			run(replicaMode("ri_t", "f"))
			run(replicaFlags(""))
			run(fixture.Exec("ALTER TABLE ri_t ADD CONSTRAINT ri_check CHECK(k<0) NOT VALID", 0))
			run(fixture.ImplicitError("ALTER TABLE ri_t REPLICA IDENTITY NOTHING, VALIDATE CONSTRAINT ri_check", fixture.Diagnostic{Code: "23514", Message: `check constraint "ri_check" of relation "ri_t" is violated by some row`, Schema: "public", Table: "ri_t", Constraint: "ri_check"}))
			run(replicaMode("ri_t", "f"))
			run(replicaFlags(""))
			run(replicaData())
		})
	}
}
