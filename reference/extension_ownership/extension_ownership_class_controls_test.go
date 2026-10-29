package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Every represented member owner, including global descriptors and recursive
// companions, crosses both logical replay and independent driver remap. This
// explicit native phase follows the class's complete shared public SQL witness.
func ownershipClassControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, tc ownershipClassCase) {
	t.Helper()
	if b.PG {
		return
	}
	run := ownershipRun(t, p, b, c)
	defer ownershipStockCleanup(t, c)()
	// The shared SQL phase retains committed procedure setup across ROLLBACK.
	// All other member definitions/membership setup were transactional.
	if tc.name != "procedure" {
		for _, q := range tc.setup {
			run(fixture.Exec(q, 0))
		}
	}
	var detachedSchemaMembers [][]any
	if tc.name == "schema" {
		detachedSchemaMembers = ownershipSnapshot(t, p, c, ownershipClassMemberSet)
	}
	run(fixture.Exec("ALTER EXTENSION unaccent ADD "+tc.target, 0))
	schema := "eo_old"
	if tc.class == 3602 || tc.class == 3601 || tc.name == "dictionary_snowball" {
		run(fixture.Exec("ALTER EXTENSION unaccent SET SCHEMA eo_new", 0))
		tc.target = strings.ReplaceAll(tc.target, "eo_old", "eo_new")
		tc.lookup = strings.ReplaceAll(tc.lookup, "eo_old", "eo_new")
		schema = "eo_new"
	}
	ownershipClassWitness(run, tc.name, schema)
	metadata := ownershipClassMetadata(t, p, c, tc, ownershipID(t, p, c, tc.lookup))
	members := ownershipSnapshot(t, p, c, ownershipClassMemberSet)
	typeOwners := ownershipSnapshot(t, p, c, ownershipClassTypeOwners)
	saved := fixture.ExtensionDump(t, p.Ctx, p.Native)
	if tc.name == "dictionary_snowball" && !strings.Contains(saved, `CREATE TEXT SEARCH DICTIONARY "eo_new"."item"`) {
		t.Fatal("ordinary Snowball member dictionary CREATE omitted from dump")
	}
	var parserRows []*sql.Rows
	if tc.class == 3601 {
		if !strings.Contains(saved, `CREATE TEXT SEARCH PARSER "eo_new"."item"`) {
			t.Fatal("ordinary member parser CREATE omitted from dump")
		}
		for range 2 {
			rows, err := c.QueryContext(p.Ctx, `SELECT * FROM ts_parse('eo_new.item','Word')`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			parserRows = append(parserRows, rows)
		}
	}
	var snowballRows []*sql.Rows
	if tc.name == "dictionary_snowball" {
		for range 2 {
			rows, err := c.QueryContext(p.Ctx, `SELECT ts_lexize('eo_new.item','RATS') AS value`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			snowballRows = append(snowballRows, rows)
		}
	}
	// Reset/Restore use raw control pools; every SQL assertion uses the
	// destination's recording connection and its matching recorder.
	restore := func(control, recorded *sql.DB, recorder *fixture.Recorder) {
		if err := stuntgres.Reset(p.Ctx, control); err != nil {
			t.Fatal("class control reset", tc.name, err)
		}
		if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(saved)); err != nil {
			t.Fatal("class membership replay", tc.name, err)
		}
		conn, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			defer ownershipStockCleanup(t, conn)()
			destination := b
			destination.Recorder = recorder
			r := ownershipRun(t, p, destination, conn)
			ownershipMemberCount(r, 6+tc.closure)
			id := ownershipID(t, p, conn, tc.lookup)
			r(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid=$1 AND objid=$2 AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='unaccent') AND deptype='e'`, 1, tc.class, id))
			ownershipClassMetadataEqual(t, metadata, ownershipClassMetadata(t, p, conn, tc, id))
			ownershipUnchanged(t, p, conn, ownershipClassMemberSet, members)
			ownershipUnchanged(t, p, conn, ownershipClassTypeOwners, typeOwners)
			if step, ok := ownershipClassMemberDrop(tc, schema); ok {
				r(step)
				if tc.class == 3601 {
					step.Query = strings.TrimSuffix(step.Query, " CASCADE")
					r(step)
				}
			}
			if tc.name == "sequence" {
				r(fixture.Select(`SELECT last_value,is_called FROM eo_old.item`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(40), true}}))
			}
			ownershipClassWitness(r, tc.name, schema)
			var heldSnowball *sql.Stmt
			const snowballSQL = `SELECT ts_lexize('eo_new.item'::regdictionary,$1::text) AS value`
			checkHeldSnowball := func() {
				if heldSnowball != nil {
					for _, tc := range []struct{ input, want any }{{"RATS", "{rat}"}, {"the", "{}"}, {nil, nil}} {
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: heldSnowball, SQL: snowballSQL}, recorder, fixture.Select(snowballSQL, []string{"value"}, []string{"_TEXT"}, [][]any{{tc.want}}, tc.input))
					}
				}
			}
			if tc.name == "dictionary_snowball" {
				var err error
				heldSnowball, err = conn.PrepareContext(p.Ctx, snowballSQL)
				if err != nil {
					t.Fatal(err)
				}
				defer heldSnowball.Close()
				checkHeldSnowball()
				if again := fixture.ExtensionDump(t, p.Ctx, control); again != saved {
					t.Fatal("Snowball dictionary replay changed dump bytes")
				}
			}
			if tc.class == 3601 {
				r(ownershipError("ALTER EXTENSION unaccent ADD "+tc.target, "55000", `text search parser `+schema+`.item is already a member of extension "unaccent"`))
				r(ownershipError("ALTER EXTENSION plpgsql DROP "+tc.target, "55000", `text search parser `+schema+`.item is not a member of extension "plpgsql"`))
				if again := fixture.ExtensionDump(t, p.Ctx, control); again != saved {
					t.Fatal("parser member replay changed dump bytes")
				}
			}
			if tc.class == 3601 || tc.name == "dictionary_snowball" {
				for _, invalid := range []string{strings.Replace(saved, "stuntgres-extension-owners-v3", "stuntgres-extension-owners-v999", 1), saved + "\nCREATE EXTENSION missing_provider;\n"} {
					if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(invalid)); err == nil {
						t.Fatal("invalid class member restore accepted", tc.name)
					}
					if after := fixture.ExtensionDump(t, p.Ctx, control); after != saved {
						t.Fatal("failed class member restore published partial state", tc.name)
					}
					ownershipClassWitness(r, tc.name, schema)
					ownershipClassMetadataEqual(t, metadata, ownershipClassMetadata(t, p, conn, tc, id))
					ownershipUnchanged(t, p, conn, ownershipClassMemberSet, members)
					checkHeldSnowball()
				}
				if tc.class == 3601 {
					ownershipParserOldRows(t, parserRows[0])
					parserRows = parserRows[1:]
				} else {
					ownershipSnowballOldRows(t, snowballRows[0])
					snowballRows = snowballRows[1:]
				}
			}
			r(fixture.Exec("ALTER EXTENSION unaccent DROP "+tc.target, 0))
			if tc.name == "schema" {
				ownershipUnchanged(t, p, conn, ownershipClassMemberSet, detachedSchemaMembers)
			}
			ownershipMemberCount(r, 6)
			r(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid=$1 AND objid=$2 AND refclassid='pg_extension'::regclass AND deptype='e'`, 0, tc.class, id))
			if ownershipID(t, p, conn, tc.lookup) != id {
				t.Fatal("replayed detach replaced identity", tc.name)
			}
			ownershipClassWitness(r, tc.name, schema)
			checkHeldSnowball()
			if tc.name == "dictionary_snowball" {
				ownershipClassMetadataEqual(t, metadata, ownershipClassMetadata(t, p, conn, tc, id))
				r(fixture.Exec("DROP TEXT SEARCH DICTIONARY "+schema+".item", 0))
			}
			if tc.class == 3601 {
				ownershipClassMetadataEqual(t, metadata, ownershipClassMetadata(t, p, conn, tc, id))
				r(ownershipParserRestrict(schema))
				r(ownershipError("ALTER EXTENSION unaccent DROP "+tc.target, "55000", `text search parser `+schema+`.item is not a member of extension "unaccent"`))
				r(fixture.Exec("ALTER EXTENSION unaccent ADD "+tc.target, 0))
				r(fixture.Exec("ALTER EXTENSION unaccent DROP "+tc.target, 0))
				r(fixture.Exec("DROP TEXT SEARCH PARSER "+schema+".item CASCADE", 0))
				r(ownershipCount(`SELECT count(*) FROM pg_ts_config WHERE cfgname='parser_config' AND cfgnamespace='eo_old'::regnamespace`, 0))
				r(ownershipCount(`SELECT count(*) FROM pg_proc WHERE oid IN (3717,3718,3719,3721)`, 4))
			}
		}()
	}
	restore(p.Native, b.DB, b.Recorder)
	connector, err := stuntgres.NewDriver().OpenConnector("eo_class_" + tc.name)
	if err != nil {
		t.Fatal(err)
	}
	recorded, recorder := fixture.RecordedNativePool(t, connector)
	other := sql.OpenDB(connector)
	defer other.Close()
	restore(other, recorded, recorder)
}

const ownershipClassMemberSet = `SELECT d.classid::bigint,pg_describe_object(d.classid,d.objid,0) AS identity FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.refclassid='pg_extension'::regclass AND e.extname='unaccent' AND d.deptype='e' ORDER BY d.classid,identity`
const ownershipClassTypeOwners = `SELECT pg_describe_object(d.classid,d.objid,0) AS identity,r.rolname,t.typacl::text FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid JOIN pg_type t ON t.oid=d.objid JOIN pg_roles r ON r.oid=t.typowner WHERE d.classid='pg_type'::regclass AND d.refclassid='pg_extension'::regclass AND e.extname='unaccent' AND d.deptype='e' ORDER BY identity`
