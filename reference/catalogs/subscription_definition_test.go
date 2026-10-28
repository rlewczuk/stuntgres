package reference_test

import (
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func subscriptionScenario(t *testing.T, names ...string) *fixture.Pair {
	t.Helper()
	clean := fixture.SubscriptionReferenceCleanup(t, names)
	p := fixture.NewScenario(t)
	clean()
	return p
}
func subscriptionRun(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, step fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, step)
}
func subscriptionExec(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, sql string, prepared bool) {
	t.Helper()
	step := fixture.Exec(sql, 0)
	step.Prepared = prepared
	subscriptionRun(t, p, b, e, step)
}
func subscriptionError(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query, code, message string, prepared bool) {
	t.Helper()
	step := fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic(code, message))
	step.Prepared = prepared
	step.ErrorStage = "execute"
	subscriptionRun(t, p, b, e, step)
}
func subscriptionNumber(t *testing.T, p *fixture.Pair, e fixture.Executor, query string) int64 {
	t.Helper()
	r, err := e.QueryContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var n int64
	if !r.Next() {
		t.Fatal("missing scalar", query, r.Err())
	}
	if err := r.Scan(&n); err != nil {
		t.Fatal(err)
	}
	if r.Next() || r.Err() != nil {
		t.Fatal("scalar cardinality", r.Err())
	}
	return n
}
func subscriptionList(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, name, publications string, enabled bool) {
	t.Helper()
	subscriptionRun(t, p, b, e, fixture.Select("SELECT subpublications,subenabled FROM pg_subscription WHERE subname='"+name+"'", []string{"subpublications", "subenabled"}, []string{"_TEXT", "BOOL"}, [][]any{{publications, enabled}}))
}
func subscriptionDetach(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, name string) {
	t.Helper()
	subscriptionExec(t, p, b, e, "ALTER SUBSCRIPTION "+name+" DISABLE", false)
	subscriptionExec(t, p, b, e, "ALTER SUBSCRIPTION "+name+" SET (slot_name=NONE)", false)
	subscriptionExec(t, p, b, e, "DROP SUBSCRIPTION "+name, false)
}
func TestReferenceSubscriptionDefinition(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := subscriptionScenario(t, "sub_definition", "QuotedSub")
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					connection := "host=publisher.invalid port=-1 application_name='sub definition'"
					subscriptionExec(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_definition CONNECTION 'host=publisher.invalid port=-1 application_name=''sub definition''' PUBLICATION remote_first,remote_second WITH (connect=false)", prepared)
					id := subscriptionNumber(t, p, b.DB, "SELECT oid::bigint FROM pg_subscription WHERE subname='sub_definition'")
					db := subscriptionNumber(t, p, b.DB, "SELECT oid::bigint FROM pg_database WHERE datname=current_database()")
					owner := subscriptionNumber(t, p, b.DB, "SELECT oid::bigint FROM pg_roles WHERE rolname=current_user")
					if id <= 0 || db <= 0 || owner <= 0 {
						t.Fatal("invalid subscription identity", id, db, owner)
					}
					step := subscriptionEmpty(b.PG, "SELECT * FROM pg_subscription WHERE subname='sub_definition'", false)
					step.Rows = [][]any{{id, db, "0/0", "sub_definition", owner, false, false, "p", "d", false, true, false, false, connection, "sub_definition", "off", "{remote_first,remote_second}", "any"}}
					fixture.OidReferenceRows(t, p.Ctx, b, b.DB, step)
					step.Prepared = true
					fixture.OidReferenceRows(t, p.Ctx, b, b.DB, step)
					subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_definition CONNECTION 'postgresql://user:secret@[::1]:-1/remote?application_name=a+b'", prepared)
					subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_definition SET (binary=on,streaming=off,synchronous_commit=remote_apply,origin=none,disable_on_error=true,run_as_owner=true,two_phase=true)", prepared)
					subscriptionRun(t, p, b, b.DB, fixture.Select("SELECT subbinary,substream,subtwophasestate,subdisableonerr,subrunasowner,subsynccommit,suborigin FROM pg_subscription WHERE subname='sub_definition'", []string{"subbinary", "substream", "subtwophasestate", "subdisableonerr", "subrunasowner", "subsynccommit", "suborigin"}, []string{"BOOL", "CHAR", "CHAR", "BOOL", "BOOL", "TEXT", "TEXT"}, [][]any{{true, "f", "p", true, true, "remote_apply", "none"}}))
					subscriptionDetach(t, p, b, b.DB, "sub_definition")
					subscriptionExec(t, p, b, b.DB, `CREATE SUBSCRIPTION "QuotedSub" CONNECTION '' PUBLICATION missing_local WITH (connect=false)`, prepared)
					subscriptionRun(t, p, b, b.DB, fixture.Select(`SELECT subslotname FROM pg_subscription WHERE subname='QuotedSub'`, []string{"subslotname"}, []string{"NAME"}, [][]any{{"QuotedSub"}}))
					subscriptionDetach(t, p, b, b.DB, `"QuotedSub"`)
				})
			}
		})
	}
}
func TestReferenceSubscriptionPublications(t *testing.T) {
	p := subscriptionScenario(t, "sub_publications")
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			subscriptionExec(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_publications CONNECTION '' PUBLICATION p1,p2 WITH (connect=false,slot_name=NONE)", false)
			for _, prepared := range []bool{false, true} {
				subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_publications SET PUBLICATION p3,p1 WITH (refresh=false)", prepared)
				subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_publications ADD PUBLICATION p4,p2 WITH (refresh=false,copy_data=true)", prepared)
				subscriptionList(t, p, b, b.DB, "sub_publications", "{p3,p1,p4,p2}", false)
				subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_publications DROP PUBLICATION p1,p4 WITH (refresh=false)", prepared)
				subscriptionList(t, p, b, b.DB, "sub_publications", "{p3,p2}", false)
				for _, tc := range []struct{ sql, code, message string }{
					{"SET PUBLICATION p1,p1 WITH (refresh=false)", "42710", `publication name "p1" used more than once`},
					{"ADD PUBLICATION p2 WITH (refresh=false)", "42710", `publication "p2" is already in subscription "sub_publications"`},
					{"DROP PUBLICATION absent WITH (refresh=false)", "42P17", `publication "absent" is not in subscription "sub_publications"`},
					{"DROP PUBLICATION p3,p2 WITH (refresh=false)", "42P17", "cannot drop all the publications from a subscription"},
				} {
					subscriptionError(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_publications "+tc.sql, tc.code, tc.message, prepared)
					subscriptionList(t, p, b, b.DB, "sub_publications", "{p3,p2}", false)
				}
			}
			subscriptionExec(t, p, b, b.DB, "DROP SUBSCRIPTION sub_publications", true)
		})
	}
}
func TestReferenceSubscriptionMetadataSmoke(t *testing.T) {
	p := subscriptionScenario(t, "sub_metadata_smoke")
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, noticeConn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			fixture.RunStep(t, p.Ctx, noticeConn, recorder, fixture.Exec("CREATE SUBSCRIPTION sub_metadata_smoke CONNECTION '' PUBLICATION remote_a,remote_b WITH (connect=false)", 0))
			warning := []fixture.Diagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: "subscription was created, but is not connected", Hint: "To initiate replication, you must manually create the replication slot, enable the subscription, and refresh the subscription."}}
			if !reflect.DeepEqual(notices, warning) {
				t.Fatal("metadata smoke warning", notices, warning)
			}
			id := subscriptionNumber(t, p, b.DB, "SELECT oid::bigint FROM pg_subscription WHERE subname='sub_metadata_smoke'")
			db := subscriptionNumber(t, p, b.DB, "SELECT oid::bigint FROM pg_database WHERE datname=current_database()")
			owner := subscriptionNumber(t, p, b.DB, "SELECT oid::bigint FROM pg_roles WHERE rolname=current_user")
			if id <= 0 || db <= 0 || owner <= 0 {
				t.Fatal("metadata smoke identity", id, db, owner)
			}
			defaults := subscriptionEmpty(b.PG, "SELECT * FROM pg_subscription WHERE subname='sub_metadata_smoke'", false)
			defaults.Rows = [][]any{{id, db, "0/0", "sub_metadata_smoke", owner, false, false, "p", "d", false, true, false, false, "", "sub_metadata_smoke", "off", "{remote_a,remote_b}", "any"}}
			fixture.OidReferenceRows(t, p.Ctx, b, b.DB, defaults)
			subscriptionList(t, p, b, b.DB, "sub_metadata_smoke", "{remote_a,remote_b}", false)
			tx, err := b.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			subscriptionExec(t, p, b, tx, "ALTER SUBSCRIPTION sub_metadata_smoke ADD PUBLICATION remote_c WITH (refresh=false)", true)
			subscriptionExec(t, p, b, tx, "ALTER SUBSCRIPTION sub_metadata_smoke ENABLE", true)
			subscriptionList(t, p, b, tx, "sub_metadata_smoke", "{remote_a,remote_b,remote_c}", true)
			subscriptionExec(t, p, b, tx, "ALTER SUBSCRIPTION sub_metadata_smoke DISABLE", true)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			subscriptionList(t, p, b, b.DB, "sub_metadata_smoke", "{remote_a,remote_b}", false)
			subscriptionError(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_metadata_smoke ADD PUBLICATION remote_b WITH (refresh=false)", "42710", `publication "remote_b" is already in subscription "sub_metadata_smoke"`, true)
			subscriptionList(t, p, b, b.DB, "sub_metadata_smoke", "{remote_a,remote_b}", false)
			subscriptionDetach(t, p, b, b.DB, "sub_metadata_smoke")
			subscriptionRun(t, p, b, b.DB, subscriptionEmpty(b.PG, "SELECT * FROM pg_subscription WHERE subname='sub_metadata_smoke'", false))
		})
	}
}

