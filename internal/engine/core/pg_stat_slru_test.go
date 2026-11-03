package core_test

import (
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func slruOwnerRows(t *testing.T, result core.Result, at time.Time, counters [7]int64) {
	t.Helper()
	if result.Rows == nil { t.Fatal("missing SLRU capture") }
	defer result.Rows.Close()
	if len(result.Rows.Schema()) != 9 { t.Fatal("nine descriptors") }
	values := make([]sqltype.Value, 9)
	seen := make(map[string]bool)
	for {
		present, err := result.Rows.Next(t.Context(), values)
		if err != nil { t.Fatal(err) }; if !present { break }
		name, err := values[0].Text()
		if err != nil || seen[name] || statistics.SLRUTarget(name).Name() != name { t.Fatal("exact unique name", name, err) }
		seen[name] = true
		for i, want := range counters {
			got, err := values[i+1].Integer()
			if err != nil || got != want || values[i+1].Type().ID() != sqltype.Int8 { t.Fatal("seven exact projected positions", i, got, want, err) }
		}
		got, err := values[8].TimestampTime()
		if err != nil || !got.Equal(at) { t.Fatal("captured reset", got, at, err) }
	}
	if len(seen) != 8 { t.Fatal("eight rows", seen) }
}

func TestPGStatSLRUInitialization(t *testing.T) {
	lower := time.Now().UTC().Truncate(time.Microsecond)
	e := engine.New()
	upper := time.Now().UTC()
	family := e.Statistics.ReadSLRUs()
	for _, got := range family {
		if got.ResetAt.Before(lower) || got.ResetAt.After(upper) || got.ResetAt.Location() != time.UTC || got.ResetAt.Nanosecond()%1000 != 0 || got != (statistics.SLRUFact{ResetAt: family[0].ResetAt}) { t.Fatal("genuine startup without physical activity", got, lower, upper) }
	}
	for k := statistics.SharedKind(0); k < statistics.SharedKindCount; k++ {
		if k == statistics.Archiver || k == statistics.BGWriter {
			started := time.UnixMicro(e.Cluster.StartedAt + 946684800000000).UTC()
			if got := e.Statistics.ReadShared(k); got != (statistics.SharedFact{ResetAt: started}) {
				t.Fatal("cluster family startup must contain only genuine reset metadata", k, got, started)
			}
			continue
		}
		if k != statistics.SLRU && k != statistics.RecoveryPrefetch && e.Statistics.ReadShared(k) != (statistics.SharedFact{}) { t.Fatal("unrelated family initialized", k) }
	}
	if e.Statistics.Read(statistics.Key{}) != (statistics.Fact{}) { t.Fatal("unrelated defaults/generation changed") }
	s := enginetest.StartSession(t, e, nil)
	enginetest.PreparedDescription(t, e, s, "SELECT * FROM pg_stat_slru")
	for range 3 {
		slruOwnerRows(t, enginetest.SessionSQL(t, s, "SELECT * FROM pg_stat_slru"), family[0].ResetAt, [7]int64{})
		if e.Statistics.ReadSLRUs() != family { t.Fatal("read/prepare sampled reset time") }
	}
	var zero statistics.Provider
	if zero.ReadSLRUs() != ([statistics.SLRUCount]statistics.SLRUFact{}) { t.Fatal("zero provider invented history") }
}

func TestPGStatSLRUCapture(t *testing.T) {
	vector := [7]int64{11,23,37,41,53,67,79}
	t1, t2 := time.Unix(100,0).UTC(), time.Unix(200,0).UTC()
	for _, mode := range []string{"none","cache","snapshot"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New(); s := enginetest.StartSession(t,e,nil)
			activityOwnerRun(t,e,s,"CREATE TABLE slru_cut(v integer)","SET stats_fetch_consistency="+mode,"BEGIN")
			defer activityOwnerRun(t,e,s,"ROLLBACK")
			ast, description := enginetest.PreparedDescription(t,e,s,"SELECT * FROM pg_stat_slru")
			stamp := enginetest.SessionSQL(t,s,"SELECT pg_stat_get_snapshot_timestamp()")
			v := make([]sqltype.Value,1); present, err := stamp.Rows.Next(t.Context(),v); stamp.Rows.Close()
			if err != nil || !present || !v[0].IsNull() { t.Fatal("prepare created snapshot",v,err) }
			e.Statistics.ResetShared(statistics.SLRU,t1)
			var w statistics.Writer
			for k:=statistics.SLRUKind(0);k<statistics.SLRUCount;k++ { w.SLRU(&e.Statistics,k,statistics.SLRUFact{Counters:vector}) }
			w.Flush(&e.Statistics,t1,true)
			key := statistics.Key{Object: enginetest.RoleTableRef(t,e,"slru_cut")}
			w.Add(&e.Statistics,key,statistics.Fact{Inserts:17})
			w.Flush(&e.Statistics,t1,true)
			capture:=func() core.Result { result,err:=enginetest.PreparedExecute(e,s,ast,&description);if err!=nil { t.Fatal(err) };return result }
			old:=capture()
			slruOwnerRows(t,capture(),t1,vector)
			slruOwnerRows(t,enginetest.SessionSQL(t,s,"SELECT * FROM pg_stat_slru"),t1,vector)
			w.Add(&e.Statistics,key,statistics.Fact{Inserts:19})
			w.Flush(&e.Statistics,t2,true)
			table := enginetest.SessionSQL(t,s,"SELECT n_tup_ins FROM pg_stat_all_tables WHERE relname='slru_cut'")
			cell := make([]sqltype.Value,1)
			present, err = table.Rows.Next(t.Context(),cell); table.Rows.Close()
			want := int64(36)
			if mode=="snapshot" { want=17 }
			if !present || err!=nil { t.Fatal("cross-family row",present,err) }
			if got,err:=cell[0].Integer();err!=nil || got!=want { t.Fatal("same-cut cross-family capture",got,want,err) }
			e.Statistics.ResetShared(statistics.SLRU,t2)
			slruOwnerRows(t,old,t1,vector)
			if mode=="none" { slruOwnerRows(t,capture(),t2,[7]int64{}) } else { slruOwnerRows(t,capture(),t1,vector) }
			activityOwnerRun(t,e,s,"SELECT pg_stat_clear_snapshot()")
			slruOwnerRows(t,capture(),t2,[7]int64{})
		})
	}
	t.Run("denial_before_snapshot",func(t *testing.T) {
		e:=engine.New();s:=enginetest.StartSession(t,e,nil)
		activityOwnerRun(t,e,s,"CREATE ROLE slru_denied","SET stats_fetch_consistency=snapshot")
		ast,description:=enginetest.PreparedDescription(t,e,s,"SELECT * FROM pg_stat_slru")
		activityOwnerRun(t,e,s,"REVOKE EXECUTE ON FUNCTION pg_stat_get_slru() FROM PUBLIC","BEGIN","SET ROLE slru_denied","SAVEPOINT denied")
		result,err:=enginetest.PreparedExecute(e,s,ast,&description)
		if result.Rows!=nil { result.Rows.Close();t.Fatal("denied execution published rows") }
		enginetest.State(t,err,"42501")
		activityOwnerRun(t,e,s,"ROLLBACK TO SAVEPOINT denied","RESET ROLE")
		stamp:=enginetest.SessionSQL(t,s,"SELECT pg_stat_get_snapshot_timestamp()")
		cell:=make([]sqltype.Value,1);present,err:=stamp.Rows.Next(t.Context(),cell);stamp.Rows.Close()
		if err!=nil || !present || !cell[0].IsNull() { t.Fatal("denial created snapshot",cell,err) }
		activityOwnerRun(t,e,s,"ROLLBACK")
	})
}
