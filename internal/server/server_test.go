package server_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/browntech/kargo-loki-logs/internal/loki"
	"github.com/browntech/kargo-loki-logs/internal/server"
)

// fakeLoki records the requests it gets and answers with canned bodies.
type fakeLoki struct {
	mu         sync.Mutex
	requests   []*http.Request
	volumeCode int
	volumeBody string
	queryBody  string
}

func (f *fakeLoki) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/loki/api/v1/index/volume_range":
		if f.volumeCode != 0 {
			w.WriteHeader(f.volumeCode)
		}
		_, _ = io.WriteString(w, f.volumeBody)
	case "/loki/api/v1/query_range":
		_, _ = io.WriteString(w, f.queryBody)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeLoki) calls() []*http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*http.Request(nil), f.requests...)
}

func (f *fakeLoki) find(path string) *http.Request {
	for _, r := range f.calls() {
		if r.URL.Path == path {
			return r
		}
	}
	return nil
}

var now = time.Date(2030, 1, 2, 12, 0, 0, 0, time.UTC)

// Two streams (for example a retried pod) whose entries interleave in time.
const twoStreams = `{"status":"success","data":{"resultType":"streams","result":[
 {"stream":{"pod":"verify-abc-1"},"values":[["3000000000","third"],["1000000000","first\n"]]},
 {"stream":{"pod":"verify-abc-2"},"values":[["2000000000","second"]]}
]}}`

const emptyVolume = `{"status":"success","data":{"resultType":"matrix","result":[]}}`

func newServer(t *testing.T, f *fakeLoki, token string) *httptest.Server {
	t.Helper()
	lokiSrv := httptest.NewServer(f)
	t.Cleanup(lokiSrv.Close)
	u, _ := url.Parse(lokiSrv.URL)
	h := server.New(server.Config{
		Source:     &loki.Client{BaseURL: u, Tenant: "team-a", HTTP: lokiSrv.Client()},
		Labels:     server.Labels{Namespace: "namespace", Pod: "pod", Container: "container"},
		Since:      time.Hour,
		Lookback:   720 * time.Hour,
		Limit:      100,
		WindowStep: time.Hour,
		Token:      token,
		Now:        func() time.Time { return now },
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, srv *httptest.Server, path, auth string) (int, string, http.Header) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	// Do not follow redirects: a redirect would hide a missing 400.
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b), res.Header
}

func TestValidRequestReturnsPlainLinesOldestFirst(t *testing.T) {
	f := &fakeLoki{volumeBody: emptyVolume, queryBody: twoStreams}
	srv := newServer(t, f, "")

	code, body, hdr := get(t, srv, "/logs/kargo-proj/verify-abc/main", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, body %q", code, body)
	}
	if ct := hdr.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
	if want := "first\nsecond\nthird\n"; body != want {
		t.Errorf("body = %q, want %q", body, want)
	}

	q := f.find("/loki/api/v1/query_range")
	if q == nil {
		t.Fatal("no query_range call")
	}
	p := q.URL.Query()
	if got := p.Get("direction"); got != "forward" {
		t.Errorf("direction = %q, want forward", got)
	}
	if got := p.Get("limit"); got != "100" {
		t.Errorf("limit = %q, want 100", got)
	}
	if want := `{namespace="kargo-proj", pod=~"verify-abc-([0-9]+-)?[a-z0-9]{5}", container="main"}`; p.Get("query") != want {
		t.Errorf("query = %q, want %q", p.Get("query"), want)
	}
	if got := q.Header.Get("X-Scope-OrgID"); got != "team-a" {
		t.Errorf("X-Scope-OrgID = %q, want team-a", got)
	}
	// No volume data: the default since window is used.
	if got, want := p.Get("start"), ns(now.Add(-time.Hour)); got != want {
		t.Errorf("start = %s, want %s", got, want)
	}
	if got, want := p.Get("end"), ns(now); got != want {
		t.Errorf("end = %s, want %s", got, want)
	}
}

