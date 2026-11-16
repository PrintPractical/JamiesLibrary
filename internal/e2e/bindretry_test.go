package e2e

import (
	"net"
	"strconv"
	"testing"
)

// A port that is taken between being chosen and being bound must not fail the
// start: the server is started again on another one, on an untouched data
// directory.
func TestServerStartRetriesWhenItsPortIsTaken(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	busy := taken.Addr().(*net.TCPAddr).Port

	orig := pickPort
	defer func() { pickPort = orig }()
	calls := 0
	pickPort = func(t testing.TB) int {
		calls++
		if calls == 1 { // the first start's app port
			return busy
		}
		return orig(t)
	}

	p := start(t, t.TempDir()+"/data", nil)
	if calls < 3 {
		t.Fatalf("the server was not started a second time (ports chosen: %d)", calls)
	}
	if p.Base == "http://127.0.0.1:"+strconv.Itoa(busy) {
		t.Fatalf("the server still uses the taken port %d", busy)
	}
	p.stop()
}
