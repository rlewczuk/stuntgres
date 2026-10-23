package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTypeLifecycleInstalledIdentity(t *testing.T) {
	for _, tc := range []struct{ extension, typ, input, output string }{
		{"cube", "cube", "(1,2)", "(1, 2)"},
		{"intarray", "query_int", "1", "1"},
		{"citext", "citext", "MiXeD", "MiXeD"},
		{"earthdistance", "earth", "(6378168,0,0)", "(6378168, 0, 0)"},
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