func TestInvalidPathParametersReturn400WithoutLokiCall(t *testing.T) {
	long := strings.Repeat("a", 254)
	cases := map[string]string{
		"uppercase namespace":  "/logs/Kargo/job/main",
		"uppercase job":        "/logs/ns/Job/main",
		"uppercase container":  "/logs/ns/job/MAIN",
		"quote in namespace":   "/logs/ns%22/job/main",
		"quote in job":         "/logs/ns/job%22/main",
		"quote in container":   "/logs/ns/job/main%22",
		"brace in namespace":   "/logs/ns%7D/job/main",
		"brace in job":         "/logs/ns/job%7D/main",
		"brace in container":   "/logs/ns/job/main%7D",
		"pipe in namespace":    "/logs/ns%7C/job/main",
		"pipe in job":          "/logs/ns/job%7C/main",
		"pipe in container":    "/logs/ns/job/main%7C",
		"empty namespace":      "/logs//job/main",
		"empty job":            "/logs/ns//main",
		"empty container":      "/logs/ns/job/",
		"over-long namespace":  "/logs/" + long + "/job/main",
		"over-long job":        "/logs/ns/" + long + "/main",
		"over-long container":  "/logs/ns/job/" + long,
		"regex in job":         "/logs/ns/job.%2A/main",
		"backslash in job":     "/logs/ns/job%5C/main",
		"encoded slash in job": "/logs/ns/a%2Fb/main",
		"too few segments":     "/logs/ns/job",
		"too many segments":    "/logs/ns/job/main/extra",
		"leading dash":         "/logs/ns/-job/main",
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakeLoki{volumeBody: emptyVolume, queryBody: twoStreams}
			srv := newServer(t, f, "")
			code, body, _ := get(t, srv, path, "")
			if code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 (body %q)", code, body)
			}
			if n := len(f.calls()); n != 0 {
				t.Errorf("Loki got %d calls, want 0", n)
			}
		})
	}
}

func TestMaxLengthNameAccepted(t *testing.T) {
	f := &fakeLoki{volumeBody: emptyVolume, queryBody: twoStreams}
	srv := newServer(t, f, "")
	code, body, _ := get(t, srv, "/logs/ns/"+strings.Repeat("a", 253)+"/main", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, body %q", code, body)
	}
}

func TestDotInJobNameIsEscaped(t *testing.T) {
	f := &fakeLoki{volumeBody: emptyVolume, queryBody: twoStreams}
	srv := newServer(t, f, "")

	if code, body, _ := get(t, srv, "/logs/ns/verify.v1.2/main", ""); code != http.StatusOK {
		t.Fatalf("status = %d, body %q", code, body)
	}
	want := `{namespace="ns", pod=~"verify\\.v1\\.2-([0-9]+-)?[a-z0-9]{5}", container="main"}`
	for _, path := range []string{"/loki/api/v1/index/volume_range", "/loki/api/v1/query_range"} {
		r := f.find(path)
		if r == nil {
			t.Fatalf("no call to %s", path)
		}
		if got := r.URL.Query().Get("query"); got != want {
			t.Errorf("%s query = %q, want %q", path, got, want)
		}
	}
}

func TestSelectorEscaping(t *testing.T) {
	l := server.Labels{Namespace: "k8s_namespace", Pod: "k8s_pod", Container: "k8s_container"}
	got := server.Selector(l, "ns", "a.b", "c")
	want := `{k8s_namespace="ns", k8s_pod=~"a\\.b-([0-9]+-)?[a-z0-9]{5}", k8s_container="c"}`
	if got != want {
		t.Errorf("Selector = %q, want %q", got, want)
	}
}

