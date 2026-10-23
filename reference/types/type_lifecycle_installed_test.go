package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTypeLifecycleInstalledIdentity(t *testing.T) {
	for _, tc := range []struct{ extension, typ, input, output string }{
		{"cube", "cube", "(1,2)", "(1, 2)"},
		{"intarray", "query_int", "1", "1"},
		{"citext", "citext", "MiXeD", "MiXeD"},
		{"earthdistance", "earth", "(6378168,0,0)", "(6378168, 0, 0)"},
		{"isn", "ean13", "1234567890120!", "123-456789012-8!"},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"tl_installed_owner"})
			typeLifecycleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				domainIdentityDDL(t, p, c, r, "CREATE SCHEMA tl_src", "CREATE SCHEMA tl_dst", "CREATE ROLE tl_installed_owner", "SET search_path TO tl_src,public")
				if tc.extension == "earthdistance" {
					domainIdentityDDL(t, p, c, r, "CREATE EXTENSION cube WITH SCHEMA tl_src")
				}
				domainIdentityDDL(t, p, c, r, "CREATE EXTENSION "+tc.extension+" WITH SCHEMA tl_src", "CREATE TABLE public.tl_installed(id integer,v tl_src."+tc.typ+")")
				run(fixture.Exec("INSERT INTO public.tl_installed VALUES(1,$1),(2,NULL)", 2, tc.input))
				wantRows := [][]any{{int64(1), tc.output}, {int64(2), nil}}
				checkCodec := func(name string) {
					t.Helper()
					rawQ := "SELECT id,v FROM public.tl_installed ORDER BY id"
					var raw fixture.Step
					switch tc.typ {
					case "cube":
						raw = cubeSelect(b, rawQ, []string{"id", "v"}, wantRows)
					case "query_int":
						raw = queryIntSelect(b, rawQ, []string{"id", "v"}, wantRows)
					case "citext":
						raw = citextSelect(b, rawQ, []string{"id", "v"}, wantRows)
					case "earth":
						raw = earthSelect(b, rawQ, []string{"id", "v"}, wantRows)
					case "ean13":
						raw = ean13Select(b, rawQ, []string{"id", "v"}, []string{"INT4", "EAN13"}, wantRows, []bool{true, true})
					}
					run(raw)
					run(fixture.Prepared(raw))
					for _, value := range []struct{ input, output any }{{tc.input, tc.output}, {nil, nil}} {
						s := fixture.Select("SELECT ($1::"+name+")::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{value.output}}, value.input)
						run(s)
						run(fixture.Prepared(s))
					}
				}
				checkCodec("tl_src." + tc.typ)
				oid := domainAlterOID(t, p, c, "SELECT 'tl_src."+tc.typ+"'::regtype::oid")
				array := domainAlterOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tl_src."+tc.typ+"'::regtype::oid")
				ext := domainAlterOID(t, p, c, "SELECT oid FROM pg_extension WHERE extname='"+tc.extension+"'")
				// Real stored I/O signatures and extension membership are captured
				// independently of mutable member spelling.
				input := domainAlterOID(t, p, c, "SELECT typinput::oid FROM pg_type WHERE oid='tl_src."+tc.typ+"'::regtype::oid")
				output := domainAlterOID(t, p, c, "SELECT typoutput::oid FROM pg_type WHERE oid='tl_src."+tc.typ+"'::regtype::oid")
				var domainBase int64
				if tc.typ == "earth" {
					domainBase = domainAlterOID(t, p, c, "SELECT 'tl_src.cube'::regtype::oid")
				}
				checkDomain := func(name string) {
					t.Helper()
					domainIdentitySameOID(t, p, c, "SELECT typbasetype::oid FROM pg_type WHERE oid='"+name+"'::regtype::oid", domainBase)
					s := fixture.Select("SELECT typtype,typinput::oid=2597::oid AS domain_input,typoutput::oid=(SELECT typoutput::oid FROM pg_type WHERE oid='tl_src.cube'::regtype::oid) AS base_output FROM pg_type WHERE oid='"+name+"'::regtype::oid", []string{"typtype", "domain_input", "base_output"}, []string{"CHAR", "BOOL", "BOOL"}, [][]any{{"d", true, true}})
					run(s)
					run(fixture.Prepared(s))
				}
				inputSignature, outputSignature := "", ""
				switch tc.typ {
				case "cube":
					inputSignature, outputSignature = "cube_in(cstring)", "cube_out(cube)"
				case "query_int":
					inputSignature, outputSignature = "bqarr_in(cstring)", "bqarr_out(query_int)"
				case "citext":
					inputSignature, outputSignature = "citextin(cstring)", "citextout(citext)"
				case "earth":
					outputSignature = "cube_out(cube)"
				case "ean13":
					inputSignature, outputSignature = "ean13_in(cstring)", "ean13_out(ean13)"
				}
				type ioStamp struct {
					name                            string
					result, owner, namespace, arity int64
				}
				captureIO := func(id int64, wantSignature string, wantArity int64) ioStamp {
					t.Helper()
					var stamp ioStamp
					var signature string
					if err := c.QueryRowContext(p.Ctx, "SELECT proname,oid::regprocedure::text,prorettype::oid,proowner::oid,pronamespace::oid,pronargs::bigint FROM pg_proc WHERE oid=$1::oid", id).Scan(&stamp.name, &signature, &stamp.result, &stamp.owner, &stamp.namespace, &stamp.arity); err != nil {
						t.Fatal(err)
					}
					if signature != wantSignature || stamp.arity != wantArity {
						t.Fatalf("I/O signature %q/%d, want %q/%d", signature, stamp.arity, wantSignature, wantArity)
					}
					return stamp
				}
				// Domain input is inherited codec/CHECK authority with a released
				// pg_type I/O ID, not a newly required pg_proc builtin inventory.
				var inputStamp ioStamp
				if tc.typ == "earth" {
					checkDomain("tl_src.earth")
				} else {
					inputStamp = captureIO(input, inputSignature, 1)
				}
				outputStamp := captureIO(output, outputSignature, 1)
				selectQ := "SELECT id,v::text AS v FROM public.tl_installed ORDER BY id"
				consumer := typeLifecycleHeld(t, p, c, selectQ)
				renameQ := "ALTER TYPE tl_src." + tc.typ + " RENAME TO moved"
				rename := typeLifecycleHeld(t, p, c, renameQ)
				domainIdentitySameOID(t, p, c, "SELECT 'tl_src."+tc.typ+"'::regtype::oid", oid)
				fixture.RunStep(t, p.Ctx, rename, r, fixture.Exec(renameQ, 0))
				domainIdentityDDL(t, p, c, r, "ALTER TYPE tl_src.moved SET SCHEMA tl_dst", "ALTER TYPE tl_dst.moved OWNER TO tl_installed_owner")
				domainIdentitySameOID(t, p, c, "SELECT 'tl_dst.moved'::regtype::oid", oid)
				domainIdentitySameOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tl_dst.moved'::regtype::oid", array)
				domainIdentitySameOID(t, p, c, "SELECT typinput::oid FROM pg_type WHERE oid='tl_dst.moved'::regtype::oid", input)
				domainIdentitySameOID(t, p, c, "SELECT typoutput::oid FROM pg_type WHERE oid='tl_dst.moved'::regtype::oid", output)
				if tc.typ != "earth" {
					outputSignature = outputStamp.name + "(tl_dst.moved)"
					if captureIO(input, inputSignature, 1) != inputStamp {
						t.Fatal("identity ALTER changed input signature/provider ownership")
					}
				} else {
					checkDomain("tl_dst.moved")
				}
				if captureIO(output, outputSignature, 1) != outputStamp {
					t.Fatal("identity ALTER changed output signature/provider ownership")
				}
				oidQueryArgument(t, p, c, array, oid)
				typeLifecycleCount(t, p, c, r, "SELECT count(*) AS n FROM pg_depend WHERE classid=1247::oid AND objid=$1::oid AND refclassid=3079::oid AND refobjid=$2::oid AND deptype='e'", 1, oid, ext)
				run(fixture.Select("SELECT typname,pg_get_userbyid(typowner) AS owner FROM pg_type WHERE oid=$1::oid OR oid=$2::oid ORDER BY typname", []string{"typname", "owner"}, []string{"NAME", "NAME"}, [][]any{{"_moved", "tl_installed_owner"}, {"moved", "tl_installed_owner"}}, oid, array))
				checkCodec("tl_dst.moved")
				s := fixture.Select(selectQ, []string{"id", "v"}, []string{"INT4", "TEXT"}, wantRows)
				fixture.RunStep(t, p.Ctx, consumer, r, s)
				run(fixture.Prepared(s))
				run(fixture.Exec("INSERT INTO public.tl_installed VALUES(3,$1)", 1, tc.input))
				run(fixture.Select("SELECT v::text AS v FROM public.tl_installed WHERE id=3", []string{"v"}, []string{"TEXT"}, [][]any{{tc.output}}))
				// Direct deletion remains extension-protected after ALTER.
				want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop type tl_dst.moved because extension "+tc.extension+" requires it")
				want.Hint = "You can drop extension " + tc.extension + " instead."
				for _, suffix := range []string{"", " CASCADE"} {
					typeLifecycleFailure(t, p, c, "DROP TYPE tl_dst.moved"+suffix, want)
				}
				arrayDrop := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop type tl_dst.moved[] because extension "+tc.extension+" requires it")
				arrayDrop.Hint = "You can drop extension " + tc.extension + " instead."
				for _, suffix := range []string{" RESTRICT", " CASCADE"} {
					typeLifecycleFailure(t, p, c, "DROP TYPE tl_dst._moved"+suffix, arrayDrop)
				}
				// Old spelling fails independently; dropping the extension still owns
				// the changed member and removes only its dependent column.
				typeLifecycleFailure(t, p, c, "ALTER TYPE tl_src."+tc.typ+" RENAME TO x", fixture.ControlsReferenceDiagnostic("42704", `type "tl_src.`+tc.typ+`" does not exist`))
				domainIdentityDDL(t, p, c, r, "DROP EXTENSION "+tc.extension+" CASCADE")
				typeLifecycleCount(t, p, c, r, "SELECT count(*) AS n FROM pg_type WHERE oid=$1::oid OR oid=$2::oid", 0, oid, array)
				run(fixture.Select("SELECT id FROM public.tl_installed ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
			})
		})
	}
}