func TestReferenceSubscriptionObjectDescription(t *testing.T) {
	for _, name := range []string{"UpperSubscriptionDescription", "subscription description"} {
		t.Run(name, func(t *testing.T) {
			p := subscriptionScenario(t, name)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					quoted := `"` + name + `"`
					subscriptionExec(t, p, b, b.DB, "CREATE SUBSCRIPTION "+quoted+" CONNECTION '' PUBLICATION remote WITH (connect=false,slot_name=NONE)", false)
					for _, prepared := range []bool{false, true} {
						for _, step := range []fixture.Step{
							fixture.Select("SELECT pg_describe_object(6100::oid,oid,0) AS description FROM pg_subscription WHERE subname=$1", []string{"description"}, []string{"TEXT"}, [][]any{{"subscription " + name}}, name),
							fixture.Select("SELECT (pg_identify_object(6100::oid,oid,0)).identity AS identity FROM pg_subscription WHERE subname=$1", []string{"identity"}, []string{"TEXT"}, [][]any{{quoted}}, name),
							fixture.Select("SELECT g.classid::bigint AS class,g.objid=s.oid AS same,g.objsubid FROM pg_subscription s,LATERAL pg_identify_object_as_address(6100::oid,s.oid,0) a,LATERAL pg_get_object_address(a.type,a.object_names,a.object_args) g WHERE s.subname=$1", []string{"class", "same", "objsubid"}, []string{"INT8", "BOOL", "INT4"}, [][]any{{int64(6100), true, int64(0)}}, name),
						} {
							step.Prepared = prepared
							subscriptionRun(t, p, b, b.DB, step)
						}
					}
					subscriptionDetach(t, p, b, b.DB, quoted)
				})
			}
		})
	}
}