func TestPodPattern(t *testing.T) {
	short := "0a1b2c3d-4e5f-6789-abcd-ef0123456789.smoke.1" // 43 chars, Kargo-style
	long := strings.Repeat("a", 50) + "." + strings.Repeat("b", 20)
	cases := []struct {
		job, pattern string
		match, miss  []string
	}{
		{
			job:     short,
			pattern: `0a1b2c3d-4e5f-6789-abcd-ef0123456789\.smoke\.1-([0-9]+-)?[a-z0-9]{5}`,
			match:   []string{short + "-x7k2p", short + "-0-x7k2p", short + "-12-abcde"},
			miss:    []string{short + "-retry-x7k2p", short + "-x7k2", "0a1b2c3d-4e5f-6789-abcd-ef0123456789xsmoke.1-x7k2p"},
		},
		{
			job:     long,
			pattern: regexp.QuoteMeta((long + "-")[:58]) + `[a-z0-9]{5}`,
			match:   []string{(long + "-")[:58] + "x7k2p"},
			miss:    []string{long + "-x7k2p", (long + "-")[:58] + "x7k2"},
		},
	}
	for _, tc := range cases {
		got := server.PodPattern(tc.job)
		if got != tc.pattern {
			t.Errorf("PodPattern(%q) = %q, want %q", tc.job, got, tc.pattern)
		}
		re := regexp.MustCompile("^(?:" + got + ")$") // Loki anchors =~ matchers
		for _, n := range tc.match {
			if !re.MatchString(n) {
				t.Errorf("%q should match pod %q", got, n)
			}
		}
		for _, n := range tc.miss {
			if re.MatchString(n) {
				t.Errorf("%q should not match pod %q", got, n)
			}
		}
	}
	// The 58-char boundary: "<job>-" of exactly 58 chars keeps the long form.
	job57 := strings.Repeat("c", 57)
	if got, want := server.PodPattern(job57), job57+"-([0-9]+-)?[a-z0-9]{5}"; got != want {
		t.Errorf("PodPattern(57 chars) = %q, want %q", got, want)
	}
	job58 := strings.Repeat("c", 58)
	if got, want := server.PodPattern(job58), job58+"[a-z0-9]{5}"; got != want {
		t.Errorf("PodPattern(58 chars) = %q, want %q", got, want)
	}
}

func TestTruncationTrailer(t *testing.T) {
	f := &fakeLoki{volumeBody: emptyVolume, queryBody: twoStreams}
	lokiSrv := httptest.NewServer(f)
	defer lokiSrv.Close()
	u, _ := url.Parse(lokiSrv.URL)
	for _, tc := range []struct {
		limit int
		want  string
	}{
		{3, "first\nsecond\nthird\n[kargo-loki-logs: output truncated at 3 lines]\n"},
		{4, "first\nsecond\nthird\n"},
	} {
		h := server.New(server.Config{
			Source:     &loki.Client{BaseURL: u, HTTP: lokiSrv.Client()},
			Labels:     server.Labels{Namespace: "namespace", Pod: "pod", Container: "container"},
			Since:      time.Hour,
			Lookback:   720 * time.Hour,
			Limit:      tc.limit,
			WindowStep: time.Hour,
			Now:        func() time.Time { return now },
		})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/logs/ns/job/main", nil))
		if rec.Code != http.StatusOK || rec.Body.String() != tc.want {
			t.Errorf("limit %d: got %d %q, want %q", tc.limit, rec.Code, rec.Body.String(), tc.want)
		}
	}
}

