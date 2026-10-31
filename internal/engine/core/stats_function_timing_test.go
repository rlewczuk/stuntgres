package core_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/statistics"
)

// pg_sleep is an actual builtin body operation. Inside synctest it advances the
// standard library's virtual clock, not an injected statistics clock. All SQL
// parsing, admission, nested routine execution and terminal publication are real.
func TestStatsSnapshotFunctionTiming(t *testing.T) {
	type expected struct { calls int64; total, self time.Duration }
	ms := time.Millisecond
	cases := []struct {
		name, mode string
		definitions []string
		calls []string
		want map[string]expected
	}{
		{
			name: "same_key", mode: "pl",
			definitions: []string{"CREATE FUNCTION stats_f(n integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN IF n=0 THEN PERFORM pg_sleep(0.002); PERFORM stats_f(1); PERFORM pg_sleep(0.003); ELSE PERFORM pg_sleep(0.005); END IF; RETURN 7; END$$"},
			calls: []string{"SELECT stats_f(0)"}, want: map[string]expected{"stats_f":{2,10*ms,10*ms}},
		},
		{
			name: "distinct", mode: "pl",
			definitions: []string{
				"CREATE FUNCTION stats_g() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM pg_sleep(0.005); RETURN 7; END$$",
				"CREATE FUNCTION stats_f() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM pg_sleep(0.002); PERFORM stats_g(); PERFORM pg_sleep(0.003); RETURN 7; END$$",
			}, calls: []string{"SELECT stats_f()"}, want: map[string]expected{"stats_f":{1,10*ms,5*ms},"stats_g":{1,5*ms,5*ms}},
		},
		{
			name: "pending_prefix", mode: "pl",
			definitions: []string{"CREATE FUNCTION stats_f(n integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN IF n<0 THEN PERFORM pg_sleep(0.004); ELSIF n=0 THEN PERFORM pg_sleep(0.002); PERFORM stats_f(1); PERFORM pg_sleep(0.003); ELSE PERFORM pg_sleep(0.005); END IF; RETURN 7; END$$"},
			// One explicit transaction keeps the successful nonoverlapping prefix
			// in the same pending writer batch as the recursive invocation.
			calls: []string{"BEGIN","SELECT stats_f(-1)","SELECT stats_f(0)","COMMIT"}, want: map[string]expected{"stats_f":{3,14*ms,14*ms}},
		},
		{
			name: "published_prefix", mode: "pl",
			definitions: []string{"CREATE FUNCTION stats_f(n integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN IF n<0 THEN PERFORM pg_sleep(0.004); ELSIF n=0 THEN PERFORM pg_sleep(0.002); PERFORM stats_f(1); PERFORM pg_sleep(0.003); ELSE PERFORM pg_sleep(0.005); END IF; RETURN 7; END$$"},
			calls: []string{"SELECT stats_f(-1)","SELECT stats_f(0)"}, want: map[string]expected{"stats_f":{3,14*ms,14*ms}},
		},
		{
			name: "untracked_child", mode: "pl",
			definitions: []string{
				"CREATE FUNCTION stats_g() RETURNS integer LANGUAGE sql VOLATILE AS 'SELECT pg_sleep(0.005); SELECT 7'",
				"CREATE FUNCTION stats_f() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM pg_sleep(0.002); PERFORM stats_g(); PERFORM pg_sleep(0.003); RETURN 7; END$$",
			}, calls: []string{"SELECT stats_f()"}, want: map[string]expected{"stats_f":{1,10*ms,10*ms},"stats_g":{}},
		},
		{
			name: "untracked_parent", mode: "pl",
			definitions: []string{
				"CREATE FUNCTION stats_g() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM pg_sleep(0.005); RETURN 7; END$$",
				"CREATE FUNCTION stats_f() RETURNS integer LANGUAGE sql VOLATILE AS 'SELECT pg_sleep(0.002); SELECT stats_g(); SELECT pg_sleep(0.003); SELECT 7'",
			}, calls: []string{"SELECT stats_f()"}, want: map[string]expected{"stats_f":{},"stats_g":{1,5*ms,5*ms}},
		},
		{
			name: "untracked_intermediate", mode: "all",
			definitions: []string{
				"CREATE FUNCTION stats_h() RETURNS integer LANGUAGE plpgsql SET track_functions=pl AS $$BEGIN PERFORM pg_sleep(0.005); RETURN 7; END$$",
				"CREATE FUNCTION stats_g() RETURNS integer LANGUAGE plpgsql SET track_functions=none AS $$BEGIN PERFORM stats_h(); RETURN 7; END$$",
				"CREATE FUNCTION stats_f() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM pg_sleep(0.002); PERFORM stats_g(); PERFORM pg_sleep(0.003); RETURN 7; END$$",
			}, calls: []string{"SELECT stats_f()"}, want: map[string]expected{"stats_f":{1,10*ms,5*ms},"stats_g":{},"stats_h":{1,5*ms,5*ms}},
		},
		{
			name: "trigger_charged_to_caller", mode: "pl",
			definitions: []string{
				"CREATE TABLE stats_timed(id integer)",
				"CREATE FUNCTION stats_trigger() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM pg_sleep(0.005); RETURN NEW; END$$",
				"CREATE TRIGGER a BEFORE INSERT ON stats_timed FOR EACH ROW EXECUTE FUNCTION stats_trigger()",
				"CREATE FUNCTION stats_f() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM pg_sleep(0.002); INSERT INTO stats_timed VALUES(1); PERFORM pg_sleep(0.003); RETURN 7; END$$",
			}, calls: []string{"SELECT stats_f()"}, want: map[string]expected{"stats_f":{1,10*ms,5*ms},"stats_trigger":{1,5*ms,5*ms}},
		},
		{
			name: "caught_failed_child", mode: "pl",
			definitions: []string{
				"CREATE FUNCTION stats_h() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM pg_sleep(0.003); RETURN 7; END$$",
				"CREATE FUNCTION stats_g() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM pg_sleep(0.001); PERFORM stats_h(); RAISE EXCEPTION 'failed child'; END$$",
				"CREATE FUNCTION stats_f() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM pg_sleep(0.001); BEGIN PERFORM stats_g(); EXCEPTION WHEN OTHERS THEN PERFORM 1; END; PERFORM pg_sleep(0.002); RETURN 7; END$$",
			}, calls: []string{"SELECT stats_f()"}, want: map[string]expected{"stats_f":{1,7*ms,4*ms},"stats_g":{},"stats_h":{1,3*ms,3*ms}},
		},
	}
	for _, mode := range []string{"pl","all"} {
		fself, gcalls := 12*ms, int64(0)
		gtotal, gself := time.Duration(0), time.Duration(0)
		if mode=="all" { fself,gcalls,gtotal,gself=7*ms,1,8*ms,5*ms }
		cases=append(cases,struct{name,mode string; definitions,calls []string; want map[string]expected}{
			name:"mutual_"+mode,mode:mode,
			definitions:[]string{
				"CREATE FUNCTION stats_f(n integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN IF n=0 THEN PERFORM pg_sleep(0.002); PERFORM stats_g(); PERFORM pg_sleep(0.002); ELSE PERFORM pg_sleep(0.003); END IF; RETURN 7; END$$",
				"CREATE FUNCTION stats_g() RETURNS integer LANGUAGE sql VOLATILE AS 'SELECT pg_sleep(0.002); SELECT stats_f(1); SELECT pg_sleep(0.003); SELECT 7'",
			}, calls:[]string{"SELECT stats_f(0)"},want:map[string]expected{"stats_f":{2,12*ms,fself},"stats_g":{gcalls,gtotal,gself}},
		})
	}
	for _, tc := range cases {
		t.Run(tc.name,func(t *testing.T){ synctest.Test(t,func(t *testing.T){
			e:=engine.New(); s:=enginetest.StartSession(t,e,nil)
			statsFunctionExec(t,s,"SET track_functions='"+tc.mode+"'")
			statsFunctionExec(t,s,tc.definitions...)
			keys:=map[string]statistics.Key{}
			for name:=range tc.want { keys[name]=statsFunctionKey(t,e,name) }
			statsFunctionExec(t,s,tc.calls...)
			statsFunctionRetire(t,s)
			for name,want:=range tc.want {
				f:=e.Statistics.Read(keys[name])
				if f.Calls!=want.calls || f.TotalTime!=want.total || f.SelfTime!=want.self { t.Fatalf("%s got calls/total/self %d/%s/%s, want %d/%s/%s",name,f.Calls,f.TotalTime,f.SelfTime,want.calls,want.total,want.self) }
			}
		}) })
	}
	for _, cancelBody:=range []bool{false,true} {
		name:="failed_outer"; if cancelBody { name="canceled_outer" }
		t.Run(name,func(t *testing.T){synctest.Test(t,func(t *testing.T){
			e:=engine.New();s:=enginetest.StartSession(t,e,nil)
			ending:="RAISE EXCEPTION 'failed outer';";if cancelBody {ending="PERFORM pg_sleep(0.100);"}
			statsFunctionExec(t,s,"SET track_functions=pl",
				"CREATE FUNCTION stats_f(n integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN IF n=0 THEN PERFORM pg_sleep(0.002); PERFORM stats_f(1); "+ending+" ELSE PERFORM pg_sleep(0.005); END IF; RETURN 7; END$$",
				"CREATE FUNCTION stats_next() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM pg_sleep(0.003); RETURN 7; END$$")
			fkey,nextkey:=statsFunctionKey(t,e,"stats_f"),statsFunctionKey(t,e,"stats_next")
			ctx,cancel:=context.WithCancel(context.Background());defer cancel()
			op,err:=s.Acquire(ctx);if err!=nil{t.Fatal(err)}
			var joined chan struct{}
			if cancelBody { joined=make(chan struct{});go func(){defer close(joined);time.Sleep(8*time.Millisecond);cancel()}() }
			r,err:=e.Execute(op,enginetest.Statement(t,"SELECT stats_f(0)"),0,nil)
			if r.Rows!=nil { r.Rows.Close() }
			op.Done()
			if joined!=nil {<-joined}
			if err==nil || cancelBody && !errors.Is(err,context.Canceled) {t.Fatal("outer failure identity",err)}
			statsFunctionExec(t,s,"SELECT stats_next()")
			statsFunctionRetire(t,s)
			if f:=e.Statistics.Read(fkey);f.Calls!=1||f.TotalTime!=5*ms||f.SelfTime!=5*ms{t.Fatal("successful recursive descendant lost or failed outer counted",f)}
			if f:=e.Statistics.Read(nextkey);f.Calls!=1||f.TotalTime!=3*ms||f.SelfTime!=3*ms{t.Fatal("failed invocation leaked baseline",f)}
		})})
	}
}
