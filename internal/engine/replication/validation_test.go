package replication_test

import (
 "testing"
 "stuntgres/internal/engine/replication"
)

func TestReplicationPluginAllowlist(t *testing.T) {
 for _,tc:=range []struct{list,plugin string; want bool}{
  {"pgoutput, test_decoding","pgoutput",true},{"pgoutput, test_decoding","test_decoding",true},{"pgoutput","PGOUTPUT",false},{"PGOUTPUT","PGOUTPUT",true},
  {` "has space" , "a""b" `,"has space",true},{`"a""b"`,`a"b`,true},{"$libdir/pgoutput","pgoutput",false},{"$libdir/pgoutput","$libdir/pgoutput",true},
  {`odd"quote`,`odd"quote`,true},{`pgoutput, odd"quote`,"pgoutput",true},{`pgoutput, odd"quote`,`odd"quote`,true},
  {"pgoutput,","pgoutput",false},{`pgoutput,"unfinished`,"pgoutput",false},{"pgoutput,,other","pgoutput",false},{`pgoutput,"ok"junk`,"pgoutput",false},{"pgoutput extra","pgoutput",false},
  {"","",false},{" \t\n","pgoutput",false},{`""`,"",true},{"pgoutput\v,\f test_decoding","test_decoding",true},
 } { if got:=replication.PluginAllowed(tc.list,tc.plugin); got!=tc.want { t.Errorf("PluginAllowed(%q,%q)=%t, want %t",tc.list,tc.plugin,got,tc.want) } }
}
