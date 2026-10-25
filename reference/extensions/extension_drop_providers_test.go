package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

type extensionDropAddress struct{ class, object int64 }

func extensionDropManifest(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) []extensionDropAddress {
	t.Helper()
	var ext int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_extension WHERE extname=$1", name).Scan(&ext); err != nil || ext <= 0 {
		t.Fatal("extension identity", ext, err)
	}
	rows, err := c.QueryContext(p.Ctx, "SELECT classid,objid FROM pg_depend WHERE refclassid=3079 AND refobjid=$1 AND deptype='e'", ext)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	addresses := []extensionDropAddress{{3079, ext}}
	seen := map[extensionDropAddress]bool{}
	for rows.Next() {
		var a extensionDropAddress
		if err := rows.Scan(&a.class, &a.object); err != nil {
			t.Fatal(err)
		}
		if a.class <= 0 || a.object <= 0 || seen[a] {
			t.Fatal("invalid manifest address", a)
		}
		seen[a] = true
		addresses = append(addresses, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(addresses) < 2 {
		t.Fatal("empty installed manifest", name)
	}
	return addresses
}

func extensionDropManifestGone(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, addresses []extensionDropAddress) {
	t.Helper()
	for _, a := range addresses {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pg_depend WHERE (classid=$1 AND objid=$2) OR (refclassid=$1 AND refobjid=$2)", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, a.class, a.object))
		table := map[int64]string{3079: "pg_extension", 1255: "pg_proc", 1247: "pg_type", 3600: "pg_ts_dict", 3764: "pg_ts_template"}[a.class]
		if table != "" {
			fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM "+table+" WHERE oid=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, a.object))
		}
	}
}

func TestReferenceExtensionDropProviders(t *testing.T) {
	for _, kind := range []string{"intarray", "intagg", "fuzzystrmatch"} {
		t.Run(kind, func(t *testing.T) {
			extensionDropCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				var notices []fixture.Diagnostic
				_, owner, recorder := fixture.NoticeReferencePool(t, p, pg, &notices)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, owner, recorder, s) }
				run(fixture.Exec("CREATE EXTENSION "+kind, 0))
				addresses := extensionDropManifest(t, p, owner, kind)
				switch kind {
				case "intarray":
					var typ, array, input, output int64
					if err := owner.QueryRowContext(p.Ctx, "SELECT oid,typarray,typinput::oid,typoutput::oid FROM pg_type WHERE typname='query_int' AND typnamespace=(SELECT oid FROM pg_namespace WHERE nspname='public')").Scan(&typ, &array, &input, &output); err != nil {
						t.Fatal(err)
					}
					for _, a := range []extensionDropAddress{{1247, typ}, {1247, array}, {1255, input}, {1255, output}} {
						if a.object <= 0 {
							t.Fatal("installed type address", a)
						}
						addresses = append(addresses, a)
					}
					run(fixture.Exec("CREATE TABLE ext_drop_typed(id int,q query_int)", 0))
					run(fixture.Exec("INSERT INTO ext_drop_typed VALUES(1,NULL)", 1))
					run(fixture.ImplicitError("DROP EXTENSION intarray RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop extension intarray because other objects depend on it", Detail: "column q of table ext_drop_typed depends on type query_int", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
					typeName := "QUERY_INT"
					if pg {
						typeName = "" // pq has no registered name for this installed type OID.
					}
					run(fixture.Select("SELECT id,q FROM ext_drop_typed", []string{"id", "q"}, []string{"INT4", typeName}, [][]any{{int64(1), nil}}))
					run(fixture.Exec("DROP EXTENSION intarray CASCADE", 0))
					extensionDropNotices(t, notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to column q of table ext_drop_typed"}})
					run(fixture.Select("SELECT id FROM ext_drop_typed", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
					run(fixture.Exec("INSERT INTO ext_drop_typed VALUES(2)", 1))
				case "intagg":
					var aggregate int64
					if err := owner.QueryRowContext(p.Ctx, "SELECT 'int_array_aggregate(integer)'::regprocedure::oid").Scan(&aggregate); err != nil {
						t.Fatal(err)
					}
					run(fixture.Select("SELECT count(*) FROM pg_aggregate WHERE aggfnoid::oid=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, aggregate))
					run(fixture.Prepared(fixture.Exec("DROP EXTENSION intagg RESTRICT", 0)))
					run(fixture.Select("SELECT count(*) FROM pg_aggregate WHERE aggfnoid::oid=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, aggregate))
					extensionDropNotices(t, notices, nil)
				case "fuzzystrmatch":
					run(fixture.Exec("CREATE VIEW ext_drop_distance AS SELECT levenshtein_less_equal('kitten','sitting',1,1,1,3) AS v", 0))
					run(fixture.ImplicitError("DROP EXTENSION fuzzystrmatch", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop extension fuzzystrmatch because other objects depend on it", Detail: "view ext_drop_distance depends on function levenshtein_less_equal(text,text,integer,integer,integer,integer)", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
					run(fixture.Select("SELECT v FROM ext_drop_distance", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}))
					run(fixture.Exec("DROP EXTENSION fuzzystrmatch CASCADE", 0))
					extensionDropNotices(t, notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to view ext_drop_distance"}})
				}
				extensionDropManifestGone(t, p, c, r, addresses)
			})
		})
	}
}
