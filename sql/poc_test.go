// Temporary proof-of-concept test that uses the testingshim to set up a test
// server from the sql package.

package sql

import (
	"testing"

	"github.com/cockroachdb/cockroach/client"
	"github.com/cockroachdb/cockroach/server/testingshim"
	"github.com/cockroachdb/cockroach/util/leaktest"
)

func TestPOC(t *testing.T) {
	defer leaktest.AfterTest(t)()

	s := testingshim.NewTestServerShim()
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	kvClient := s.ClientDB().(*client.DB)
	err := kvClient.Put("testkey", "testval")
	if err != nil {
		t.Fatal(err)
	}
	kv, err := kvClient.Get("testkey")
	if err != nil {
		t.Fatal(err)
	}
	if kv.PrettyValue() != `"testval"` {
		t.Errorf(`Invalid Get result: %s, expected "testval"`, kv.PrettyValue())
	}
}
