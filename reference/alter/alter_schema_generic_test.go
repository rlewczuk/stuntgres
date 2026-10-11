package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterSchemaGenericAndTemporaryBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "s1", "s2")
	for _, backend := range p.Backends() {
		postgres := backend.PG
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			c, err := db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			r := backend.Recorder
			referenceAlterSchemaSetup(t, p, c, r)
			before := ""
			if !postgres {
				before = referenceDDLNativeDump(t, p)
			}
			for _, object := range []string{"free", "v"} {
				source := "s1.free"
				if object == "v" {
					source = "public.v"
				}
				q := "ALTER TABLE " + source + " SET SCHEMA s2"
				if postgres || object == "v" {
					fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(fixture.Exec(q, 0)))
				} else {
					s := fixture.Prepared(fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "ALTER TABLE SET SCHEMA supports ordinary tables only"}))
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, c, r, s)
					if referenceDDLNativeDump(t, p) != before {
						t.Fatal("unsupported sequence move changed native closure")
					}
				}
			}
			view, sequence := "s2.v", "s1.free"
			if postgres {
				sequence = "s2.free"
			}
			fixture.RunStep(t, p.Ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM "+view+" ORDER BY id"))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT nextval('"+sequence+"')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1000)}}))
			message := "temporary schema creation is unsupported"
			if postgres {
				message = "cannot move objects into or out of temporary schemas"
			}
			if !postgres {
				before = referenceDDLNativeDump(t, p)
			}
			s := fixture.Prepared(fixture.ImplicitError("ALTER TABLE s1.t SET SCHEMA pg_temp", fixture.Diagnostic{Code: "0A000", Message: message}))
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, r, s)
			if !postgres && referenceDDLNativeDump(t, p) != before {
				t.Fatal("temporary destination rejection changed native closure")
			}
			if !postgres {
				fixture.RunStep(t, p.Ctx, c, r, fixture.RoleReferenceError("ALTER TABLE s1.t SET SCHEMA pg_catalog", "0A000", "system schema creation is unsupported", "", true, "execute"))
				if referenceDDLNativeDump(t, p) != before {
					t.Fatal("system destination rejection changed native closure")
				}
			}
			fixture.RunStep(t, p.Ctx, c, r, referenceAlterSchemaRows("SELECT id,note FROM s1.t ORDER BY id"))
			if postgres {
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE INDEX ix ON s1.t(id)", 0))
				s := fixture.Prepared(fixture.ImplicitError("ALTER TABLE s1.ix SET SCHEMA s2", fixture.Diagnostic{Code: "42809", Message: `cannot change schema of index "ix"`, Hint: "Change the schema of the table instead."}))
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, r, s)
			}
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE SEQUENCE s1.q", 0))
			if postgres {
				fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(fixture.Exec("ALTER TABLE s1.q SET SCHEMA s2", 0)))
			} else {
				before = referenceDDLNativeDump(t, p)
				fixture.RunStep(t, p.Ctx, c, r, fixture.RoleReferenceError("ALTER TABLE s1.q SET SCHEMA s2", "0A000", "ALTER TABLE SET SCHEMA supports ordinary tables only", "", true, "execute"))
				if referenceDDLNativeDump(t, p) != before {
					t.Fatal("original sequence boundary changed native closure")
				}
			}
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("DROP VIEW s2.v", 0))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE VIEW s1.v AS SELECT id FROM s1.t WHERE id=7", 0))
			var oldOID, oldNamespace, sourceNamespace int64
			if err = c.QueryRowContext(p.Ctx, "SELECT oid,relnamespace FROM pg_class WHERE relname='v'").Scan(&oldOID, &oldNamespace); err != nil || oldOID <= 0 || oldNamespace <= 0 {
				t.Fatal(oldOID, oldNamespace, err)
			}
			fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(fixture.Exec("ALTER TABLE s1.v SET SCHEMA s2", 0)))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT id FROM s2.v", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			var movedOID, newNamespace int64
			if err = c.QueryRowContext(p.Ctx, "SELECT oid,relnamespace FROM pg_class WHERE relname='v'").Scan(&movedOID, &newNamespace); err != nil {
				t.Fatal(err)
			}
			if err = c.QueryRowContext(p.Ctx, "SELECT relnamespace FROM pg_class WHERE relname='t'").Scan(&sourceNamespace); err != nil {
				t.Fatal(err)
			}
			if movedOID != oldOID || newNamespace == oldNamespace || sourceNamespace != oldNamespace {
				t.Fatal("view identity or source namespace changed", oldOID, movedOID, oldNamespace, newNamespace, sourceNamespace)
			}
		})
	}
}
