package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceIsbnIdentityControls(t *testing.T) {
	for _, cycle := range []bool{false, true} {
		name := "independent_moves"
		if cycle {
			name = "canonical_name_cycle"
		}
		t.Run(name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"isbn_e_owner", "isbn_v_owner", "isbn_io_owner"})
			fixture.SchemaReferenceCleanup(t, "isbn_src")
			fixture.SchemaReferenceCleanup(t, "isbn_dst")
			ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, q := range []string{"CREATE ROLE isbn_e_owner", "CREATE ROLE isbn_v_owner", "CREATE ROLE isbn_io_owner", "CREATE SCHEMA isbn_src", "CREATE SCHEMA isbn_dst", "CREATE EXTENSION isn SCHEMA isbn_src", "CREATE TABLE public.isbn_identity(id integer NOT NULL,e isbn_src.ean13,v isbn_src.isbn)"} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("INSERT INTO public.isbn_identity VALUES(1,'9780123456786','1234567890!'),(2,NULL,NULL)", 2))
				expected := [][]any{{int64(1), "978-0-12-345678-6", "1-234-56789-X!"}, {int64(2), nil, nil}}
				query := "SELECT id,e,v FROM public.isbn_identity ORDER BY id"
				selectHeld := ean13Held(t, p, c, query)
				insertHeld := ean13Held(t, p, c, "INSERT INTO public.isbn_identity VALUES($1,$2,$3)")
				literalQuery := "SELECT ('9780123456786'::isbn_src.ean13)::text AS e,('1234567890!'::isbn_src.isbn)::text AS v"
				literalHeld := ean13Held(t, p, c, literalQuery)
				boundQuery := "SELECT ($1::isbn_src.ean13)::text AS e,($2::isbn_src.isbn)::text AS v"
				boundHeld := ean13Held(t, p, c, boundQuery)
				stamps := map[string][4]int64{}
				for _, typ := range []string{"ean13", "isbn", "isbn13", "ismn", "ismn13"} {
					stamps[typ] = isbnIdentityStamp(t, p, c, "isbn_src."+typ)
				}
				// Save descriptors before any mutable name, namespace or owner
				// changes; release pq streams before issuing DDL on this session.
				rows, err := c.QueryContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				metadata, err := rows.ColumnTypes()
				if err != nil {
					rows.Close()
					t.Fatal(err)
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				eName, vName := "isbn_dst.moved_e", "isbn_dst.moved_v"
				if cycle {
					for _, q := range []string{"ALTER TYPE isbn_src.ean13 RENAME TO parked", "ALTER TYPE isbn_src.isbn RENAME TO ean13", "ALTER TYPE isbn_src.parked RENAME TO isbn"} {
						run(fixture.Exec(q, 0))
					}
					eName, vName = "isbn_src.isbn", "isbn_src.ean13"
				} else {
					for _, q := range []string{"ALTER TYPE isbn_src.ean13 RENAME TO moved_e", "ALTER TYPE isbn_src.isbn RENAME TO moved_v", "ALTER TYPE isbn_src.moved_e SET SCHEMA isbn_dst", "ALTER TYPE isbn_src.moved_v SET SCHEMA isbn_dst"} {
						run(fixture.Exec(q, 0))
					}
				}
				for _, q := range []string{"ALTER TYPE " + eName + " OWNER TO isbn_e_owner", "ALTER TYPE " + vName + " OWNER TO isbn_v_owner",
					"ALTER FUNCTION isbn_src.isbn_in(cstring) RENAME TO moved_isbn_input",
					"ALTER FUNCTION isbn_src.moved_isbn_input(cstring) SET SCHEMA isbn_dst",
					"ALTER FUNCTION isbn_dst.moved_isbn_input(cstring) OWNER TO isbn_io_owner",
					"ALTER FUNCTION isbn_src.isn_out(" + vName + ") RENAME TO moved_isbn_output",
					"ALTER FUNCTION isbn_src.moved_isbn_output(" + vName + ") SET SCHEMA isbn_dst",
					"ALTER FUNCTION isbn_dst.moved_isbn_output(" + vName + ") OWNER TO isbn_io_owner"} {
					run(fixture.Exec(q, 0))
				}
				for typ, current := range map[string]string{"ean13": eName, "isbn": vName} {
					if got := isbnIdentityStamp(t, p, c, current); got != stamps[typ] {
						t.Fatalf("%s ALTER changed identity: %v, want %v", typ, got, stamps[typ])
					}
				}
				ean13ColumnMetadata(t, b.PG, metadata, []string{"id", "e", "v"}, []string{"INT4", "EAN13", "ISBN"}, []bool{false, true, true})
				read := isbnControlSelect(b, query, expected)
				fixture.RunStep(t, p.Ctx, selectHeld, b.Recorder, read)
				fixture.RunStep(t, p.Ctx, literalHeld, b.Recorder, fixture.Select(literalQuery, []string{"e", "v"}, []string{"TEXT", "TEXT"}, [][]any{{"978-0-12-345678-6", "1-234-56789-X!"}}))
				for _, values := range [][]any{{"9780123456786", "1234567890!"}, {nil, nil}} {
					want := [][]any{{"978-0-12-345678-6", "1-234-56789-X!"}}
					if values[0] == nil {
						want = [][]any{{nil, nil}}
					}
					fixture.RunStep(t, p.Ctx, boundHeld, b.Recorder, fixture.Select(boundQuery, []string{"e", "v"}, []string{"TEXT", "TEXT"}, want, values...))
				}
				fixture.RunStep(t, p.Ctx, insertHeld, b.Recorder, fixture.Exec(insertHeld.SQL, 1, 3, []byte("9791234567896"), []byte("9780123456780!")))
				run(ean13Select(b, "SELECT e,v FROM public.isbn_identity WHERE id=3", []string{"e", "v"}, []string{"EAN13", "ISBN"}, [][]any{{"979-123456789-6", "0-12-345678-9!"}}, []bool{true, true}))
				run(fixture.Exec("DELETE FROM public.isbn_identity WHERE id=3", 1))
				run(fixture.Exec("INSERT INTO public.isbn_identity SELECT 4,e,v FROM public.isbn_identity WHERE id=1", 1))
				run(ean13Select(b, "SELECT e,v FROM public.isbn_identity WHERE id=4", []string{"e", "v"}, []string{"EAN13", "ISBN"}, [][]any{{"978-0-12-345678-6", "1-234-56789-X!"}}, []bool{true, true}))
				run(fixture.Exec("DELETE FROM public.isbn_identity WHERE id=4", 1))
				isbnIdentityPrivileges(t, p, b, c, vName)
				isbnIdentityVerify(t, p, b, c, eName, vName)
				if b.PG {
					return
				}
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				old, columns := isbnRetain(t, p, c, query)
				defer old.Close()
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				isbnIdentityVerify(t, p, b, c, eName, vName)
				isbnConsumeRetained(t, old, columns, expected)
				var second bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &second); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(dump.Bytes(), second.Bytes()) {
					t.Fatal("identity replay changed deterministic dump")
				}
				target := fixture.NewFixture(t, false, true)
				tc, err := target.Memory.Conn(target.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tc.Close()
				tb := fixture.Backend{Recorder: target.MemoryRecord}
				for _, q := range []string{"CREATE ROLE isbn_role_spacer", "CREATE ROLE isbn_e_owner", "CREATE ROLE isbn_v_owner", "CREATE ROLE isbn_io_owner", "CREATE TABLE isbn_oid_spacer(v integer)", "CREATE EXTENSION isn"} {
					fixture.RunStep(t, target.Ctx, tc, tb.Recorder, fixture.Exec(q, 0))
				}
				for _, typ := range []string{"ean13", "isbn", "isbn13", "ismn", "ismn13"} {
					destination := isbnIdentityStamp(t, target, tc, "public."+typ)
					for i, source := range stamps[typ] {
						if source == destination[i] {
							t.Fatalf("fresh %s identity %d was not displaced", typ, i)
						}
					}
				}
				if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				isbnIdentityVerify(t, target, tb, tc, eName, vName)
				second.Reset()
				if err := stuntgres.Dump(target.Ctx, target.Native, &second); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(dump.Bytes(), second.Bytes()) {
					t.Fatal("fresh destination changed symbolic owners/identities")
				}
				if p.Postgres != nil {
					isbnReplay(t, p, dump.String(), []string{"CREATE ROLE isbn_e_owner", "CREATE ROLE isbn_v_owner", "CREATE ROLE isbn_io_owner"}, func(pg *sql.Conn, pb fixture.Backend) {
						isbnIdentityVerify(t, p, pb, pg, eName, vName)
					})
				}
			})
		})
	}
}

