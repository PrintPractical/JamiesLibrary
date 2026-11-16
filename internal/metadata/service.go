package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	_ "image/gif" // so the 1x1 placeholder Open Library sends can be recognised
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"jamieslibrary/internal/catalog"
	"jamieslibrary/internal/media"
)

// Bounds on the text taken from the provider (security design §10).
const (
	maxTitle       = 500
	maxAuthorName  = 200
	maxAuthors     = 20
	maxDescription = 10000

	// coverTTL is how long a fetched cover waits for its book to be saved. The
	// store's tmp cleaner deletes the file too; this only ends the claim on it.
	coverTTL = time.Hour
	// maxPending bounds the covers held for people who haven't saved yet.
	maxPending = 200
)

// Outcomes other than a book. A bad ISBN is an *apperr.Error and is never sent.
var (
	// ErrNotFound: the provider answered and doesn't know the ISBN.
	ErrNotFound = errors.New("the provider has no book with that ISBN")
	// ErrUnavailable: the provider didn't answer, in time or at all, or answered nonsense.
	ErrUnavailable = errors.New("lookup unavailable")
)

// Book is what a lookup found, to prefill the form. Nothing is saved.
type Book struct {
	ISBN13      string
	Title       string
	Authors     []string
	Description string
	// CoverToken names the cover held for the caller, "" when there is none.
	CoverToken string
}

// Options make a Service.
type Options struct {
	// Images is where a cover goes when its book is saved; Tmp is where it waits.
	// Tmp is a sibling of Images (media.Images.Sibling), on DATA_DIR/tmp.
	Images, Tmp *media.Images
	// Version is the program's version, for the User-Agent.
	Version string
	// BaseURL replaces the provider's hosts. It's set from the test hooks and so
	// is always empty in a release build.
	BaseURL string
	Logger  *slog.Logger
	// Timeout is the deadline over a lookup; the default is Timeout (10 s).
	Timeout time.Duration
	// Now is the clock for a cover's age; the default is time.Now.
	Now func() time.Time
}

// Service is the book lookup.
type Service struct {
	c       *client
	images  *media.Images
	tmp     *media.Images
	log     *slog.Logger
	timeout time.Duration
	now     func() time.Time
	olBase  string
	cvBase  string

	mu      sync.Mutex
	pending map[string]pendingCover
}

type pendingCover struct {
	user int64
	at   time.Time
}

// New makes the Service. It fails on a BaseURL that isn't a URL.
func New(o Options) (*Service, error) {
	s := &Service{images: o.Images, tmp: o.Tmp, log: o.Logger, timeout: o.Timeout, now: o.Now,
		olBase: openLibrary, cvBase: covers, pending: map[string]pendingCover{}}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.timeout <= 0 {
		s.timeout = Timeout
	}
	if s.now == nil {
		s.now = time.Now
	}
	ver := o.Version
	if ver == "" {
		ver = "dev"
	}
	var test *url.URL
	if o.BaseURL != "" {
		u, err := url.Parse(strings.TrimRight(o.BaseURL, "/"))
		if err != nil || u.Host == "" {
			return nil, errors.New("the metadata base URL is not a URL")
		}
		test, s.olBase, s.cvBase = u, u.String(), u.String()
	}
	s.c = newClient("JamiesLibrary/"+ver+" (self-hosted library software; ISBN lookup)", test)
	return s, nil
}

