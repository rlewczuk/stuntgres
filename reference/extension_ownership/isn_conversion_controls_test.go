package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func isnConversionControlSetup(run func(fixture.Step)) {
	for _, q := range []string{
		"CREATE EXTENSION isn",
		"CREATE TABLE isn_control(id integer NOT NULL,e ean13,issue issn)",
		"CREATE VIEW isn_control_view AS SELECT id,e::isbn::text AS book,e='9781234567897'::isbn13 AS equal,issue::ean13='9771234567898'::ean13 AS issue FROM isn_control",
		"CREATE TABLE isn_default(v isbn DEFAULT isbn('9780123456786'::ean13),CONSTRAINT isn_limit CHECK(v <= '9781234567897'::isbn13))",
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO isn_control VALUES(1,'9781234567897','9771234567003'),(2,'9781234567897!','9771234567898'),(3,NULL,NULL)", 3))
	run(fixture.Exec("INSERT INTO isn_default DEFAULT VALUES", 1))
}

func isnConversionControlView() fixture.Step {
	return fixture.Select("SELECT id,book,equal,issue FROM isn_control_view ORDER BY id", []string{"id", "book", "equal", "issue"}, []string{"INT4", "TEXT", "BOOL", "BOOL"}, [][]any{{int64(1), "1-234-56789-X", true, false}, {int64(2), "1-234-56789-X!", false, true}, {int64(3), nil, nil, nil}})
}

func isnConversionControlCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := isnConversionRun(t, p, b, c)
	isnConversionRoutes(t, p, b, c, isnConversionControlView())
	isnConversionRoutes(t, p, b, c, isnConversionIDs("SELECT id FROM isn_control ORDER BY e ASC NULLS LAST,id ASC", 1, 2, 3))
	isnConversionRoutes(t, p, b, c, fixture.Select("SELECT v::text AS book,v='9780123456786'::ean13 AS equal FROM isn_default", []string{"book", "equal"}, []string{"TEXT", "BOOL"}, [][]any{{"0-12-345678-9", true}}))
	for _, q := range []string{
		`SELECT count(*) FROM pg_depend d JOIN pg_attrdef a ON a.oid=d.objid WHERE d.classid='pg_attrdef'::regclass AND a.adrelid='isn_default'::regclass AND d.refclassid='pg_proc'::regclass AND d.refobjid='isbn(ean13)'::regprocedure AND d.deptype='n'`,
		`SELECT count(*) FROM pg_depend d JOIN pg_constraint c ON c.oid=d.objid WHERE d.classid='pg_constraint'::regclass AND c.conrelid='isn_default'::regclass AND c.conname='isn_limit' AND d.refclassid='pg_operator'::regclass AND d.refobjid='<=(isbn,isbn13)'::regoperator AND d.deptype='n'`,
		`SELECT count(*) FROM pg_depend d JOIN pg_rewrite r ON r.oid=d.objid WHERE d.classid='pg_rewrite'::regclass AND r.ev_class='isn_control_view'::regclass AND d.refclassid='pg_operator'::regclass AND d.refobjid='=(ean13,isbn13)'::regoperator AND d.deptype='n'`,
	} {
		run(fixture.OwnershipCount(q, 1))
	}
	run(fixture.Select(`SELECT c.opcintype='ean13'::regtype AS operand,f.opfname::text AS family,p.proname::text AS support,m.amprocnum::integer AS slot FROM pg_opclass c JOIN pg_am a ON a.oid=c.opcmethod JOIN pg_opfamily f ON f.oid=c.opcfamily JOIN pg_amproc m ON m.amprocfamily=f.oid AND m.amproclefttype=c.opcintype AND m.amprocrighttype=c.opcintype JOIN pg_proc p ON p.oid=m.amproc WHERE c.opcnamespace='public'::regnamespace AND c.opcname='ean13_ops' AND a.amname='btree'`, []string{"operand", "family", "support", "slot"}, []string{"BOOL", "TEXT", "TEXT", "INT4"}, [][]any{{true, "isn_ops", "btean13cmp", int64(1)}}))
}

