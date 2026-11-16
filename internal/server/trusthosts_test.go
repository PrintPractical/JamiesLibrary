package server

import (
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"

	"jamieslibrary/internal/config"
)

func ips(s ...string) func() ([]net.IP, error) {
	return func() ([]net.IP, error) {
		var out []net.IP
		for _, v := range s {
			out = append(out, net.ParseIP(v))
		}
		return out, nil
	}
}

// With no JL_HOSTNAMES or JL_ADDRESSES the console names the machine's own
// private addresses, not localhost, which a phone can't reach.
func TestTrustHosts(t *testing.T) {
	lan := ips("127.0.0.1", "8.8.8.8", "192.168.1.20", "10.0.0.5", "fe80::1", "172.16.0.2", "192.168.1.21")
	cases := []struct {
		name string
		cfg  config.Config
		ifs  func() ([]net.IP, error)
		want []string
	}{
		{"private addresses, at most three", config.Config{}, lan, []string{"192.168.1.20", "10.0.0.5", "172.16.0.2"}},
		{"a configured name wins", config.Config{Hostnames: []string{"library.lan"}}, lan, []string{"library.lan"}},
		{"a configured address wins", config.Config{Addresses: []net.IP{net.ParseIP("192.168.9.9")}}, lan, []string{"192.168.9.9"}},
		{"nothing private", config.Config{}, ips("127.0.0.1", "8.8.8.8"), []string{"localhost"}},
		{"interfaces unreadable", config.Config{}, func() ([]net.IP, error) { return nil, errors.New("x") }, []string{"localhost"}},
	}
	for _, c := range cases {
		if got := trustHosts(c.cfg, c.ifs); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPrintTrustGuide(t *testing.T) {
	var one, many strings.Builder
	printTrustGuide(&one, []string{"192.168.1.20"}, 8080)
	if want := "On a phone or another computer, first follow the trust guide: http://192.168.1.20:8080/trust/\n"; one.String() != want {
		t.Errorf("one address:\n%s", one.String())
	}
	printTrustGuide(&many, []string{"192.168.1.20", "10.0.0.5"}, 8080)
	want := "On a phone or another computer, first follow the trust guide at whichever of these addresses is on the phone's network:\n- http://192.168.1.20:8080/trust/\n- http://10.0.0.5:8080/trust/\n"
	if many.String() != want {
		t.Errorf("several addresses:\n%s", many.String())
	}
}

func TestPrintSetupPage(t *testing.T) {
	cases := []struct {
		name  string
		cfg   config.Config
		hosts []string
		want  string
	}{
		{"https address and port", config.Config{HTTPSPort: 8443}, []string{"192.168.1.20"}, "Setup page: https://192.168.1.20:8443/setup\n"},
		{"port 443 is left out", config.Config{HTTPSPort: 443}, []string{"library.lan"}, "Setup page: https://library.lan/setup\n"},
		{"each host", config.Config{HTTPSPort: 8443}, []string{"10.0.0.5", "10.0.0.6"}, "Setup page: https://10.0.0.5:8443/setup\nSetup page: https://10.0.0.6:8443/setup\n"},
		{"behind a proxy", config.Config{HTTPOnly: true, HTTPSPort: 8443}, []string{"10.0.0.5"}, "Setup page: the address this library is served at, followed by /setup (for example https://library.example.org/setup).\n"},
	}
	for _, c := range cases {
		var b strings.Builder
		printSetupPage(&b, c.cfg, c.hosts)
		if b.String() != c.want {
			t.Errorf("%s:\n%s\nwant\n%s", c.name, b.String(), c.want)
		}
	}
}
