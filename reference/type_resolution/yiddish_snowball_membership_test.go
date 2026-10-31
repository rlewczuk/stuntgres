package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func yiddishMembershipCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, attached bool) {
	t.Helper()
	var count int64
	if attached { count = 1 }
	for _, item := range []struct { class, object, receiver string }{
		{"pg_ts_dict", "'pg_catalog.yiddish_stem'::regdictionary", "unaccent"},
		{"pg_ts_config", "'pg_catalog.yiddish'::regconfig", "plpgsql"},
	} {
		// Both sides are class-qualified: dictionary/configuration OIDs may
		// collide across catalogs without creating a membership relationship.
		lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid='"+item.class+"'::regclass AND objid="+item.object+" AND refclassid='pg_extension'::regclass AND deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{count}}))
		lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid='"+item.class+"'::regclass AND d.objid="+item.object+" AND d.refclassid='pg_extension'::regclass AND d.deptype='e' AND e.extname=$1", []string{"n"}, []string{"INT8"}, [][]any{{count}}, item.receiver))
	}
	for _, q := range []string{
		"SELECT count(*) AS n FROM pg_depend WHERE classid='pg_ts_dict'::regclass AND objid='yiddish_stem'::regdictionary AND refclassid='pg_ts_template'::regclass AND refobjid=(SELECT dicttemplate FROM pg_ts_dict WHERE oid='yiddish_stem'::regdictionary) AND deptype='n'",
		"SELECT count(*) AS n FROM pg_depend WHERE classid='pg_ts_config'::regclass AND objid='yiddish'::regconfig AND refclassid='pg_ts_dict'::regclass AND refobjid='yiddish_stem'::regdictionary AND deptype='n'",
	} { lithuanianSmokeBoth(t, p, b, c, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})) }
	lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('yiddish_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{קינד}"}}, "קינדער"))
	lithuanianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('yiddish','ONLY קינדער RATS') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'only':1 'rats':3 'קינד':2"}}))
	regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='yiddish_stem'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"language = 'yiddish'"}})
}

// Detach real current receivers before extension teardown; never drop a stock
// object during cleanup, including when a replay or assertion fails midway.
func yiddishDetachMembership(ctx context.Context, c *sql.Conn) error {
	for _, item := range []struct { class, object, target string }{
		{"pg_ts_dict", "'pg_catalog.yiddish_stem'::regdictionary", "TEXT SEARCH DICTIONARY pg_catalog.yiddish_stem"},
		{"pg_ts_config", "'pg_catalog.yiddish'::regconfig", "TEXT SEARCH CONFIGURATION pg_catalog.yiddish"},
	} {
		var receiver string
		err := c.QueryRowContext(ctx, "SELECT e.extname FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid='"+item.class+"'::regclass AND d.objid="+item.object+" AND d.refclassid='pg_extension'::regclass AND d.deptype='e'").Scan(&receiver)
		if err == sql.ErrNoRows { continue }
		if err != nil { return err }
		if _, err := c.ExecContext(ctx, "ALTER EXTENSION "+receiver+" DROP "+item.target); err != nil { return err }
	}
	return nil
}

