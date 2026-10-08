//go:build reference

package reference_test

import (
	"fmt"
	"testing"
)

// PostgreSQL has no native Restore API. These are independently expected public
// SQL inputs; TestStringCoreReviewRestore exercises the actual native staging API.
func stringCoreReviewRestoreSQLReference(t *testing.T, p *referencePair, oracle bool) {
	run := func(t *testing.T, s referenceStep) {
		t.Helper()
		if oracle {
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
		} else {
			p.step(t, s)
		}
	}
	want := [][]any{
		{int64(1), "X", true}, {int64(2), "xy ", true}, {int64(3), " xy", true},
		{int64(4), "Yxx", true}, {int64(5), "xxY", false}, {int64(6), "Tom", false},
		{int64(7), "é", nil}, {int64(8), "é", true}, {int64(9), "ffi", true},
		{int64(10), "é", nil}, {int64(11), "[é]", true},
	}
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			name := fmt.Sprintf("sc_restore_%t", prepared)
			run(t, referenceExec("CREATE TABLE "+name+"(id INT,v TEXT,p BOOL)", 0))
			q := "INSERT INTO " + name + ` VALUES
(1,btrim('é🙂X🙂é','é🙂'),is_normalized('é')),
(2,ltrim(' xy ',' '),is_normalized('é','NFD')),
(3,rtrim(' xy ',' '),is_normalized('é','NFD')),
(4,trim(leading 'x' from 'xxYxx'),is_normalized('é','NFC')),
(5,trim(trailing 'x' from 'xxYxx'),is_normalized('é','NFD')),
(6,trim(both 'xy' from 'yxTomxy'),is_normalized('é','NFKC')),
(7,normalize('é'),is_normalized(NULL)),
(8,normalize('é',NFD),is_normalized('é','NFD')),
(9,pg_catalog.normalize('ﬃ','nfKc'),is_normalized('ffi','NFKC')),
(10,"normalize"('é','nFkD'),is_normalized('é',NULL)),
(11,'['||normalize(btrim(' é '))||']',is_normalized(normalize('é')))`
			s := referenceExec(q, 11)
			if prepared {
				s = referencePrepared(s)
			}
			run(t, s)
			s = referenceSelect("SELECT id,v,p FROM "+name+" ORDER BY id", []string{"id", "v", "p"}, []string{"INT4", "TEXT", "BOOL"}, want)
			if prepared {
				s = referencePrepared(s)
			}
			run(t, s)
			s = referenceImplicitError("INSERT INTO "+name+` VALUES(12,pg_catalog.normalize('x','bad'),true)`, referenceDiagnostic{Code: "22023", Message: "invalid normalization form: bad"})
			if prepared {
				s = referencePrepared(s)
				s.errorStage = "execute"
			}
			run(t, s)
			run(t, referenceSelect("SELECT id,v,p FROM "+name+" ORDER BY id", []string{"id", "v", "p"}, []string{"INT4", "TEXT", "BOOL"}, want))
		})
	}
}

func TestReferenceStringCoreReviewRestoreSQL(t *testing.T) {
	stringCoreReviewRestoreSQLReference(t, newReferencePair(t), false)
}
