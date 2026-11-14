package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	c, err := Parse(nil, envMap(map[string]string{"LOKI_URL": "http://loki.example:3100"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != ":8080" || c.NamespaceLabel != "namespace" || c.PodLabel != "pod" ||
		c.ContainerLabel != "container" || c.Since != time.Hour || c.Lookback != 720*time.Hour || c.Limit != 5000 ||
		c.WindowStep != time.Hour || c.Token != "" {
		t.Errorf("unexpected defaults: %+v", c)
	}
}

func TestFlagsOverrideEnv(t *testing.T) {
	c, err := Parse([]string{"-limit", "10", "-pod-label", "k8s_pod"},
		envMap(map[string]string{"LOKI_URL": "http://loki.example:3100", "LIMIT": "20"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Limit != 10 || c.PodLabel != "k8s_pod" {
		t.Errorf("got limit %d pod label %q", c.Limit, c.PodLabel)
	}
}

func TestTokenFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte("abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Parse([]string{"-auth-token-file", p}, envMap(map[string]string{"LOKI_URL": "https://loki.example"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Token != "abc" {
		t.Errorf("token = %q", c.Token)
	}
}

func TestInvalid(t *testing.T) {
	base := map[string]string{"LOKI_URL": "http://loki.example:3100"}
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{
		"bad label":     {"-pod-label", `pod"}`},
		"zero limit":    {"-limit", "0"},
		"bad since":     {"-since", "forever"},
		"negative step": {"-window-step", "-1h"},
		"bad level":     {"-log-level", "loud"},
		"missing token": {"-auth-token-file", filepath.Join(t.TempDir(), "nope")},
		"empty token":   {"-auth-token-file", empty},
		"bad url":       {"-loki-url", "loki.example"},
		"extra arg":     {"extra"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(args, envMap(base)); err == nil {
				t.Error("want error")
			}
		})
	}
	if _, err := Parse(nil, envMap(nil)); err == nil {
		t.Error("missing Loki URL: want error")
	}
}
