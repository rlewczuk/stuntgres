package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/util"
)

func clusterProgressAdmittedTargets(t *testing.T) {
	for _,boundary := range []string{"cluster.publish","cluster.database.next"} {
		for _,cancel := range []bool{false,true} {
			t.Run(boundary+map[bool]string{false:"/success",true:"/cancel"}[cancel],func(t *testing.T){
				e := engine.New()
				owner := enginetest.StartSession(t,e,nil)
				worker := enginetest.StartSession(t,e,nil)
				observer := enginetest.TransactionOperation(t,enginetest.StartSession(t,e,nil))
				defer observer.Done()
				for _,q := range []string{
					"CREATE TABLE cp_first(id integer)","CREATE INDEX cp_first_ix ON cp_first(id)","ALTER TABLE cp_first CLUSTER ON cp_first_ix",
					"CREATE TABLE cp_second(id integer)","CREATE INDEX cp_second_ix ON cp_second(id)","ALTER TABLE cp_second CLUSTER ON cp_second_ix",
				} { enginetest.SessionSQL(t,owner,q) }
				pause := util.NewExecutionPause(boundary)
				defer pause.Resume()
				ctx,stop := context.WithTimeout(util.WithExecutionPause(context.Background(),pause),enginetest.Liveness)
				defer stop()
				join := enginetest.ExecuteAsync(t,e,worker,ctx,"CLUSTER")
				if err := pause.Wait(ctx); err!=nil { t.Fatal(err) }
				facts := clusterProgressFacts(t,e,observer)
				if boundary=="cluster.database.next" {
					if len(facts)!=0 { t.Fatal("retired target survived before next admission",facts) }
				} else {
					if len(facts)!=1 || facts[0].Command!="CLUSTER" || facts[0].Phase!="initializing" || facts[0].Indexes!=0 || facts[0].Scanned!=0 || facts[0].Written!=0 { t.Fatal("metadata-only work invented rebuilding",facts) }
				}
				if cancel { stop() }
				pause.Resume()
				_,err := join()
				if cancel { if !errors.Is(err,context.Canceled) { t.Fatal(err) } } else if err!=nil { t.Fatal(err) }
				if facts := clusterProgressFacts(t,e,observer); len(facts)!=0 { t.Fatal("target lifetime leaked",facts) }
			})
		}
	}
}
