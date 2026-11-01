package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceIsnConversionIdentity(t *testing.T) {
	for _, state := range []string{"detached", "foreign", "origin_absent"} {
		t.Run(state, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"isn_identity_owner", "isn_identity_reader"})
			isnConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := isnConversionRun(t, p, b, c)
				for _, q := range []string{
					"CREATE ROLE isn_identity_owner", "CREATE ROLE isn_identity_reader",
					"CREATE SCHEMA eo_old", "CREATE SCHEMA eo_new", "CREATE SCHEMA eo_other",
					"CREATE EXTENSION isn SCHEMA eo_old", "SET search_path=eo_old,pg_catalog",
					"CREATE TABLE public.isn_identity(id integer NOT NULL,v ean13)",
					"CREATE VIEW public.isn_identity_view AS SELECT id,isbn(v)::text AS book,v='123456789X'::isbn AS equal FROM public.isn_identity ORDER BY v ASC NULLS LAST,id ASC",
				} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("INSERT INTO public.isn_identity VALUES(1,'9781234567897'),(2,'9781234567897!'),(3,NULL)", 3))
				view := fixture.Select("SELECT id,book,equal FROM public.isn_identity_view ORDER BY id", []string{"id", "book", "equal"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), "1-234-56789-X", true}, {int64(2), "1-234-56789-X!", false}, {int64(3), nil, nil}})
				held := fixture.IdentityHeld(t, p.Ctx, c, view.Query)
				defer held.Statement.Close()
				fixture.RunStep(t, p.Ctx, held, b.Recorder, view)
				// Keep exactly the two operand roles, their codecs, direct casts,
				// selected cross predicates and complete EAN same-type sort recipe.
				// Other release members may be removed with the original header.
				objects := []string{
					"TYPE eo_old.ean13", "TYPE eo_old.isbn",
					"FUNCTION eo_old.ean13_in(cstring)", "FUNCTION eo_old.ean13_out(eo_old.ean13)",
					"FUNCTION eo_old.isbn_in(cstring)", "FUNCTION eo_old.isn_out(eo_old.isbn)",
					"FUNCTION eo_old.isbn(eo_old.ean13)",
					"CAST (eo_old.ean13 AS eo_old.isbn)", "CAST (eo_old.isbn AS eo_old.ean13)",
					"FUNCTION eo_old.btean13cmp(eo_old.ean13,eo_old.ean13)",
					"OPERATOR FAMILY eo_old.isn_ops USING btree",
					"OPERATOR CLASS eo_old.ean13_ops USING btree",
				}
				for _, pair := range [][2]string{{"ean13", "ean13"}, {"ean13", "isbn"}, {"isbn", "ean13"}} {
					for _, op := range isnConversionOps {
						signature := "(eo_old." + pair[0] + ",eo_old." + pair[1] + ")"
						objects = append(objects, "FUNCTION eo_old."+op.routine+signature, "OPERATOR eo_old."+op.operator+signature)
					}
				}
				for _, object := range objects {
					run(fixture.Exec("ALTER EXTENSION isn DROP "+object, 0))
				}
				if state == "foreign" {
					run(fixture.Exec(`CREATE EXTENSION "uuid-ossp" SCHEMA eo_other`, 0))
					for _, object := range objects {
						run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD `+object, 0))
					}
				}
				if state == "origin_absent" {
					run(fixture.Exec("DROP EXTENSION isn", 0))
				}
				membership := int64(0)
				if state == "foreign" {
					membership = 1
				}
				for _, object := range []struct{ class, identity string }{
					{"pg_type", "'eo_old.ean13'::regtype"},
					{"pg_proc", "'eo_old.isbn(eo_old.ean13)'::regprocedure"},
					{"pg_operator", "'eo_old.=(eo_old.ean13,eo_old.isbn)'::regoperator"},
				} {
					run(fixture.OwnershipCount("SELECT count(*) FROM pg_depend WHERE classid='"+object.class+"'::regclass AND objid="+object.identity+" AND refclassid='pg_extension'::regclass AND deptype='e'", membership))
				}
				origin := int64(1)
				if state == "origin_absent" {
					origin = 0
				}
				run(fixture.OwnershipCount("SELECT count(*) FROM pg_extension WHERE extname='isn'", origin))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, view)
				ids := map[string]int64{}
				for name, q := range map[string]string{
					"conversion": "SELECT 'eo_old.isbn(eo_old.ean13)'::regprocedure::bigint",
					"predicate":  "SELECT 'eo_old.isneq(eo_old.ean13,eo_old.isbn)'::regprocedure::bigint",
					"operator":   "SELECT 'eo_old.=(eo_old.ean13,eo_old.isbn)'::regoperator::bigint",
					"support":    "SELECT 'eo_old.btean13cmp(eo_old.ean13,eo_old.ean13)'::regprocedure::bigint",
					"type":       "SELECT 'eo_old.ean13'::regtype::bigint",
				} {
					ids[name] = fixture.OwnershipID(t, p, c, q)
				}
				for _, q := range []string{
					"ALTER FUNCTION eo_old.isbn(eo_old.ean13) RENAME TO checked_book",
					"ALTER FUNCTION eo_old.checked_book(eo_old.ean13) SET SCHEMA eo_new",
					"ALTER FUNCTION eo_old.isneq(eo_old.ean13,eo_old.isbn) RENAME TO same_book",
					"ALTER FUNCTION eo_old.same_book(eo_old.ean13,eo_old.isbn) SET SCHEMA eo_new",
					"ALTER OPERATOR eo_old.=(eo_old.ean13,eo_old.isbn) SET SCHEMA eo_new",
					"ALTER FUNCTION eo_old.btean13cmp(eo_old.ean13,eo_old.ean13) RENAME TO compare_articles",
					"ALTER FUNCTION eo_old.compare_articles(eo_old.ean13,eo_old.ean13) SET SCHEMA eo_new",
					"ALTER TYPE eo_old.ean13 RENAME TO article",
					"ALTER TYPE eo_old.article SET SCHEMA eo_new",
					"ALTER FUNCTION eo_new.checked_book(eo_new.article) OWNER TO isn_identity_owner",
				} {
					run(fixture.Exec(q, 0))
				}
				for name, q := range map[string]string{
					"conversion": "SELECT 'eo_new.checked_book(eo_new.article)'::regprocedure::bigint",
					"predicate":  "SELECT 'eo_new.same_book(eo_new.article,eo_old.isbn)'::regprocedure::bigint",
					"operator":   "SELECT 'eo_new.=(eo_new.article,eo_old.isbn)'::regoperator::bigint",
					"support":    "SELECT 'eo_new.compare_articles(eo_new.article,eo_new.article)'::regprocedure::bigint",
					"type":       "SELECT 'eo_new.article'::regtype::bigint",
				} {
					if fixture.OwnershipID(t, p, c, q) != ids[name] {
						t.Fatal("lawful move changed genuine identity", name)
					}
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, view)
				isnConversionRoutes(t, p, b, c, view)
				isnConversionRoutes(t, p, b, c, isnConversionIDs("SELECT id FROM public.isn_identity ORDER BY v ASC NULLS LAST,id ASC", 1, 2, 3))
				q := "SELECT eo_new.checked_book($1::eo_new.article)::text AS book,eo_new.same_book($1::eo_new.article,'123456789X'::eo_old.isbn) AS called,$1::eo_new.article OPERATOR(eo_new.=) '123456789X'::eo_old.isbn AS equal"
				isnConversionRoutes(t, p, b, c, fixture.Select(q, []string{"book", "called", "equal"}, []string{"TEXT", "BOOL", "BOOL"}, [][]any{{"1-234-56789-X", true, true}}, "9781234567897"))
				run(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_operator'::regclass AND objid='eo_new.=(eo_new.article,eo_old.isbn)'::regoperator AND refclassid='pg_proc'::regclass AND refobjid='eo_new.same_book(eo_new.article,eo_old.isbn)'::regprocedure AND deptype='n'`, 1))
				// ACLs belong to actual selected routines, including renamed calls.
				for _, statement := range []string{
					"GRANT USAGE ON SCHEMA eo_old,eo_new TO isn_identity_reader",
					"REVOKE EXECUTE ON FUNCTION eo_new.checked_book(eo_new.article) FROM PUBLIC",
					"SET ROLE isn_identity_reader",
				} {
					run(fixture.Exec(statement, 0))
				}
				denied := "SELECT eo_new.checked_book('9781234567897'::eo_new.article)::text"
				run(isnConversionRuntimeError(denied, isnConversionDiagnostic("42501", "permission denied for function checked_book")))
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION eo_new.checked_book(eo_new.article) TO isn_identity_reader", 0))
				run(fixture.Exec("SET ROLE isn_identity_reader", 0))
				isnConversionRoutes(t, p, b, c, fixture.Select(denied+" AS book", []string{"book"}, []string{"TEXT"}, [][]any{{"1-234-56789-X"}}))
				run(fixture.Exec("RESET ROLE", 0))
				isnConversionIdentityControls(t, p, b, c, state, view)
			})
		})
	}
}

func isnConversionIdentityControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, state string, view fixture.Step) {
	t.Helper()
	if b.PG {
		return
	}
	saved := fixture.ExtensionDump(t, p.Ctx, p.Native)
	queries := []string{
		fixture.OwnershipSurvivorDeclaration("eo_new.checked_book(eo_new.article)"),
		fixture.OwnershipSurvivorDeclaration("eo_new.same_book(eo_new.article,eo_old.isbn)"),
		fixture.OwnershipSurvivorDeclaration("eo_new.compare_articles(eo_new.article,eo_new.article)"),
	}
	before := make([][][]any, len(queries))
	for i, q := range queries {
		before[i] = fixture.OwnershipSnapshot(t, p, c, q)
	}
	connector, err := stuntgres.NewDriver().OpenConnector("isn_identity_" + state)
	if err != nil {
		t.Fatal(err)
	}
	recorded, recorder := fixture.RecordedNativePool(t, connector)
	other := sql.OpenDB(connector)
	defer other.Close()
	// Roles are cluster-level setup, not database Dump contents.
	for _, q := range []string{"CREATE ROLE isn_identity_owner", "CREATE ROLE isn_identity_reader", "CREATE TABLE isn_identity_spacer(v integer)"} {
		fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Exec(q, 0))
	}
	for _, target := range []struct {
		control, recorded *sql.DB
		recorder          *fixture.Recorder
	}{{p.Native, b.DB, b.Recorder}, {other, recorded, recorder}} {
		if err := stuntgres.Restore(p.Ctx, target.control, strings.NewReader(saved)); err != nil {
			t.Fatal(err)
		}
		conn, err := target.recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			destination := fixture.Backend{DB: target.recorded, Recorder: target.recorder}
			fixture.RunStep(t, p.Ctx, conn, target.recorder, fixture.Exec("SET search_path=eo_old,pg_catalog", 0))
			isnConversionRoutes(t, p, destination, conn, view)
			isnConversionRoutes(t, p, destination, conn, isnConversionIDs("SELECT id FROM public.isn_identity ORDER BY v ASC NULLS LAST,id ASC", 1, 2, 3))
			for i, q := range queries {
				fixture.OwnershipUnchanged(t, p, conn, q, before[i])
			}
			isnConversionAssertDump(t, saved, fixture.ExtensionDump(t, p.Ctx, target.control))
		}()
	}
}
