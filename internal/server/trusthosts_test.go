package server

import (
	"errors"
	"net"
	"reflect"
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