func TestToken(t *testing.T) {
	const token = "s3cret-token"
	cases := []struct {
		name string
		auth string
		want int
	}{
		{"missing", "", http.StatusUnauthorized},
		{"wrong", "Bearer wrong", http.StatusUnauthorized},
		{"prefix only", "Bearer ", http.StatusUnauthorized},
		{"token prefix", "Bearer s3cret", http.StatusUnauthorized},
		{"bearer", "Bearer " + token, http.StatusOK},
		{"bearer lowercase", "bearer " + token, http.StatusOK},
		{"bare token", token, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeLoki{volumeBody: emptyVolume, queryBody: twoStreams}
			srv := newServer(t, f, token)
			code, body, _ := get(t, srv, "/logs/ns/job/main", tc.auth)
			if code != tc.want {
				t.Errorf("status = %d, want %d (body %q)", code, tc.want, body)
			}
			if tc.want == http.StatusUnauthorized && len(f.calls()) != 0 {
				t.Errorf("Loki called on an unauthorized request")
			}
		})
	}
}

func TestHealthzNeedsNoToken(t *testing.T) {
	srv := newServer(t, &fakeLoki{}, "tok")
	code, body, _ := get(t, srv, "/healthz", "")
	if code != http.StatusOK || body != "ok\n" {
		t.Errorf("healthz = %d %q", code, body)
	}
}

func TestWindowLookupNarrowsRange(t *testing.T) {
	// Volume in two hourly buckets: 09:00 and 10:00 on the test day.
	b1 := now.Add(-3 * time.Hour)
	b2 := now.Add(-2 * time.Hour)
	vol := `{"status":"success","data":{"resultType":"matrix","result":[
	 {"metric":{},"values":[[` + secs(b1.Add(-time.Hour)) + `,"0"],[` + secs(b1) + `,"120"],[` + secs(b2) + `,"40"]]}
	]}}`
	f := &fakeLoki{volumeBody: vol, queryBody: twoStreams}
	srv := newServer(t, f, "")

	if code, body, _ := get(t, srv, "/logs/ns/job/main", ""); code != http.StatusOK {
		t.Fatalf("status = %d, body %q", code, body)
	}

	v := f.find("/loki/api/v1/index/volume_range")
	if v == nil {
		t.Fatal("no volume_range call")
	}
	vp := v.URL.Query()
	if vp.Get("start") != ns(now.Add(-720*time.Hour)) || vp.Get("end") != ns(now) || vp.Get("step") != "3600" {
		t.Errorf("volume_range params = %v", vp)
	}

	q := f.find("/loki/api/v1/query_range").URL.Query()
	if got, want := q.Get("start"), ns(b1.Add(-time.Hour)); got != want {
		t.Errorf("start = %s, want %s", got, want)
	}
	if got, want := q.Get("end"), ns(b2.Add(time.Hour)); got != want {
		t.Errorf("end = %s, want %s", got, want)
	}
}

func TestWindowLookupFailureFallsBackToSince(t *testing.T) {
	f := &fakeLoki{volumeCode: http.StatusBadRequest, volumeBody: "volume disabled", queryBody: twoStreams}
	srv := newServer(t, f, "")

	code, body, _ := get(t, srv, "/logs/ns/job/main", "")
	if code != http.StatusOK || body != "first\nsecond\nthird\n" {
		t.Fatalf("got %d %q", code, body)
	}
	q := f.find("/loki/api/v1/query_range").URL.Query()
	if got, want := q.Get("start"), ns(now.Add(-time.Hour)); got != want {
		t.Errorf("start = %s, want %s", got, want)
	}
}

func TestLokiQueryErrorReturns502(t *testing.T) {
	f := &fakeLoki{volumeBody: emptyVolume, queryBody: `not json`}
	srv := newServer(t, f, "")
	if code, _, _ := get(t, srv, "/logs/ns/job/main", ""); code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", code)
	}
}

func TestMethodAndUnknownPath(t *testing.T) {
	srv := newServer(t, &fakeLoki{}, "")
	res, err := http.Post(srv.URL+"/logs/ns/job/main", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", res.StatusCode)
	}
	if code, _, _ := get(t, srv, "/other", ""); code != http.StatusNotFound {
		t.Errorf("unknown path status = %d, want 404", code)
	}
}

func ns(t time.Time) string   { return strconv.FormatInt(t.UnixNano(), 10) }
func secs(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }
