package e2e

// DEPLOY-T6: a library of 20,000 copies and 500 patrons, searched by 100 clients
// at once, stays fast (95th percentile under one second) and small (under 512 MB)
// in a container limited to 512 MB.
//
// The test drives an installation that is already running in a container, so
// the numbers are the container's. It is skipped unless JL_LOAD_URL is set; the
// script scripts/loadtest.sh starts the container, runs this test, and removes
// the container. Timings are those of the machine running the container, not of
// a Raspberry Pi.
//
//	JL_LOAD_URL        base URL of the library (plain HTTP, JL_HTTP_ONLY=1)
//	JL_LOAD_SETUP_CODE the first-run code the container printed
//	JL_LOAD_CONTAINER  the container's name, for memory and out-of-memory checks
//	JL_LOAD_MEMORY_MB  the limit the container was given (default 512)
//	JL_LOAD_SEED_WORKERS  how many requests seed the library at once (default 4)

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	loadCopies   = 20000
	loadPatrons  = 500
	loadSearches = 100 // concurrent clients
	loadRounds   = 30  // searches each client makes
)

var loadWords = strings.Fields(`river garden winter night castle forest silver dragon quiet harbour
	lantern orchard meadow thunder velvet compass island moth ember willow granite saffron hollow
	paper kingdom journey shadow summer mountain whisper copper falcon marble tide cinder juniper`)

var loadNames = strings.Fields(`Adams Baker Chen Diaz Evans Fischer Garcia Haddad Ito Jones Kowalski Lopez
	Moreau Nkosi Okafor Petrov Quinn Rossi Silva Tanaka Usman Varga Walker Xu Yilmaz Zhou`)

type loadClient struct {
	t    *testing.T
	base string
	c    *http.Client
}