var typeLifecycleEanRows = [][]any{
	{int64(1), "012-345678901-2", "123-456789012-8!"},
	{int64(2), nil, "978-0-12-345678-6"},
}

const typeLifecycleEanQuery = "SELECT id,v,required FROM public.tl_ean ORDER BY id"

func typeLifecycleEanSelect(b fixture.Backend, empty bool) fixture.Step {
	q, rows := typeLifecycleEanQuery, typeLifecycleEanRows
	if empty {
		q, rows = "SELECT id,v,required FROM public.tl_ean WHERE id=99", [][]any{}
	}
	return ean13Select(b, q, []string{"id", "v", "required"}, []string{"INT4", "EAN13", "EAN13"}, rows, []bool{false, true, false})
}

func TestReferenceTypeLifecycleEan13IdentityConsumers(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"tl_ean_owner"})
	typeLifecycleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		domainIdentityDDL(t, p, c, r, "CREATE ROLE tl_ean_owner", "CREATE SCHEMA tl_src", "CREATE SCHEMA tl_dst",
			"CREATE EXTENSION isn WITH SCHEMA tl_src",
			"CREATE TABLE public.tl_ean(id integer NOT NULL,v tl_src.ean13,required tl_src.ean13 NOT NULL)")
		run(fixture.Exec("INSERT INTO public.tl_ean VALUES(1,'0123456789012','1234567890120!'),(2,NULL,'9780123456786')", 2))
		oid := domainAlterOID(t, p, c, "SELECT 'tl_src.ean13'::regtype::oid")
		array := domainAlterOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tl_src.ean13'::regtype::oid")
		input := domainAlterOID(t, p, c, "SELECT typinput::oid FROM pg_type WHERE oid='tl_src.ean13'::regtype::oid")
		output := domainAlterOID(t, p, c, "SELECT typoutput::oid FROM pg_type WHERE oid='tl_src.ean13'::regtype::oid")
		ext := domainAlterOID(t, p, c, "SELECT oid FROM pg_extension WHERE extname='isn'")
		// Save public descriptors, but release streaming PostgreSQL rows before DDL.
		rows, err := c.QueryContext(p.Ctx, typeLifecycleEanQuery)
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
		selectHeld := typeLifecycleHeld(t, p, c, typeLifecycleEanQuery)
		emptyHeld := typeLifecycleHeld(t, p, c, "SELECT id,v,required FROM public.tl_ean WHERE id=99")
		castQ := "SELECT ($1::tl_src.ean13)::text AS v"
		castHeld := typeLifecycleHeld(t, p, c, castQ)
		insertQ := "INSERT INTO public.tl_ean VALUES($1,$2,$3)"
		insertHeld := typeLifecycleHeld(t, p, c, insertQ)
		literalCastQ := "SELECT ('1234567890120!'::tl_src.ean13)::text AS v"
		literalCastHeld := typeLifecycleHeld(t, p, c, literalCastQ)
		literalInsertQ := "INSERT INTO public.tl_ean VALUES(4,'0123456789012','1234567890120!')"
		literalInsertHeld := typeLifecycleHeld(t, p, c, literalInsertQ)
		checkConsumers := func(name string) {
			t.Helper()
			fixture.RunStep(t, p.Ctx, literalCastHeld, r, fixture.Select(literalCastQ, []string{"v"}, []string{"TEXT"}, [][]any{{"123-456789012-8!"}}))
			literalCast := fixture.Select("SELECT ('1234567890120!'::"+name+")::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"123-456789012-8!"}})
			run(literalCast)
			run(fixture.Prepared(literalCast))
			fixture.RunStep(t, p.Ctx, literalInsertHeld, r, fixture.Exec(literalInsertQ, 1))
			run(ean13Select(b, "SELECT id,v,required FROM public.tl_ean WHERE id=4", []string{"id", "v", "required"}, []string{"INT4", "EAN13", "EAN13"}, [][]any{{int64(4), "012-345678901-2", "123-456789012-8!"}}, []bool{false, true, false}))
			run(fixture.Exec("DELETE FROM public.tl_ean WHERE id=4", 1))
			for _, empty := range []bool{false, true} {
				s := typeLifecycleEanSelect(b, empty)
				run(s)
				run(fixture.Prepared(s))
				held := selectHeld
				if empty {
					held = emptyHeld
				}
				fixture.RunStep(t, p.Ctx, held, r, s)
			}
			for _, value := range []struct{ input, output any }{{"1234567890120!", "123-456789012-8!"}, {nil, nil}} {
				fixture.RunStep(t, p.Ctx, castHeld, r, fixture.Select(castQ, []string{"v"}, []string{"TEXT"}, [][]any{{value.output}}, value.input))
				s := fixture.Select("SELECT ($1::"+name+")::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{value.output}}, value.input)
				run(s)
				run(fixture.Prepared(s))
			}
			ean13ColumnMetadata(t, b.PG, metadata, []string{"id", "v", "required"}, []string{"INT4", "EAN13", "EAN13"}, []bool{false, true, false})
			domainIdentitySameOID(t, p, c, "SELECT '"+name+"'::regtype::oid", oid)
			domainIdentitySameOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='"+name+"'::regtype::oid", array)
			domainIdentitySameOID(t, p, c, "SELECT typinput::oid FROM pg_type WHERE oid='"+name+"'::regtype::oid", input)
			domainIdentitySameOID(t, p, c, "SELECT typoutput::oid FROM pg_type WHERE oid='"+name+"'::regtype::oid", output)
			domainIdentitySameOID(t, p, c, "SELECT oid FROM pg_extension WHERE extname='isn'", ext)
			oidQueryArgument(t, p, c, array, oid)
		}
		checkConsumers("tl_src.ean13")
		domainIdentityDDL(t, p, c, r, "ALTER TYPE tl_src.ean13 RENAME TO moved")
		checkConsumers("tl_src.moved")
		domainIdentityDDL(t, p, c, r, "ALTER TYPE tl_src.moved SET SCHEMA tl_dst")
		checkConsumers("tl_dst.moved")
		domainIdentityDDL(t, p, c, r, "ALTER TYPE tl_dst.moved OWNER TO tl_ean_owner")
		checkConsumers("tl_dst.moved")
		fixture.RunStep(t, p.Ctx, insertHeld, r, fixture.Exec(insertQ, 1, 3, []byte("9790123456785"), "1234567890120!"))
		run(ean13Select(b, "SELECT id,v,required FROM public.tl_ean WHERE id=3", []string{"id", "v", "required"}, []string{"INT4", "EAN13", "EAN13"}, [][]any{{int64(3), "979-0-1234-5678-5", "123-456789012-8!"}}, []bool{false, true, false}))
		run(fixture.Exec("DELETE FROM public.tl_ean WHERE id=3", 1))
		owner := "postgres"
		if b.PG {
			owner = "stuntgres"
		}
		typeLifecycleEanVerify(t, p, b, c, r, owner)
		if b.PG {
			return
		}
		typeLifecycleEanControls(t, p, b, c, r)
	})
}