// Lookup asks the provider about an ISBN-10 or ISBN-13 and, if it has a cover,
// keeps the cover for actor under a token. The whole of it, cover included, is
// done or given up within the timeout. The errors are an *apperr.Error for an
// ISBN with a wrong check digit (nothing is asked), ErrNotFound and ErrUnavailable.
func (s *Service) Lookup(ctx context.Context, actor int64, rawISBN string) (Book, error) {
	isbn, err := catalog.ParseISBN(rawISBN)
	if err != nil {
		return Book{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	data, err := s.c.get(ctx, s.olBase+"/api/books?format=json&jscmd=data&bibkeys=ISBN:"+isbn, maxJSON)
	if err != nil {
		return Book{}, s.unavailable(isbn, "book data", err)
	}
	var found map[string]struct {
		Title   string `json:"title"`
		Authors []struct {
			Name string `json:"name"`
		} `json:"authors"`
		Cover json.RawMessage `json:"cover"`
	}
	if err := json.Unmarshal(data, &found); err != nil {
		return Book{}, s.unavailable(isbn, "book data", err)
	}
	entry, ok := found["ISBN:"+isbn]
	b := Book{ISBN13: isbn}
	if ok {
		b.Title = cleanText(entry.Title, maxTitle, false)
	}
	if b.Title == "" {
		return Book{}, ErrNotFound
	}
	for _, a := range entry.Authors {
		if a := cleanText(a.Name, maxAuthorName, false); a != "" && len(b.Authors) < maxAuthors {
			b.Authors = append(b.Authors, a)
		}
	}
	b.Description = s.description(ctx, isbn)
	if len(entry.Cover) > 0 && string(entry.Cover) != "null" {
		b.CoverToken = s.cover(ctx, actor, isbn)
	}
	return b, nil
}

func (s *Service) unavailable(isbn, what string, err error) error {
	// The detail is for the server's log; the person is told only that it's unavailable.
	s.log.Warn("book lookup unavailable", "what", what, "isbn", isbn, "err", err)
	return ErrUnavailable
}

// description is the edition's, else its work's. Both are optional: a book
// without one, or one that can't be fetched, is still found.
func (s *Service) description(ctx context.Context, isbn string) string {
	var ed struct {
		Description json.RawMessage `json:"description"`
		Works       []struct {
			Key string `json:"key"`
		} `json:"works"`
	}
	if !s.optionalJSON(ctx, s.olBase+"/isbn/"+isbn+".json", &ed) {
		return ""
	}
	if d := descriptionText(ed.Description); d != "" {
		return d
	}
	if len(ed.Works) == 0 || !strings.HasPrefix(ed.Works[0].Key, "/works/") || strings.ContainsAny(ed.Works[0].Key[len("/works/"):], "/?#.") {
		return ""
	}
	var w struct {
		Description json.RawMessage `json:"description"`
	}
	if !s.optionalJSON(ctx, s.olBase+ed.Works[0].Key+".json", &w) {
		return ""
	}
	return descriptionText(w.Description)
}

func (s *Service) optionalJSON(ctx context.Context, u string, into any) bool {
	data, err := s.c.get(ctx, u, maxJSON)
	if err != nil {
		if !isNotFound(err) {
			s.log.Warn("book lookup: optional data not fetched", "err", err)
		}
		return false
	}
	if err := json.Unmarshal(data, into); err != nil {
		s.log.Warn("book lookup: optional data unreadable", "err", err)
		return false
	}
	return true
}

// descriptionText reads Open Library's description, a string or {"value": ...}.
func descriptionText(raw json.RawMessage) string {
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return cleanText(str, maxDescription, true)
	}
	var obj struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return cleanText(obj.Value, maxDescription, true)
	}
	return ""
}

// cover fetches the ISBN's cover, asking for none when there isn't one
// (default=false), and keeps it. A missing, 1x1, unreadable or unfetchable
// cover is no cover: the book is still found.
func (s *Service) cover(ctx context.Context, actor int64, isbn string) string {
	data, err := s.c.get(ctx, s.cvBase+"/b/isbn/"+isbn+"-L.jpg?default=false", maxImage)
	if err != nil {
		if !isNotFound(err) {
			s.log.Warn("book lookup: cover not fetched", "isbn", isbn, "err", err)
		}
		return ""
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (cfg.Width <= 1 && cfg.Height <= 1) {
		return "" // not a picture, or the 1x1 placeholder
	}
	token, err := s.tmp.Store(ctx, bytes.NewReader(data), maxImage, media.Cover)
	if err != nil {
		s.log.Warn("book lookup: cover refused", "isbn", isbn, "err", err)
		return ""
	}
	s.remember(token, actor)
	return token
}

// remember records whose cover token is, dropping the oldest when too many wait.
func (s *Service) remember(token string, actor int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for t, p := range s.pending {
		if now.Sub(p.at) > coverTTL {
			s.dropLocked(t)
		}
	}
	for len(s.pending) >= maxPending {
		var oldest string
		for t, p := range s.pending {
			if oldest == "" || p.at.Before(s.pending[oldest].at) {
				oldest = t
			}
		}
		s.dropLocked(oldest)
	}
	s.pending[token] = pendingCover{user: actor, at: now}
}

func (s *Service) dropLocked(token string) {
	delete(s.pending, token)
	if err := s.tmp.Delete(token); err != nil {
		s.log.Warn("removing an unclaimed cover failed", "err", err)
	}
}

// owned reports whether token is a live cover held for actor.
func (s *Service) owned(token string, actor int64) bool {
	p, ok := s.pending[token]
	return ok && p.user == actor && s.now().Sub(p.at) <= coverTTL
}

// OpenCover opens the cover held under token for actor, to show a preview.
// A token that isn't theirs, is unknown or has expired is media.ErrNotFound.
func (s *Service) OpenCover(token string, actor int64) (*os.File, error) {
	s.mu.Lock()
	ok := s.owned(token, actor)
	s.mu.Unlock()
	if !ok {
		return nil, media.ErrNotFound
	}
	return s.tmp.Open(token)
}

// TakeCover moves the cover held under token for actor into the library's
// images and returns its stored name, for the book being saved. The token is
// spent: a second try is media.ErrNotFound.
func (s *Service) TakeCover(token string, actor int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.owned(token, actor) {
		return "", media.ErrNotFound
	}
	if err := s.images.Adopt(s.tmp, token); err != nil {
		return "", err
	}
	delete(s.pending, token)
	return token, nil
}

// cleanText trims s, drops control and text-direction characters (newlines and
// tabs stay in a description, as newlines) and cuts it to max characters.
func cleanText(s string, max int, multiline bool) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			if multiline {
				b.WriteRune(r)
			} else {
				b.WriteRune(' ')
			}
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), r == utf8.RuneError:
			// dropped
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if utf8.RuneCountInString(out) > max {
		out = strings.TrimSpace(string([]rune(out)[:max]))
	}
	return out
}