func newLoadClient(t *testing.T, base string) *loadClient {
	jar, _ := cookiejar.New(nil)
	return &loadClient{t: t, base: base, c: &http.Client{
		Timeout: 60 * time.Second, Jar: secureJarFor(jar),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// secureJarFor lets the Secure session cookie travel over the plain-HTTP address
// of a container that sits behind no proxy in this test.
func secureJarFor(j http.CookieJar) http.CookieJar { return httpsView{j} }

type httpsView struct{ http.CookieJar }

func asHTTPSURL(u *url.URL) *url.URL {
	c := *u
	c.Scheme = "https"
	return &c
}

func (h httpsView) SetCookies(u *url.URL, c []*http.Cookie) { h.CookieJar.SetCookies(asHTTPSURL(u), c) }
func (h httpsView) Cookies(u *url.URL) []*http.Cookie       { return h.CookieJar.Cookies(asHTTPSURL(u)) }

func urlQuery(q string) string { return url.QueryEscape(q) }

func capital(w string) string { return strings.ToUpper(w[:1]) + w[1:] }

func (l *loadClient) do(method, path string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, l.base+path, rd)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := l.c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return resp.StatusCode, raw, err
}

func (l *loadClient) must(status int, method, path string, body any) map[string]any {
	l.t.Helper()
	code, raw, err := l.do(method, path, body)
	if err != nil || code != status {
		l.t.Fatalf("%s %s: want %d, got %d, %v: %.300s", method, path, status, code, err, raw)
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}

// parallel runs fn for 0..n-1 on workers goroutines and stops at the first failure.
func parallel(n, workers int, fn func(i int) error) error {
	var next atomic.Int64
	var wg sync.WaitGroup
	var first atomic.Value
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for first.Load() == nil {
				i := int(next.Add(1)) - 1
				if i >= n {
					return
				}
				if err := fn(i); err != nil {
					first.CompareAndSwap(nil, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if e := first.Load(); e != nil {
		return e.(error)
	}
	return nil
}

// containerMemory samples the container's memory use (what the kernel charges
// the container, page cache included) until stop is closed, and keeps the peak.
type memWatch struct {
	name string
	peak atomic.Int64 // bytes
	stop chan struct{}
	done chan struct{}
}

var memUse = regexp.MustCompile(`^([0-9.]+)\s*([KMG]i?B|B)`)

func parseSize(s string) int64 {
	m := memUse.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return -1
	}
	v, _ := strconv.ParseFloat(m[1], 64)
	mult := map[string]float64{"B": 1, "KiB": 1 << 10, "KB": 1e3, "MiB": 1 << 20, "MB": 1e6, "GiB": 1 << 30, "GB": 1e9}[m[2]]
	return int64(v * mult)
}

func (w *memWatch) sample() int64 {
	out, err := exec.Command("docker", "stats", "--no-stream", "--format", "{{.MemUsage}}", w.name).Output()
	if err != nil {
		return -1
	}
	return parseSize(strings.SplitN(string(out), "/", 2)[0])
}

func startMemWatch(name string) *memWatch {
	w := &memWatch{name: name, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		for {
			if v := w.sample(); v > w.peak.Load() {
				w.peak.Store(v)
			}
			select {
			case <-w.stop:
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	}()
	return w
}

func (w *memWatch) finish() int64 {
	close(w.stop)
	<-w.done
	return w.peak.Load()
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := int(float64(len(sorted))*p+0.999999) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}

func dockerInspect(name, format string) string {
	out, _ := exec.Command("docker", "inspect", "--format", format, name).Output()
	return strings.TrimSpace(string(out))
}

func TestTwentyThousandCopiesFiveHundredPatronsHundredSearches(t *testing.T) {
	base := strings.TrimRight(os.Getenv("JL_LOAD_URL"), "/")
	if base == "" {
		t.Skip("set JL_LOAD_URL to a library running in a 512 MB container (scripts/loadtest.sh does it)")
	}
	container := os.Getenv("JL_LOAD_CONTAINER")
	limitMB := int64(512)
	if v := os.Getenv("JL_LOAD_MEMORY_MB"); v != "" {
		limitMB, _ = strconv.ParseInt(v, 10, 64)
	}
	if container != "" {
		if got := dockerInspect(container, "{{.HostConfig.Memory}}"); got != strconv.FormatInt(limitMB<<20, 10) {
			t.Fatalf("the container's memory limit is %q bytes, not %d MB: the numbers would not mean what they say", got, limitMB)
		}
	}
	seedWorkers := 4
	if v := os.Getenv("JL_LOAD_SEED_WORKERS"); v != "" {
		seedWorkers, _ = strconv.Atoi(v)
	}
	var watch *memWatch
	if container != "" {
		watch = startMemWatch(container)
	}

	admin := newLoadClient(t, base)
	admin.must(200, "POST", "/api/setup/admin", map[string]string{
		"code": os.Getenv("JL_LOAD_SETUP_CODE"), "username": "root", "display_name": "Root", "password": adminPassword})

	// Seed: 8,000 books, half with two copies and half with three, 20,000 copies in all.
	rng := rand.New(rand.NewSource(77))
	locs := make([]int64, 0, 8)
	for i := 0; i < 8; i++ {
		l := admin.must(201, "POST", "/api/locations", map[string]string{"name": fmt.Sprintf("Shelf %d", i+1)})
		locs = append(locs, int64(l["id"].(float64)))
	}
	const books = 8000
	titles := make([]string, books)
	for i := range titles {
		titles[i] = fmt.Sprintf("The %s %s of %s %d", capital(loadWords[rng.Intn(len(loadWords))]),
			capital(loadWords[rng.Intn(len(loadWords))]), capital(loadWords[rng.Intn(len(loadWords))]), i)
	}
	seedStart := time.Now()
	var copies atomic.Int64
	bookIDs := make([]int64, books)
	firstCode := make([]string, books) // each book's first copy's barcode, for the desk
	if err := parallel(books, seedWorkers, func(i int) error {
		code, raw, err := admin.do("POST", "/api/books", map[string]any{
			"title": titles[i], "authors": []string{loadNames[i%len(loadNames)] + ", " + loadWords[i%len(loadWords)]},
			"description": "Seeded for the load test."})
		if err != nil || code != 201 {
			return fmt.Errorf("add book %d: %d %v %.200s", i, code, err, raw)
		}
		var b map[string]any
		_ = json.Unmarshal(raw, &b)
		id := int64(b["id"].(float64))
		bookIDs[i] = id
		n := 2 + i%2
		for k := 0; k < n; k++ {
			code, raw, err := admin.do("POST", fmt.Sprintf("/api/books/%d/copies", id),
				map[string]any{"location_id": locs[(i+k)%len(locs)], "condition": "good"})
			if err != nil || code != 201 {
				return fmt.Errorf("add copy to book %d: %d %v %.200s", i, code, err, raw)
			}
			if k == 0 {
				var cp map[string]any
				_ = json.Unmarshal(raw, &cp)
				firstCode[i], _ = cp["barcode"].(string)
			}
			copies.Add(1)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if copies.Load() != loadCopies {
		t.Fatalf("seeded %d copies, want %d", copies.Load(), loadCopies)
	}
	t.Logf("seeded %d books and %d copies in %s", books, copies.Load(), time.Since(seedStart).Round(time.Second))

	patronStart := time.Now()
	cards := make([]string, loadPatrons)
	patrons := make([]*loadClient, loadPatrons)
	if err := parallel(loadPatrons, seedWorkers, func(i int) error {
		code, raw, err := admin.do("POST", "/api/admin/invitations", map[string]string{"role": "patron"})
		if err != nil || code != 201 {
			return fmt.Errorf("invite %d: %d %v %.200s", i, code, err, raw)
		}
		var inv map[string]any
		_ = json.Unmarshal(raw, &inv)
		p := newLoadClient(t, base)
		code, raw, err = p.do("POST", "/api/register", map[string]string{"code": inv["code"].(string),
			"username": fmt.Sprintf("patron%03d", i), "display_name": fmt.Sprintf("Patron %d", i), "password": adminPassword})
		if err != nil || code != 201 {
			return fmt.Errorf("register %d: %d %v %.200s", i, code, err, raw)
		}
		code, raw, err = p.do("GET", "/api/me/card", nil)
		if err != nil || code != 200 {
			return fmt.Errorf("card %d: %d %v", i, code, err)
		}
		var card map[string]any
		_ = json.Unmarshal(raw, &card)
		cards[i], _ = card["number"].(string)
		patrons[i] = p
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("registered %d patrons in %s", loadPatrons, time.Since(patronStart).Round(time.Second))

	seen := map[string]bool{}
	for _, c := range cards {
		if c == "" || seen[c] {
			t.Fatalf("card number %q is empty or repeated across %d patrons", c, loadPatrons)
		}
		seen[c] = true
	}

	// The seeded library really holds what the test says it does.
	all := admin.must(200, "GET", "/api/books?q=", nil)
	if int(all["total"].(float64)) != books {
		t.Fatalf("the catalogue lists %v books, want %d", all["total"], books)
	}

	// 100 clients search at once; some are patrons, some staff. A few desk
	// operations run alongside, as a busy afternoon would.
	queries := func(r *rand.Rand) string {
		switch r.Intn(5) {
		case 0:
			return ""
		case 1:
			return loadWords[r.Intn(len(loadWords))]
		case 2:
			return loadWords[r.Intn(len(loadWords))] + " " + loadWords[r.Intn(len(loadWords))]
		case 3:
			return strings.ToUpper(loadNames[r.Intn(len(loadNames))])
		}
		return "zzqx" + strconv.Itoa(r.Intn(1000)) // no match
	}
	var mu sync.Mutex
	var lat []time.Duration
	var failures []string
	runSearches := func(label string) {
		lat = lat[:0]
		start := time.Now()
		var wg sync.WaitGroup
		for w := 0; w < loadSearches; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				cl := admin
				if w%2 == 1 {
					cl = patrons[w%loadPatrons]
				}
				r := rand.New(rand.NewSource(int64(w)))
				for k := 0; k < loadRounds; k++ {
					path := "/api/books?q=" + urlQuery(queries(r))
					if r.Intn(4) == 0 {
						path += "&page=" + strconv.Itoa(1+r.Intn(5))
					}
					t0 := time.Now()
					code, raw, err := cl.do("GET", path, nil)
					d := time.Since(t0)
					mu.Lock()
					lat = append(lat, d)
					if err != nil || code != 200 {
						failures = append(failures, fmt.Sprintf("%s: %d %v %.100s", path, code, err, raw))
					}
					mu.Unlock()
				}
			}(w)
		}
		wg.Wait()
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		t.Logf("%s: %d searches by %d clients in %s: p50 %s, p95 %s, p99 %s, max %s", label, len(lat), loadSearches,
			time.Since(start).Round(time.Millisecond), percentile(lat, .5), percentile(lat, .95), percentile(lat, .99), lat[len(lat)-1])
	}

	runSearches("searching alone")
	p95Alone := percentile(lat, .95)

	stopDesk := make(chan struct{})
	var deskDone sync.WaitGroup
	var deskOps atomic.Int64
	for d := 0; d < 4; d++ {
		deskDone.Add(1)
		go func(d int) {
			defer deskDone.Done()
			r := rand.New(rand.NewSource(int64(1000 + d)))
			for {
				select {
				case <-stopDesk:
					return
				default:
				}
				i := r.Intn(books)
				code := firstCode[i]
				if code == "" {
					continue
				}
				if c, _, _ := admin.do("POST", "/api/desk/checkout", map[string]string{"patron": cards[r.Intn(loadPatrons)], "code": code}); c == 200 {
					admin.do("POST", "/api/desk/checkin", map[string]string{"code": code})
					deskOps.Add(2)
				}
			}
		}(d)
	}
	runSearches("searching while the desk works")
	close(stopDesk)
	deskDone.Wait()
	p95Busy := percentile(lat, .95)
	t.Logf("desk operations during the busy run: %d", deskOps.Load())
	if deskOps.Load() == 0 {
		t.Error("the desk did no check-outs during the busy run, so it was not busy")
	}

	if len(failures) > 0 {
		t.Errorf("%d searches failed, first: %s", len(failures), failures[0])
	}
	for _, p := range []struct {
		name string
		d    time.Duration
	}{{"alone", p95Alone}, {"with the desk busy", p95Busy}} {
		if p.d >= time.Second {
			t.Errorf("p95 of searches %s is %s, want under 1 s", p.name, p.d)
		}
	}

	if watch != nil {
		peak := watch.finish()
		if peak < 0 {
			t.Fatal("could not read the container's memory with docker stats")
		}
		t.Logf("container memory peak (docker stats, page cache included) %.1f MiB of the %d MiB limit", float64(peak)/(1<<20), limitMB)
		if peak >= 512<<20 {
			t.Errorf("memory peak %.1f MiB is not under 512 MB", float64(peak)/(1<<20))
		}
		if oom := dockerInspect(container, "{{.State.OOMKilled}} {{.RestartCount}} {{.State.Running}}"); oom != "false 0 true" {
			t.Errorf("the container was killed or restarted during the run (OOMKilled, restarts, running): %s", oom)
		}
	}

	// What was seeded is still all there, and the library still answers.
	all = admin.must(200, "GET", "/api/books?q=", nil)
	if int(all["total"].(float64)) != books {
		t.Errorf("after the run the catalogue lists %v books, want %d", all["total"], books)
	}
}

// The load test's own arithmetic, so a wrong percentile or size can't make a slow
// or large library look fine.
func TestLoadHelpersMeasureWhatTheySay(t *testing.T) {
	var ds []time.Duration
	for i := 1; i <= 100; i++ {
		ds = append(ds, time.Duration(i)*time.Millisecond)
	}
	if got := percentile(ds, .95); got != 95*time.Millisecond {
		t.Errorf("p95 of 1..100 ms is %s, want 95ms", got)
	}
	if got := percentile(ds, .5); got != 50*time.Millisecond {
		t.Errorf("p50 of 1..100 ms is %s, want 50ms", got)
	}
	if got := percentile(ds, 1); got != 100*time.Millisecond {
		t.Errorf("p100 is %s, want 100ms", got)
	}
	for in, want := range map[string]int64{"234.2MiB": 245576499, "1.5GiB": 1610612736, "900KiB": 921600, "512B": 512, "garbage": -1} {
		if got := parseSize(in); got != want {
			t.Errorf("parseSize(%q) = %d, want %d", in, got, want)
		}
	}
}