func yiddishMembershipControls(t *testing.T) {
	emitted := make(map[bool][]byte)
	var nativeMemberOwner string
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cleanupPair := *p
		original := lithuanianDefinitionSnapshot(t, &cleanupPair, b, c, "yiddish_stem", "yiddish")
		var bootstrapQueries []string
		var bootstrapRows [][][]any
		if b.PG {
			bootstrapQueries = []string{
				"SELECT oid::bigint,extowner::bigint,extnamespace::bigint,extversion FROM pg_extension WHERE extname='plpgsql'",
				"SELECT oid::bigint,lanowner::bigint,lanpltrusted,lanplcallfoid::bigint,laninline::bigint,lanvalidator::bigint FROM pg_language WHERE lanname='plpgsql'",
				"SELECT oid::bigint,proowner::bigint FROM pg_proc WHERE pronamespace=11 AND proname IN ('plpgsql_call_handler','plpgsql_inline_handler','plpgsql_validator') ORDER BY oid",
			}
			for _, q := range bootstrapQueries { bootstrapRows = append(bootstrapRows, fixture.OwnershipSnapshot(t, p, c, q)) }
		}
		if b.PG {
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second); defer cancel()
				for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "RESET ROLE"} {
					if _, err := c.ExecContext(ctx, q); err != nil { t.Error("restore replay session authority", err); return }
				}
				if err := yiddishDetachMembership(ctx, c); err != nil { t.Error("detach stock membership before cleanup", err); return }
				if _, err := c.ExecContext(ctx, "DROP EXTENSION IF EXISTS unaccent CASCADE"); err != nil { t.Error("drop membership receiver", err); return }
				cleanupPair.Ctx = ctx; original()
				for i, q := range bootstrapQueries { fixture.OwnershipUnchanged(t, &cleanupPair, c, q, bootstrapRows[i]) }
			}()
		}
		regtsExec(t, p, b, c, "CREATE EXTENSION unaccent", 0)
		attach := func() {
			regtsExec(t, p, b, c, "ALTER EXTENSION unaccent ADD TEXT SEARCH DICTIONARY pg_catalog.yiddish_stem", 0)
			regtsExec(t, p, b, c, "ALTER EXTENSION plpgsql ADD TEXT SEARCH CONFIGURATION pg_catalog.yiddish", 0)
		}
		attach()
		yiddishMembershipCheck(t, p, b, c, true)
		preserved := lithuanianDefinitionSnapshot(t, p, b, c, "yiddish_stem", "yiddish")
		for _, item := range []struct { kind, object, display, receiver string }{
			{"DICTIONARY", "yiddish_stem", "yiddish_stem", "unaccent"},
			{"CONFIGURATION", "yiddish", "yiddish", "plpgsql"},
		} {
			d := dictionaryDiagnostic("2BP01", "cannot drop text search "+map[string]string{"DICTIONARY": "dictionary", "CONFIGURATION": "configuration"}[item.kind]+" "+item.display+" because extension "+item.receiver+" requires it")
			d.Hint = "You can drop extension "+item.receiver+" instead."
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("DROP TEXT SEARCH "+item.kind+" pg_catalog."+item.object+" CASCADE", d)
				s.Prepared, s.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				preserved()
				yiddishMembershipCheck(t, p, b, c, true)
			}
		}
		if err := yiddishDetachMembership(p.Ctx, c); err != nil { t.Fatal(err) }
		yiddishMembershipCheck(t, p, b, c, false)
		attach()
		for _, attached := range []bool{true, false} {
			if !attached { if err := yiddishDetachMembership(p.Ctx, c); err != nil { t.Fatal(err) } }
			yiddishMembershipCheck(t, p, b, c, attached)
			if b.PG {
				stream, ok := emitted[attached]
				if !ok { t.Fatal("missing actual native membership stream", attached) }
				// Native bootstrap owner names resolve to a distinct PG role.
				// Rollback restores every actual PG bootstrap owner and member
				// association, not just the two Yiddish objects.
				regtsExec(t, p, b, c, "BEGIN", 0)
				if err := yiddishDetachMembership(p.Ctx, c); err != nil { t.Fatal(err) }
				regtsExec(t, p, b, c, "DROP EXTENSION unaccent", 0)
				if _, err := c.ExecContext(p.Ctx, string(stream)); err != nil { t.Fatal("actual membership replay", attached, err) }
				yiddishMembershipCheck(t, p, b, c, attached)
				if attached {
					regtsSelect(t, p, b, c, "SELECT cfgowner=$1::regrole::oid AS captured_owner FROM pg_ts_config WHERE oid='yiddish'::regconfig", []string{"captured_owner"}, []string{"BOOL"}, [][]any{{true}}, nativeMemberOwner)
				}
				regtsExec(t, p, b, c, "ROLLBACK", 0)
				regtsExec(t, p, b, c, "RESET SESSION AUTHORIZATION", 0)
				for i, q := range bootstrapQueries { fixture.OwnershipUnchanged(t, p, c, q, bootstrapRows[i]) }
				continue
			}
			var out bytes.Buffer
			if attached {
				if err := c.QueryRowContext(p.Ctx, "SELECT cfgowner::regrole::text FROM pg_ts_config WHERE oid='yiddish'::regconfig").Scan(&nativeMemberOwner); err != nil { t.Fatal(err) }
			}
			if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil { t.Fatal(err) }
			stream := bytes.Clone(out.Bytes()); emitted[attached] = stream
			for _, reset := range []bool{false, true} {
				if reset { if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) } }
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(stream)); err != nil { t.Fatal(err) }
				yiddishMembershipCheck(t, p, b, c, attached)
				var again bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(stream, again.Bytes()) { t.Fatal("membership deterministic redump", err) }
			}
			fresh := fixture.NewFixture(t, false, true)
			fc, err := fresh.Memory.Conn(fresh.Ctx)
			if err != nil { t.Fatal(err) }
			func() {
				defer fc.Close()
				if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(stream)); err != nil { t.Fatal(err) }
				yiddishMembershipCheck(t, fresh, fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}, fc, attached)
			}()
		}
	})
}
