package reference_test

import (
	"bytes"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

type definitionFailedReader struct{ err error }

func (r definitionFailedReader) Read([]byte) (int, error) { return 0, r.err }

func TestReferenceTransactionalDefinitionControls(t *testing.T) {
	for _, replacement := range []string{"reset", "restore", "failed_restore_commit"} {
		t.Run(replacement, func(t *testing.T) {
			definitionScenario(t, []string{"tx_control_committed", "tx_control_private"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := definitionRun(t, p, b, c)
				for _, q := range []string{`BEGIN`, `CREATE ACCESS METHOD tx_control_committed TYPE INDEX HANDLER bthandler`, `CREATE OPERATOR FAMILY public.tx_control_family USING tx_control_committed`, `CREATE OPERATOR CLASS public.tx_control_class DEFAULT FOR TYPE integer USING tx_control_committed FAMILY public.tx_control_family AS ` + definitionClassItems, `COMMIT`, `BEGIN`, `CREATE ACCESS METHOD tx_control_private TYPE INDEX HANDLER bthandler`, `CREATE OPERATOR FAMILY public.tx_control_private_family USING tx_control_private`, `CREATE OPERATOR CLASS public.tx_control_private_class DEFAULT FOR TYPE integer USING tx_control_private FAMILY public.tx_control_private_family AS ` + definitionClassItems, `SAVEPOINT graph`} {
					run(fixture.Exec(q, 0))
				}
				definitionRun(t, p, b, b.DB)(definitionCount(`SELECT count(*) FROM pg_am WHERE amname='tx_control_private'`, 0))
				if b.PG {
					run(fixture.Exec(`ROLLBACK`, 0))
					run(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname='tx_control_private_class'`, 0))
					return
				}
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(dump.String(), "tx_control_private") {
					t.Fatal("Dump exposed private graph", dump.String())
				}
				for _, token := range []string{`CREATE ACCESS METHOD "tx_control_committed"`, `CREATE OPERATOR FAMILY "public"."tx_control_family"`, `CREATE OPERATOR CLASS "public"."tx_control_class"`} {
					if !strings.Contains(dump.String(), token) {
						t.Fatal("committed definition missing from actual dump", token, dump.String())
					}
				}
				connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/restored")
				if err != nil {
					t.Fatal(err)
				}
				clone := sql.OpenDB(connector)
				defer clone.Close()
				if err := stuntgres.Restore(p.Ctx, clone, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				recorded, rec := fixture.RecordedNativePool(t, connector)
				check := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, recorded, rec, s) }
				method := definitionID(t, p, recorded, `SELECT oid::bigint FROM pg_am WHERE amname='tx_control_committed'`)
				check(fixture.Select(`SELECT c.opcmethod::bigint,c.opcintype::bigint,c.opcdefault,c.opcfamily=f.oid FROM pg_opclass c JOIN pg_opfamily f ON f.opfname='tx_control_family' WHERE c.opcname='tx_control_class'`, []string{"opcmethod", "opcintype", "opcdefault", "?column?"}, []string{"INT8", "INT8", "BOOL", "BOOL"}, [][]any{{method, int64(23), true, true}}))
				check(fixture.Exec(`CREATE TABLE tx_control_rows(n integer)`, 0))
				check(fixture.Exec(`CREATE INDEX tx_control_index ON tx_control_rows USING tx_control_committed(n)`, 0))
				check(fixture.Exec(`INSERT INTO tx_control_rows VALUES(7),(NULL)`, 2))
				check(fixture.Select(`SELECT n FROM tx_control_rows ORDER BY n`, []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}, {nil}}))
				sentinel := errors.New("definition reader failure")
				if err := stuntgres.Restore(p.Ctx, p.Native, definitionFailedReader{sentinel}); !errors.Is(err, sentinel) {
					t.Fatal(err)
				}
				err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+`CREATE ACCESS METHOD late_bad TYPE TABLE HANDLER bthandler;`))
				want := fixture.ControlsReferenceDiagnostic("42809", `function bthandler must return type table_am_handler`)
				fixture.AssertDiagnostic(t, err, &want)
				err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+`CREATE OPERATOR FAMILY public.tx_control_duplicate USING tx_control_committed; CREATE OPERATOR CLASS public.tx_control_duplicate DEFAULT FOR TYPE integer USING tx_control_committed FAMILY public.tx_control_duplicate AS `+definitionClassItems+`;`))
				duplicate := fixture.ControlsReferenceDiagnostic("42710", `could not make operator class "tx_control_duplicate" be default for type pg_catalog.int4`)
				duplicate.Detail = `Operator class "tx_control_class" already is the default.`
				fixture.AssertDiagnostic(t, err, &duplicate)
				run(definitionCount(`SELECT count(*) FROM pg_opfamily WHERE opfname='tx_control_duplicate'`, 0))
				run(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname='tx_control_class'`, 1))
				run(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname='tx_control_private_class'`, 1))
				if replacement == "failed_restore_commit" {
					run(fixture.Exec(`COMMIT`, 0))
					definitionRun(t, p, b, b.DB)(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname='tx_control_private_class'`, 1))
					return
				}
				q := `ALTER OPERATOR FAMILY public.tx_control_private_family USING tx_control_private ADD FUNCTION 2 (integer,integer) pg_catalog.btint4sortsupport(internal)`
				held := definitionHeld(t, p, c, q)
				rows, err := c.QueryContext(p.Ctx, `SELECT amname,amhandler::oid::bigint,amtype FROM pg_am WHERE amname='tx_control_private'`)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				columns, err := rows.ColumnTypes()
				if err != nil || len(columns) != 3 {
					t.Fatal("retained descriptor metadata", columns, err)
				}
				for i, want := range []struct{ name, typ string }{{"amname", "NAME"}, {"amhandler", "INT8"}, {"amtype", "CHAR"}} {
					if columns[i].Name() != want.name || columns[i].DatabaseTypeName() != want.typ {
						t.Fatalf("retained column %d: %s %s", i, columns[i].Name(), columns[i].DatabaseTypeName())
					}
				}
				if replacement == "reset" {
					err = stuntgres.Reset(p.Ctx, p.Native)
				} else {
					err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes()))
				}
				if err != nil {
					t.Fatal(err)
				}
				fixture.DeleteReferenceConsume(t, false, rows, nil, fixture.Select(`SELECT amname,amhandler::oid::bigint,amtype FROM pg_am WHERE amname='tx_control_private'`, []string{"amname", "amhandler", "amtype"}, []string{"NAME", "INT8", "CHAR"}, [][]any{{"tx_control_private", int64(330), "i"}}))
				_, err = held.Statement.ExecContext(p.Ctx)
				fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				_, err = c.ExecContext(p.Ctx, `ROLLBACK TO graph`)
				fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				_, err = c.ExecContext(p.Ctx, `COMMIT`)
				fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				run(fixture.Exec(`ROLLBACK`, 0))
				run(definitionCount(`SELECT count(*) FROM pg_am WHERE amname='tx_control_private'`, 0))
				run(fixture.Exec(`CREATE ACCESS METHOD tx_control_private TYPE TABLE HANDLER heap_tableam_handler`, 0))
				run(fixture.Exec(`DROP ACCESS METHOD tx_control_private`, 0))
			})
		})
	}
}
