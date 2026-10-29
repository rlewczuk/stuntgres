package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEventTriggerRewriteReasons(t *testing.T) {
	for _, tc := range []struct {
		name string
		setup []string
		action string
		reason int64
		empty bool
	}{
		{name:"unlogged", action:"SET UNLOGGED", reason:1},
		{name:"logged", setup:[]string{"ALTER TABLE et_rw_t SET UNLOGGED"}, action:"SET LOGGED", reason:1},
		{name:"same_logged", action:"SET LOGGED"},
		{name:"volatile_default", action:"ADD extra bigint DEFAULT nextval('et_rw_seq')", reason:2},
		{name:"domain_default", setup:[]string{"CREATE DOMAIN et_rw_positive AS integer CHECK(VALUE>0) DEFAULT 1"}, action:"ADD extra et_rw_positive", reason:2},
		{name:"stored_expression", setup:[]string{"DROP TABLE et_rw_t","CREATE TABLE et_rw_t(v integer,extra integer GENERATED ALWAYS AS(v+1) STORED)"}, action:"ALTER extra SET EXPRESSION AS(v+2)", reason:2},
		{name:"constant_default", action:"ADD extra integer DEFAULT 5"},
		{name:"stable_default", action:"ADD extra text DEFAULT current_user"},
		{name:"plain_add", action:"ADD extra integer"},
		{name:"set_default", action:"ALTER v SET DEFAULT 5"},
		{name:"drop_default", setup:[]string{"ALTER TABLE et_rw_t ALTER v SET DEFAULT 5"}, action:"ALTER v DROP DEFAULT"},
		{name:"type", action:"ALTER v TYPE bigint", reason:4},
		{name:"empty_type", action:"ALTER v TYPE bigint", reason:4, empty:true},
		{name:"computed_identity", action:"ALTER v TYPE integer USING v+0", reason:4},
		{name:"identity", action:"ALTER v TYPE integer USING v"},
		{name:"method", action:"SET ACCESS METHOD et_rw_heap", reason:8},
		{name:"same_method", setup:[]string{"ALTER TABLE et_rw_t SET ACCESS METHOD et_rw_heap"}, action:"SET ACCESS METHOD et_rw_heap"},
		{name:"default_type", action:"ADD extra bigint DEFAULT nextval('et_rw_seq'), ALTER v TYPE bigint", reason:6},
		{name:"all", action:"SET UNLOGGED, ADD extra bigint DEFAULT nextval('et_rw_seq'), ALTER v TYPE bigint, SET ACCESS METHOD et_rw_heap", reason:15},
		{name:"multiple", action:"ADD extra bigint DEFAULT nextval('et_rw_seq'), ADD other bigint DEFAULT nextval('et_rw_seq'), ALTER v TYPE bigint", reason:6},
		{name:"multiple_types", setup:[]string{"ALTER TABLE et_rw_t ADD other integer"}, action:"ALTER v TYPE bigint, ALTER other TYPE bigint", reason:4},
		{name:"rename", action:"RENAME COLUMN v TO renamed"},
		{name:"check_scan", action:"ADD CHECK(v>0)"},
		{name:"not_null_scan", action:"ALTER v SET NOT NULL"},
		{name:"storage", action:"ALTER v SET STORAGE PLAIN"},
		{name:"compression", setup:[]string{"ALTER TABLE et_rw_t ADD extra text"}, action:"ALTER extra SET COMPRESSION pglz"},
		{name:"tablespace", action:"SET TABLESPACE pg_default"},
	} {
		for _, prepared := range []bool{false,true} {
			route := "direct"; if prepared { route = "prepared" }
			t.Run(tc.name+"/"+route, func(t *testing.T) {
				rewriteScenario(t, func(d rewriteCase) {
					// Access methods are database-global and must retire before the
					// fixture drops public, including on assertion failure.
					defer func() { if _,err := d.c.ExecContext(context.Background(), "DROP ACCESS METHOD IF EXISTS et_rw_heap CASCADE"); err != nil { t.Error(err) } }()
					d.exec("CREATE TABLE et_rw_t(v integer)", "CREATE SEQUENCE et_rw_seq START 1", "CREATE ACCESS METHOD et_rw_heap TYPE TABLE HANDLER heap_tableam_handler")
					d.exec(tc.setup...)
					if !tc.empty {
						insert := "INSERT INTO et_rw_t(v) VALUES(7)"
						d.run(fixture.Exec(insert,1))
					}
					d.setup()
					d.alter("ALTER TABLE et_rw_t "+tc.action,prepared)
					d.audit("et_rw_t",tc.reason,"ALTER TABLE")
					if tc.reason==6 || tc.reason==15 {
						d.run(fixture.Select("SELECT v,extra FROM et_rw_t",[]string{"v","extra"},[]string{"INT8","INT8"},[][]any{{int64(7),int64(1)}}))
					}
					if tc.name=="stored_expression" { d.run(fixture.Select("SELECT extra FROM et_rw_t",[]string{"extra"},[]string{"INT4"},[][]any{{int64(9)}})) }
				})
			})
		}
	}
	t.Run("typed_tables",func(t *testing.T) {
		for _, prepared := range []bool{false,true} {
			t.Run(map[bool]string{false:"direct",true:"prepared"}[prepared],func(t *testing.T) {
				rewriteScenario(t,func(d rewriteCase) {
					d.exec("CREATE TYPE et_rw_type AS(v integer)","CREATE TABLE et_rw_a OF et_rw_type","CREATE TABLE et_rw_b OF et_rw_type")
					d.run(fixture.Exec("INSERT INTO et_rw_a VALUES(7)",1)); d.run(fixture.Exec("INSERT INTO et_rw_b VALUES(9)",1))
					d.setup()
					d.alter("ALTER TYPE et_rw_type ALTER ATTRIBUTE v TYPE integer CASCADE",prepared)
					d.audit("et_rw_a",0,"ALTER TYPE")
					d.alter("ALTER TYPE et_rw_type ALTER ATTRIBUTE v TYPE bigint CASCADE",prepared)
					s:=fixture.Select("SELECT target::regclass::text AS target,reason,tag,same_oid,same_reason FROM et_rw_audit",[]string{"target","reason","tag","same_oid","same_reason"},[]string{"TEXT","INT4","TEXT","BOOL","BOOL"},[][]any{{"et_rw_a",int64(4),"ALTER TYPE",true,true},{"et_rw_b",int64(4),"ALTER TYPE",true,true}})
					s.Bag=true; d.run(s)
				})
			})
		}
	})
	t.Run("partitioned_root",func(t *testing.T) {
		rewriteScenario(t,func(d rewriteCase) {
			defer d.c.ExecContext(context.Background(),"DROP ACCESS METHOD IF EXISTS et_rw_heap CASCADE")
			d.exec("CREATE ACCESS METHOD et_rw_heap TYPE TABLE HANDLER heap_tableam_handler","CREATE TABLE et_rw_t(v integer) PARTITION BY RANGE(v)")
			d.setup(); d.exec("ALTER TABLE et_rw_t SET ACCESS METHOD et_rw_heap"); d.audit("et_rw_t",0,"ALTER TABLE")
		})
	})
	t.Run("maintenance",func(t *testing.T) {
		rewriteScenario(t,func(d rewriteCase) {
			d.exec("CREATE TABLE et_rw_t(v integer)","CREATE INDEX et_rw_idx ON et_rw_t(v)")
			d.run(fixture.Exec("INSERT INTO et_rw_t VALUES(7)",1)); d.setup()
			d.exec("ALTER TABLE et_rw_t CLUSTER ON et_rw_idx")
			// Standalone CLUSTER retains its existing admission boundary;
			// rewrite inspection must not turn it into a successful native no-op.
			if d.b.PG {
				d.exec("CLUSTER et_rw_t")
			} else {
				want:=fixture.ControlsReferenceDiagnostic("0A000","statement is unsupported")
				want.Position=fixture.Position(1)
				step:=fixture.ErrorDiagnostic("CLUSTER et_rw_t",want)
				step.ErrorStage="prepare"
				d.run(fixture.Prepared(step))
			}
			d.exec("VACUUM et_rw_t","REINDEX INDEX et_rw_idx")
			d.audit("et_rw_t",0,"ALTER TABLE")
			d.run(fixture.Select("SELECT v FROM et_rw_t",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
		})
	})
}