func isbnIdentityStamp(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) [4]int64 {
	t.Helper()
	var stamp [4]int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid,typarray,typinput::oid,typoutput::oid FROM pg_type WHERE oid="+ean13Literal(name)+"::regtype::oid").Scan(&stamp[0], &stamp[1], &stamp[2], &stamp[3]); err != nil {
		t.Fatal(err)
	}
	return stamp
}

func isbnIdentityVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, eName, vName string) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	for _, empty := range []bool{false, true} {
		q := "SELECT id,e,v FROM public.isbn_identity ORDER BY id"
		expected := [][]any{{int64(1), "978-0-12-345678-6", "1-234-56789-X!"}, {int64(2), nil, nil}}
		if empty {
			q, expected = "SELECT id,e,v FROM public.isbn_identity WHERE id=99", [][]any{}
		}
		s := isbnControlSelect(b, q, expected)
		run(s)
		run(fixture.Prepared(s))
	}
	e, v := isbnIdentityStamp(t, p, c, eName), isbnIdentityStamp(t, p, c, vName)
	for _, role := range []struct {
		name, owner, input, output string
		array int64
	}{
		{eName, "isbn_e_owner", "isbn_src.ean13_in(cstring)", "isbn_src.ean13_out(" + eName + ")", e[1]},
		{vName, "isbn_v_owner", "isbn_dst.moved_isbn_input(cstring)", "isbn_dst.moved_isbn_output(" + vName + ")", v[1]},
	} {
		q := fmt.Sprintf("SELECT pg_get_userbyid(t.typowner) AS owner,pg_get_userbyid(a.typowner) AS array_owner,a.typelem=t.oid AS reciprocal,t.typnamespace=a.typnamespace AS same_schema,t.typinput=%s::regprocedure::oid AS input_identity,t.typoutput=%s::regprocedure::oid AS output_identity FROM (SELECT * FROM pg_type WHERE oid=%s::regtype::oid) t JOIN (SELECT * FROM pg_type WHERE oid=%d) a ON a.oid=t.typarray", ean13Literal(role.input), ean13Literal(role.output), ean13Literal(role.name), role.array)
		run(fixture.Select(q, []string{"owner", "array_owner", "reciprocal", "same_schema", "input_identity", "output_identity"}, []string{"NAME", "NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{role.owner, role.owner, true, true, true, true}}))
		for _, signature := range []string{role.input, role.output} {
			run(fixture.Select("SELECT p.prorettype="+ean13Literal(role.name)+"::regtype::oid AS returns_base,p.oid=t.typinput AS is_input,p.oid=t.typoutput AS is_output FROM (SELECT * FROM pg_proc WHERE oid="+ean13Literal(signature)+"::regprocedure::oid) p JOIN (SELECT * FROM pg_type WHERE oid="+ean13Literal(role.name)+"::regtype::oid) t ON t.oid="+ean13Literal(role.name)+"::regtype::oid", []string{"returns_base", "is_input", "is_output"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{signature == role.input, signature == role.input, signature == role.output}}))
		}
	}
	seen := map[int64]bool{}
	for _, id := range append(e[:], v[:]...) {
		if id <= 0 || seen[id] {
			t.Fatal("shared or invalid installed role identity", id)
		}
		seen[id] = true
	}
	for _, id := range []int64{e[0], v[0]} {
		run(fixture.Select(fmt.Sprintf("SELECT deptype FROM pg_depend WHERE classid=1247 AND objid=%d AND refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='isn')", id), []string{"deptype"}, []string{"CHAR"}, [][]any{{"e"}}))
	}
	// Ordinary ALTER FUNCTION OWNER SQL preserves the independently named
	// codec owners in both native Restore and actual PostgreSQL replay.
	run(fixture.Select("SELECT pg_get_userbyid(proowner) AS owner FROM pg_proc WHERE oid='isbn_dst.moved_isbn_input(cstring)'::regprocedure::oid OR oid="+ean13Literal("isbn_dst.moved_isbn_output("+vName+")")+"::regprocedure::oid", []string{"owner"}, []string{"NAME"}, [][]any{{"isbn_io_owner"}, {"isbn_io_owner"}}))
}

func isbnIdentityPrivileges(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	// GRANT/REVOKE materializes an explicit ACL even when its final rights
	// equal the default. Roll back the probe instead of trying to normalize
	// persistent namespace/type ACLs before the independent Dump witness.
	run(fixture.Exec("BEGIN", 0))
	active := true
	defer func() {
		if active {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := c.ExecContext(cleanup, "ROLLBACK"); err != nil {
				t.Error(err)
			}
		}
	}()
	for _, q := range []string{"GRANT USAGE ON SCHEMA isbn_src,isbn_dst TO isbn_e_owner", "GRANT USAGE,CREATE ON SCHEMA public TO isbn_e_owner", "REVOKE USAGE ON TYPE " + name + " FROM PUBLIC", "GRANT USAGE ON TYPE " + name + " TO isbn_e_owner", "SET ROLE isbn_e_owner"} {
		run(fixture.Exec(q, 0))
	}
	// Type USAGE governs new declarations, not ordinary casts/value reads.
	// A retained declaration must recheck privileges on every execution.
	query := "CREATE TABLE public.isbn_acl_probe(v " + name + ")"
	held := ean13Held(t, p, c, query)
	defer held.Statement.Close()
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 0))
	run(fixture.Exec("DROP TABLE public.isbn_acl_probe", 0))
	run(fixture.Exec("RESET ROLE", 0))
	run(fixture.Exec("REVOKE USAGE ON TYPE " + name + " FROM isbn_e_owner", 0))
	run(fixture.Exec("SET ROLE isbn_e_owner", 0))
	want := ean13Diagnostic("42501", "permission denied for type "+name)
	s := fixture.ErrorDiagnostic(query, want)
	s.ErrorStage = "execute"
	run(fixture.Exec("SAVEPOINT isbn_acl_denial", 0))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	run(fixture.Exec("ROLLBACK TO SAVEPOINT isbn_acl_denial", 0))
	run(fixture.Exec("RELEASE SAVEPOINT isbn_acl_denial", 0))
	run(fixture.Exec("RESET ROLE", 0))
	run(fixture.Exec("GRANT USAGE ON TYPE " + name + " TO isbn_e_owner", 0))
	run(fixture.Exec("SET ROLE isbn_e_owner", 0))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(query, 0))
	run(fixture.Exec("DROP TABLE public.isbn_acl_probe", 0))
	run(fixture.Exec("RESET ROLE", 0))
	if err := held.Statement.Close(); err != nil {
		t.Fatal(err)
	}
	run(fixture.Exec("ROLLBACK", 0))
	active = false
}