// These logical checks also apply after Restore, where allocated OIDs and role
// IDs need not equal the source IDs. I/O routines keep the extension's owner and
// namespace, not the independently changed base-type owner and namespace.
func typeLifecycleEanVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, r *fixture.Recorder, owner string) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, r, s) }
	for _, empty := range []bool{false, true} {
		s := typeLifecycleEanSelect(b, empty)
		run(s)
		run(fixture.Prepared(s))
	}
	run(fixture.Select("SELECT t.typname,n.nspname,pg_get_userbyid(t.typowner) AS owner,a.typname AS array_name,an.nspname AS array_schema,pg_get_userbyid(a.typowner) AS array_owner,a.typelem=t.oid AS reciprocal FROM pg_type t JOIN pg_type a ON a.oid=t.typarray JOIN pg_namespace n ON n.oid=t.typnamespace JOIN pg_namespace an ON an.oid=a.typnamespace WHERE t.oid='tl_dst.moved'::regtype::oid",
		[]string{"typname", "nspname", "owner", "array_name", "array_schema", "array_owner", "reciprocal"},
		[]string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "BOOL"},
		[][]any{{"moved", "tl_dst", "tl_ean_owner", "_moved", "tl_dst", "tl_ean_owner", true}}))
	run(fixture.Select("SELECT e.extversion,n.nspname,pg_get_userbyid(e.extowner) AS owner FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='isn'",
		[]string{"extversion", "nspname", "owner"}, []string{"TEXT", "NAME", "NAME"}, [][]any{{"1.3", "tl_src", owner}}))
	run(fixture.Select("SELECT p.proname,n.nspname,pg_get_userbyid(p.proowner) AS owner,p.pronargs::bigint AS arity,p.prorettype=t.oid AS returns_base,p.oid=t.typinput AS is_input,p.oid=t.typoutput AS is_output FROM pg_type t JOIN pg_proc p ON p.oid=t.typinput OR p.oid=t.typoutput JOIN pg_namespace n ON n.oid=p.pronamespace WHERE t.oid='tl_dst.moved'::regtype::oid ORDER BY p.proname",
		[]string{"proname", "nspname", "owner", "arity", "returns_base", "is_input", "is_output"},
		[]string{"NAME", "NAME", "NAME", "INT8", "BOOL", "BOOL", "BOOL"},
		[][]any{{"ean13_in", "tl_src", owner, int64(1), true, true, false}, {"ean13_out", "tl_src", owner, int64(1), false, false, true}}))
	run(fixture.Select("SELECT 'tl_src.ean13_in(cstring)'::regprocedure::oid=t.typinput AS input_identity,'tl_src.ean13_out(tl_dst.moved)'::regprocedure::oid=t.typoutput AS output_identity,t.typinput<>t.typoutput AS distinct_io,t.oid<>t.typarray AS distinct_array FROM pg_type t WHERE t.oid='tl_dst.moved'::regtype::oid",
		[]string{"input_identity", "output_identity", "distinct_io", "distinct_array"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
	for _, member := range []struct{ class, address string }{
		{"1247", "'tl_dst.moved'::regtype::oid"},
		{"1255", "'tl_src.ean13_in(cstring)'::regprocedure::oid"},
		{"1255", "'tl_src.ean13_out(tl_dst.moved)'::regprocedure::oid"},
	} {
		typeLifecycleCount(t, p, x, r, "SELECT count(*) AS n FROM pg_depend WHERE classid="+member.class+"::oid AND objid="+member.address+" AND refclassid=3079::oid AND refobjid=(SELECT oid FROM pg_extension WHERE extname='isn') AND deptype='e'", 1)
	}
}

func typeLifecycleEanControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	old, err := c.QueryContext(p.Ctx, typeLifecycleEanQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	metadata, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	typeLifecycleEanVerify(t, p, b, c, r, "postgres")
	got := [][]any{}
	for old.Next() {
		var id, value, required any
		if err := old.Scan(&id, &value, &required); err != nil {
			t.Fatal(err)
		}
		if _, ok := required.(string); !ok {
			t.Fatalf("retained required raw = %T, want string", required)
		}
		if value != nil {
			if _, ok := value.(string); !ok {
				t.Fatalf("retained nullable raw = %T, want string", value)
			}
		}
		got = append(got, []any{fixture.Normalize(id), value, required})
	}
	if err := old.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, typeLifecycleEanRows) {
		t.Fatalf("retained rows = %#v, want %#v", got, typeLifecycleEanRows)
	}
	ean13ColumnMetadata(t, false, metadata, []string{"id", "v", "required"}, []string{"INT4", "EAN13", "EAN13"}, []bool{false, true, false})
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("same-target identity Restore changed deterministic Dump")
	}
	target := fixture.NewFixture(t, false, true)
	domainIdentityDDL(t, target, target.Memory, target.MemoryRecord, "CREATE ROLE tl_ean_spacer", "CREATE ROLE tl_ean_owner", "CREATE TABLE public.tl_ean_spacer(v integer)", "CREATE EXTENSION isn")
	var sourceOID, destinationOID int64
	if err := c.QueryRowContext(p.Ctx, "SELECT 'tl_dst.moved'::regtype::oid").Scan(&sourceOID); err != nil {
		t.Fatal(err)
	}
	if err := target.Memory.QueryRowContext(target.Ctx, "SELECT 'public.ean13'::regtype::oid").Scan(&destinationOID); err != nil {
		t.Fatal(err)
	}
	if sourceOID == destinationOID {
		t.Fatal("fresh destination did not displace EAN13 allocation")
	}
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	typeLifecycleEanVerify(t, target, fixture.Backend{}, target.Memory, target.MemoryRecord, "postgres")
	redump.Reset()
	if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("fresh-target identity Restore changed deterministic Dump")
	}
	if p.Postgres != nil {
		typeLifecycleEanReplay(t, p, dump.String())
	}
}

func typeLifecycleEanReplay(t *testing.T, p *fixture.Pair, dump string) {
	t.Helper()
	c, err := p.Postgres.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "RESET ROLE", "DROP EXTENSION IF EXISTS isn CASCADE", "DROP SCHEMA IF EXISTS tl_src CASCADE", "DROP SCHEMA IF EXISTS tl_dst CASCADE", "DROP SCHEMA public CASCADE", "CREATE SCHEMA public", "DROP ROLE IF EXISTS tl_ean_owner", "DISCARD ALL"} {
			if _, err := c.ExecContext(ctx, q); err != nil {
				t.Errorf("EAN identity replay cleanup %s: %v", q, err)
			}
		}
	}()
	if _, err := c.ExecContext(p.Ctx, "CREATE ROLE tl_ean_owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ExecContext(p.Ctx, dump); err != nil {
		t.Fatal("actual native identity Dump PostgreSQL replay", err)
	}
	for _, q := range []string{"RESET SESSION AUTHORIZATION", "RESET ROLE"} {
		if _, err := c.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	typeLifecycleEanVerify(t, p, fixture.Backend{PG: true}, c, p.PostgresRecord, "postgres")
}
