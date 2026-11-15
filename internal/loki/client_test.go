package loki_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/browntech/kargo-loki-logs/internal/loki"
)

func volumeClient(t *testing.T, body string) *loki.Client {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loki/api/v1/index/volume_range" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(ts.Close)
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &loki.Client{BaseURL: u, HTTP: ts.Client()}
}

func secs(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }

func TestVolumeWindow(t *testing.T) {
	end := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	start := end.Add(-720 * time.Hour)
	step := time.Hour
	b1 := end.Add(-3 * time.Hour)
	b2 := end.Add(-2 * time.Hour)

	tests := []struct {
		name       string
		body       string
		want       loki.Window
		wantNoData bool
		wantErr    bool
	}{
		{
			name: "matrix spans the non-empty buckets",
			body: `{"status":"success","data":{"resultType":"matrix","result":[
			 {"metric":{},"values":[[` + secs(b1.Add(-step)) + `,"0"],[` + secs(b1) + `,"120"],[` + secs(b2) + `,"40"]]}]}}`,
			want: loki.Window{Start: b1.Add(-step), End: b2.Add(step)},
		},
		{
			// Loki 3.6 answers with a vector when each series has one non-empty bucket.
			name: "vector with one bucket",
			body: `{"status":"success","data":{"resultType":"vector","result":[
			 {"metric":{},"value":[` + secs(b1) + `,"1024"]}]}}`,
			want: loki.Window{Start: b1.Add(-step), End: b1.Add(step)},
		},
		{
			name: "vector bucket at the range end is clamped",
			body: `{"status":"success","data":{"resultType":"vector","result":[
			 {"metric":{},"value":[` + secs(end) + `,"1024"]}]}}`,
			want: loki.Window{Start: end.Add(-step), End: end},
		},
		{
			name: "vector from several series",
			body: `{"status":"success","data":{"resultType":"vector","result":[
			 {"metric":{"a":"1"},"value":[` + secs(b2) + `,"5"]},
			 {"metric":{"a":"2"},"value":[` + secs(b1) + `,"7"]}]}}`,
			want: loki.Window{Start: b1.Add(-step), End: b2.Add(step)},
		},
		{
			name: "vector skips a zero-volume series",
			body: `{"status":"success","data":{"resultType":"vector","result":[
			 {"metric":{"a":"1"},"value":[` + secs(b1) + `,"0"]},
			 {"metric":{"a":"2"},"value":[` + secs(b2) + `,"9"]}]}}`,
			want: loki.Window{Start: b2.Add(-step), End: b2.Add(step)},
		},
		{
			name:       "vector with a null value",
			body:       `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":null}]}}`,
			wantNoData: true,
		},
		{
			name:       "empty vector",
			body:       `{"status":"success","data":{"resultType":"vector","result":[]}}`,
			wantNoData: true,
		},
		{
			name:       "vector with zero volume",
			body:       `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[` + secs(b1) + `,"0"]}]}}`,
			wantNoData: true,
		},
		{
			name:    "unknown resultType",
			body:    `{"status":"success","data":{"resultType":"scalar","result":[]}}`,
			wantErr: true,
		},
		{
			name:    "bad vector value",
			body:    `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[` + secs(b1) + `,"x"]}]}}`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := volumeClient(t, tt.body).VolumeWindow(context.Background(), `{job="j"}`, start, end, step)
			switch {
			case tt.wantNoData:
				if !errors.Is(err, loki.ErrNoData) {
					t.Fatalf("err = %v, want ErrNoData", err)
				}
			case tt.wantErr:
				if err == nil || errors.Is(err, loki.ErrNoData) {
					t.Fatalf("err = %v, want a parse error", err)
				}
			default:
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				if !got.Start.Equal(tt.want.Start) || !got.End.Equal(tt.want.End) {
					t.Errorf("window = %v..%v, want %v..%v", got.Start, got.End, tt.want.Start, tt.want.End)
				}
			}
		})
	}
}
