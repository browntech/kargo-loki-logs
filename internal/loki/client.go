// Package loki is a minimal, read-only client for the Grafana Loki HTTP API.
// It calls only query_range and index/volume_range.
package loki

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// maxErrorBody caps how much of a Loki error response is kept in an error.
const maxErrorBody = 512

// Client calls one Loki instance.
type Client struct {
	// BaseURL is the Loki root, for example http://loki.example:3100.
	BaseURL *url.URL
	// Tenant, when set, is sent as X-Scope-OrgID.
	Tenant string
	// HTTP is the client used for requests. It must not be nil.
	HTTP *http.Client
}

// Entry is one log line with its timestamp.
type Entry struct {
	Time time.Time
	Line string
}

// StatusError is returned when Loki answers with a non-2xx status.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("loki returned HTTP %d: %s", e.Code, e.Body)
}

// QueryRange runs a log query over [start, end] in forward direction and
// returns the entries of all streams merged and sorted oldest first.
func (c *Client) QueryRange(ctx context.Context, query string, start, end time.Time, limit int) ([]Entry, error) {
	v := url.Values{}
	v.Set("query", query)
	v.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	v.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	v.Set("limit", strconv.Itoa(limit))
	v.Set("direction", "forward")

	var resp struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Values [][2]string `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := c.get(ctx, "/loki/api/v1/query_range", v, &resp); err != nil {
		return nil, err
	}
	if resp.Data.ResultType != "streams" {
		return nil, fmt.Errorf("loki query_range: unexpected resultType %q", resp.Data.ResultType)
	}

	var entries []Entry
	for _, s := range resp.Data.Result {
		for _, val := range s.Values {
			ns, err := strconv.ParseInt(val[0], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("loki query_range: bad timestamp %q: %w", val[0], err)
			}
			entries = append(entries, Entry{Time: time.Unix(0, ns), Line: val[1]})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Time.Before(entries[j].Time) })
	return entries, nil
}

// Window is the time range in which a selector has data.
type Window struct {
	Start, End time.Time
}

// ErrNoData means the volume lookup found no entries for the selector.
var ErrNoData = errors.New("no data for selector")

// VolumeWindow asks index/volume_range for the selector's volume over
// [start, end] in buckets of step, and returns the span of the non-empty
// buckets, padded by one step on each side. It reads only the index, so it is
// much cheaper than a log query over the same range.
//
// Loki leaves out empty buckets. When no series has more than one non-empty
// bucket, it answers with a "vector" (one "value" per series) instead of a
// "matrix" ("values"). A short Job usually fits in one bucket, so both forms
// are read.
func (c *Client) VolumeWindow(ctx context.Context, selector string, start, end time.Time, step time.Duration) (Window, error) {
	v := url.Values{}
	v.Set("query", selector)
	v.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	v.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	v.Set("step", strconv.FormatFloat(step.Seconds(), 'f', -1, 64))

	var resp struct {
		Data struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Value  *[2]json.RawMessage  `json:"value"`
				Values [][2]json.RawMessage `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := c.get(ctx, "/loki/api/v1/index/volume_range", v, &resp); err != nil {
		return Window{}, err
	}

	var samples [][2]json.RawMessage
	switch resp.Data.ResultType {
	case "matrix":
		for _, s := range resp.Data.Result {
			samples = append(samples, s.Values...)
		}
	case "vector":
		for _, s := range resp.Data.Result {
			if s.Value != nil {
				samples = append(samples, *s.Value)
			}
		}
	default:
		return Window{}, fmt.Errorf("loki volume_range: unexpected resultType %q", resp.Data.ResultType)
	}

	var first, last time.Time
	for _, val := range samples {
		var ts float64
		if err := json.Unmarshal(val[0], &ts); err != nil {
			return Window{}, fmt.Errorf("loki volume_range: bad timestamp %s: %w", val[0], err)
		}
		var sval string
		if err := json.Unmarshal(val[1], &sval); err != nil {
			return Window{}, fmt.Errorf("loki volume_range: bad value %s: %w", val[1], err)
		}
		n, err := strconv.ParseFloat(sval, 64)
		if err != nil {
			return Window{}, fmt.Errorf("loki volume_range: bad value %q: %w", sval, err)
		}
		if n <= 0 {
			continue
		}
		t := time.Unix(0, int64(ts*float64(time.Second)))
		if first.IsZero() || t.Before(first) {
			first = t
		}
		if t.After(last) {
			last = t
		}
	}
	if first.IsZero() {
		return Window{}, ErrNoData
	}

	w := Window{Start: first.Add(-step), End: last.Add(step)}
	if w.Start.Before(start) {
		w.Start = start
	}
	if w.End.After(end) {
		w.End = end
	}
	return w, nil
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	u := c.BaseURL.JoinPath(path)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	if c.Tenant != "" {
		req.Header.Set("X-Scope-OrgID", c.Tenant)
	}
	req.Header.Set("Accept", "application/json")

	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode < 200 || res.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBody))
		return &StatusError{Code: res.StatusCode, Body: strings.TrimSpace(string(b))}
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("loki %s: decode response: %w", path, err)
	}
	return nil
}
