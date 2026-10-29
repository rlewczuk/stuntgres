package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTcnPayloads(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				sender, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = sender.Close() })
				receiver := sessionMessageConn(t, p, b)
				sessionMessageSQL(t, p, b, sender, "CREATE EXTENSION tcn")
				catalogStep := fixture.Select(`SELECT p.proname,p.proargtypes::text AS args,p.prorettype::bigint AS result,p.provolatile,p.proisstrict,p.proparallel,l.lanname,p.prosrc,p.probin FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.oid='triggered_change_notification()'::regprocedure`, []string{"proname", "args", "result", "provolatile", "proisstrict", "proparallel", "lanname", "prosrc", "probin"}, []string{"NAME", "TEXT", "INT8", "CHAR", "BOOL", "CHAR", "NAME", "TEXT", "TEXT"}, [][]any{{"triggered_change_notification", "", int64(2279), "v", false, "u", "c", "triggered_change_notification", "$libdir/tcn"}})
				if prepared {
					catalogStep = fixture.Prepared(catalogStep)
				}
				fixture.RunStep(t, p.Ctx, sender, b.Recorder, catalogStep)
				sessionMessageSQL(t, p, b, sender, `CREATE TABLE "t""cn" ("k""b" text, n integer, note text, PRIMARY KEY(n,"k""b"))`)
				sessionMessageSQL(t, p, b, sender, `CREATE TRIGGER changes AFTER INSERT OR UPDATE OR DELETE ON "t""cn" FOR EACH ROW EXECUTE FUNCTION triggered_change_notification()`)
				sessionMessageSQL(t, p, b, receiver, "LISTEN tcn")
				for _, returning := range []bool{false, true} {
					queries := []string{
						`INSERT INTO "t""cn" VALUES ('O''Brien,west',7,NULL)`,
						`UPDATE "t""cn" SET n=8 WHERE n=7`,
						`DELETE FROM "t""cn" WHERE n=8`,
					}
					payloads := []string{`"t""cn",I,"n"='7',"k""b"='O''Brien,west'`, `"t""cn",U,"n"='7',"k""b"='O''Brien,west'`, `"t""cn",D,"n"='8',"k""b"='O''Brien,west'`}
					for i, q := range queries {
						step := fixture.Exec(q, 1)
						if returning {
							q += ` RETURNING n,"k""b",note`
							n := int64(8)
							if i == 0 {
								n = 7
							}
							step = fixture.Select(q, []string{"n", `k"b`, "note"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{n, "O'Brien,west", nil}})
						}
						if prepared {
							stmt, err := sender.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: sender, Statement: stmt, SQL: q}, b.Recorder, step)
							if err := stmt.Close(); err != nil {
								t.Fatal(err)
							}
						} else {
							fixture.RunStep(t, p.Ctx, sender, b.Recorder, step)
						}
						sessionMessageReceive(t, p, b, receiver, "tcn", payloads[i])
					}
				}
			})
		})
	}
	t.Run("type_output_and_include", func(t *testing.T) {
		sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			sender := sessionMessageConn(t, p, b)
			receiver := sessionMessageConn(t, p, b)
			sessionMessageSQL(t, p, b, sender, "CREATE EXTENSION tcn")
			sessionMessageSQL(t, p, b, receiver, "LISTEN tcn")
			sessionMessageSQL(t, p, b, sender, "CREATE TABLE tcn_output(note text,k text,n numeric(8,2))")
			sessionMessageSQL(t, p, b, sender, "CREATE UNIQUE INDEX tcn_output_pk ON tcn_output(n,k) INCLUDE(note)")
			sessionMessageSQL(t, p, b, sender, "ALTER TABLE tcn_output ADD CONSTRAINT tcn_output_pk PRIMARY KEY USING INDEX tcn_output_pk")
			sessionMessageSQL(t, p, b, sender, "CREATE TRIGGER changes AFTER INSERT ON tcn_output FOR EACH ROW EXECUTE FUNCTION triggered_change_notification()")
			for _, c := range []struct{ query, payload string }{
				{`INSERT INTO tcn_output VALUES (NULL,'',12.30)`, `"tcn_output",I,"n"='12.30',"k"=''`},
				{`INSERT INTO tcn_output VALUES ('ignored','é\,=O''B',-0.50)`, `"tcn_output",I,"n"='-0.50',"k"='é\,=O''B'`},
			} {
				fixture.RunStep(t, p.Ctx, sender, b.Recorder, fixture.Exec(c.query, 1))
				sessionMessageReceive(t, p, b, receiver, "tcn", c.payload)
			}
		})
	})
}
