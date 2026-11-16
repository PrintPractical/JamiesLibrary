// Package metadata looks a book up by ISBN at Open Library and keeps the cover
// it finds until the book is saved (security design §10, architecture §9).
//
// Only the server talks to the provider. Its client takes https only, from a
// short list of hosts, on every redirect hop, within one deadline and with a
// cap on every body it reads. What the provider says is untrusted text.
package metadata

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Limits (security design §10).
const (
	// Timeout is the one deadline over a whole lookup, cover included (DEPLOY-T5).
	Timeout = 10 * time.Second

	maxHops     = 5
	maxJSON     = 1 << 20 // metadata JSON
	maxImage    = 5 << 20 // a cover
	openLibrary = "https://openlibrary.org"
	covers      = "https://covers.openlibrary.org"
)

// hostAllowed is the list of hosts the client will talk to: Open Library, its
// cover host, and archive.org with its subdomains, where covers redirect.
func hostAllowed(host string) bool {
	switch host = strings.ToLower(strings.TrimSuffix(host, ".")); {
	case host == "openlibrary.org", host == "covers.openlibrary.org", host == "archive.org":
		return true
	case strings.HasSuffix(host, ".archive.org"):
		return true
	}
	return false
}

// errStatus is a provider answer other than the one asked for.
type errStatus struct{ code int }

func (e errStatus) Error() string { return fmt.Sprintf("the provider answered %d", e.code) }

// client is the one outbound HTTP client.
type client struct {
	http      *http.Client
	userAgent string
	// test, when set, is the one origin allowed besides the real hosts: a fake
	// provider in a test build, on any scheme.
	test *url.URL
}

func newClient(userAgent string, test *url.URL) *client {
	c := &client{userAgent: userAgent, test: test}
	c.http = &http.Client{
		Timeout: Timeout,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: Timeout,
			MaxIdleConns:          4,
			IdleConnTimeout:       30 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxHops {
				return fmt.Errorf("more than %d redirects", maxHops)
			}
			return c.check(req.URL)
		},
	}
	return c
}

// check refuses a URL the client may not fetch: it is used for the first
// request and for each redirect.
func (c *client) check(u *url.URL) error {
	if c.test != nil && u.Scheme == c.test.Scheme && u.Host == c.test.Host {
		return nil
	}
	if u.Scheme != "https" {
		return fmt.Errorf("refusing a %q URL: https only", u.Scheme)
	}
	if p := u.Port(); p != "" && p != "443" {
		return fmt.Errorf("refusing port %s", p)
	}
	if u.User != nil || !hostAllowed(u.Hostname()) {
		return fmt.Errorf("refusing host %q", u.Hostname())
	}
	return nil
}

// get fetches rawURL and returns at most max bytes of the body. A longer body
// is an error, never a truncated answer. A status other than 200 is an errStatus.
func (c *client) get(ctx context.Context, rawURL string, max int64) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if err := c.check(u); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json, image/*")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errStatus{resp.StatusCode}
	}
	if resp.ContentLength > max {
		return nil, fmt.Errorf("the answer is larger than %d bytes", max)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("the answer is larger than %d bytes", max)
	}
	return data, nil
}

func isNotFound(err error) bool {
	var s errStatus
	return errors.As(err, &s) && s.code == http.StatusNotFound
}