func TestReferenceIsnConversionControls(t *testing.T) {
	isnConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := isnConversionRun(t, p, b, c)
		isnConversionControlSetup(run)
		held := fixture.IdentityHeld(t, p.Ctx, c, isnConversionControlView().Query)
		defer held.Statement.Close()
		isnConversionControlCheck(t, p, b, c)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, isnConversionControlView())
		// BOOL/TEXT output deliberately survives extension OID replacement;
		// retaining extension-typed prepared output would be a different contract.
		for _, q := range []string{"DROP VIEW isn_control_view", "DROP TABLE isn_default,isn_control", "DROP EXTENSION isn"} {
			run(fixture.Exec(q, 0))
		}
		isnConversionControlSetup(run)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, isnConversionControlView())
		isnConversionControlCheck(t, p, b, c)
		if b.PG {
			return
		}
		saved := fixture.ExtensionDump(t, p.Ctx, p.Native)
		rawQuery := "SELECT id,e,issue::ean13 AS issue FROM isn_control ORDER BY id"
		retainedWant := fixture.Ean13Select(b, rawQuery, []string{"id", "e", "issue"}, []string{"INT4", "EAN13", "EAN13"}, [][]any{{int64(1), "978-1-234-56789-7", "977-1234-567-00-3"}, {int64(2), "978-1-234-56789-7!", "977-1234-567-89-8"}, {int64(3), nil, nil}}, nil)
		// Capture both original resources and their actual raw descriptors before
		// either catalog is replaced; never reconstruct them by re-querying.
		oldRows := make([]*sql.Rows, 2)
		oldMetadata := make([][]*sql.ColumnType, 2)
		for i := range oldRows {
			var err error
			oldRows[i], err = c.QueryContext(p.Ctx, rawQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer oldRows[i].Close()
			oldMetadata[i], err = oldRows[i].ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			retainedWant.CheckMetadata(t, oldRows[i])
		}
		connector, err := stuntgres.NewDriver().OpenConnector("isn_conversion_destination")
		if err != nil {
			t.Fatal(err)
		}
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		other := sql.OpenDB(connector)
		defer other.Close()
		destination, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		destBackend := fixture.Backend{DB: recorded, Recorder: recorder}
		destRun := isnConversionRun(t, p, destBackend, destination)
		destRun(fixture.Exec("CREATE TABLE isn_allocation_spacer(v integer)", 0))
		destRun(fixture.Exec("CREATE EXTENSION isn", 0))
		for _, signature := range []string{"isbn(ean13)", "isneq(ean13,isbn13)", "btean13cmp(ean13,ean13)"} {
			q := "SELECT " + isnConversionLiteral(signature) + "::regprocedure::bigint"
			if fixture.OwnershipID(t, p, destination, q) == fixture.OwnershipID(t, p, c, q) {
				t.Fatal("independent destination signature was not displaced", signature)
			}
		}
		destination.Close()
		for i, target := range []struct {
			control, recorded *sql.DB
			recorder          *fixture.Recorder
		}{{p.Native, b.DB, b.Recorder}, {other, recorded, recorder}} {
			if err := stuntgres.Reset(p.Ctx, target.control); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, target.control, strings.NewReader(saved)); err != nil {
				t.Fatal(err)
			}
			conn, err := target.recorded.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer conn.Close()
				db := fixture.Backend{DB: target.recorded, Recorder: target.recorder}
				isnConversionControlCheck(t, p, db, conn)
				isnConversionAssertDump(t, saved, fixture.ExtensionDump(t, p.Ctx, target.control))
			}()
			metadata, err := oldRows[i].ColumnTypes()
			if err != nil || !reflect.DeepEqual(metadata, oldMetadata[i]) {
				t.Fatal("original reader descriptors changed", err)
			}
			fixture.RangeConsumeHeld(t, oldRows[i], retainedWant)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, isnConversionControlView())
		isnConversionReplay(t, p, saved, isnConversionControlCheck)
	})
	// Each replacement failure gets an independent fixture budget; never
	// accumulate eight expensive Restore operations on one scenario context.
	for _, destination := range []string{"same_driver", "independent_driver"} {
		for _, kind := range []string{"later_sql", "malformed", "canceled", "reader"} {
			t.Run(destination+"/"+kind, func(t *testing.T) {
				isnConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					if b.PG {
						return // Native replacement controls have no server equivalent.
					}
					isnConversionControlSetup(isnConversionRun(t, p, b, c))
					saved := fixture.ExtensionDump(t, p.Ctx, p.Native)
					control, recorded, recorder := p.Native, b.DB, b.Recorder
					if destination == "independent_driver" {
						connector, err := stuntgres.NewDriver().OpenConnector("isn_failure_" + kind)
						if err != nil {
							t.Fatal(err)
						}
						recorded, recorder = fixture.RecordedNativePool(t, connector)
						control = sql.OpenDB(connector)
						defer control.Close()
						fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Exec("CREATE TABLE isn_failure_spacer(v integer)", 0))
						fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Exec("CREATE EXTENSION isn", 0))
					}
					if err := stuntgres.Reset(p.Ctx, control); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(saved)); err != nil {
						t.Fatal(err)
					}
					conn, err := recorded.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					current := fixture.Backend{DB: recorded, Recorder: recorder}
					isnConversionRestoreFailure(t, p, current, conn, control, saved, kind)
				})
			})
		}
	}
}

func isnConversionRestoreFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, control *sql.DB, saved, kind string) {
	t.Helper()
	identities := `SELECT oid::bigint,proargtypes::text,prorettype::bigint FROM pg_proc WHERE pronamespace='public'::regnamespace ORDER BY oid`
	before := fixture.OwnershipSnapshot(t, p, c, identities)
	rows, err := c.QueryContext(p.Ctx, isnConversionControlView().Query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ctx := p.Ctx
	var reader io.Reader = strings.NewReader(saved + "\nINSERT INTO public.isn_default(v) VALUES ('9790123456785'::ean13::isbn);\n")
	switch kind {
	case "malformed":
		malformed := strings.Replace(saved, "stuntgres-extension-owners-v3", "stuntgres-extension-owners-v999", 1)
		if malformed == saved {
			t.Fatal("missing native owner manifest for malformed-control witness")
		}
		reader = strings.NewReader(malformed)
	case "canceled":
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		cancel()
		reader = strings.NewReader(saved)
	case "reader":
		reader = io.MultiReader(strings.NewReader(saved), fixture.TypeLifecycleErrorReader{Err: io.ErrUnexpectedEOF})
	}
	err = stuntgres.Restore(ctx, control, reader)
	if err == nil {
		t.Fatal("invalid Restore succeeded", kind)
	}
	if kind == "later_sql" {
		want := isnConversionDiagnostic("22P02", `cannot cast EAN13(ISMN) to ISBN for number: "9790123456785"`)
		fixture.AssertDiagnostic(t, err, &want)
	}
	if kind == "malformed" {
		var state interface{ SQLState() string }
		if errors.As(err, &state) || !strings.Contains(err.Error(), "invalid extension owner metadata") {
			t.Fatal("malformed owner metadata must be an ordinary control error", err)
		}
	}
	if kind == "canceled" && !errors.Is(err, context.Canceled) || kind == "reader" && !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("Restore lost ordinary control error", kind, err)
	}
	fixture.OwnershipUnchanged(t, p, c, identities, before)
	isnConversionControlCheck(t, p, b, c)
	fixture.RangeConsumeHeld(t, rows, isnConversionControlView())
	if fixture.ExtensionDump(t, p.Ctx, control) != saved {
		t.Fatal("failed Restore changed incumbent dump", kind)
	}
}

func isnConversionReplay(t *testing.T, p *fixture.Pair, saved string, check func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	if p.Postgres == nil {
		return
	}
	c, err := p.Postgres.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	clean := "DROP SCHEMA public CASCADE; CREATE SCHEMA public"
	if _, err := c.ExecContext(p.Ctx, clean); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
		defer cancel()
		if _, err := c.ExecContext(ctx, "RESET SESSION AUTHORIZATION; RESET ROLE; "+clean); err != nil {
			t.Error("dump replay cleanup", err)
		}
	}()
	if _, err := c.ExecContext(p.Ctx, saved); err != nil {
		t.Fatal("actual emitted ISN Dump PostgreSQL replay", err)
	}
	if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION; RESET ROLE"); err != nil {
		t.Fatal(err)
	}
	check(t, p, fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}, c)
}
