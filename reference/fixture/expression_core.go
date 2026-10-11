package fixture

import (
	"testing"
)

func ExpressionReferenceSetup(t *testing.T) *Pair {
	p := NewScenario(t)
	ScalarStep(t, p, Exec("CREATE TABLE t(id INTEGER,v INTEGER,b BIGINT,s TEXT,f BOOLEAN)", 0))
	ScalarStep(t, p, Exec("INSERT INTO t VALUES(1,1,2147483648,'x',TRUE),(2,NULL,2,'x',NULL),(3,2,3,'y',FALSE),(4,1,4,'x',TRUE)", 4))
	return p
}
