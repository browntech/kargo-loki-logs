// Package config reads the adapter's settings from flags, with environment
// variables as defaults.
package config

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Config holds every setting.
type Config struct {
	ListenAddr     string
	LokiURL        *url.URL
	Tenant         string
	NamespaceLabel string
	PodLabel       string
	ContainerLabel string
	Since          time.Duration
	Lookback       time.Duration
	Limit          int
	WindowStep     time.Duration
	LokiTimeout    time.Duration
	TokenFile      string
	Token          string
	LogLevel       string
}

var labelPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// Parse reads args (without the program name) and env. getenv is usually
// os.Getenv.
func Parse(args []string, getenv func(string) string) (*Config, error) {
	fs := flag.NewFlagSet("kargo-loki-logs", flag.ContinueOnError)
	env := func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return def
	}

	listen := fs.String("listen", env("LISTEN_ADDR", ":8080"), "listen address (env LISTEN_ADDR)")
	lokiURL := fs.String("loki-url", env("LOKI_URL", ""), "Loki base URL, e.g. http://loki.example:3100 (env LOKI_URL, required)")
	tenant := fs.String("tenant", env("LOKI_TENANT", ""), "Loki tenant sent as X-Scope-OrgID (env LOKI_TENANT)")
	nsLabel := fs.String("namespace-label", env("LOKI_LABEL_NAMESPACE", "namespace"), "Loki label for the namespace (env LOKI_LABEL_NAMESPACE)")
	podLabel := fs.String("pod-label", env("LOKI_LABEL_POD", "pod"), "Loki label for the pod (env LOKI_LABEL_POD)")
	ctrLabel := fs.String("container-label", env("LOKI_LABEL_CONTAINER", "container"), "Loki label for the container (env LOKI_LABEL_CONTAINER)")
	since := fs.String("since", env("DEFAULT_SINCE", "1h"), "how far back the log query looks when the window lookup finds no window (env DEFAULT_SINCE)")
	limit := fs.String("limit", env("LIMIT", "5000"), "maximum lines returned (env LIMIT)")
	lookback := fs.String("window-lookback", env("WINDOW_LOOKBACK", "720h"), "how far back the window lookup searches (env WINDOW_LOOKBACK)")
	step := fs.String("window-step", env("WINDOW_STEP", "1h"), "bucket size of the volume lookup (env WINDOW_STEP)")
	timeout := fs.String("loki-timeout", env("LOKI_TIMEOUT", "30s"), "timeout of each Loki request (env LOKI_TIMEOUT)")
	tokenFile := fs.String("token-file", env("LOKI_BEARER_TOKEN_FILE", ""), "file holding the token callers must send in Authorization (env LOKI_BEARER_TOKEN_FILE)")
	logLevel := fs.String("log-level", env("LOG_LEVEL", "info"), "debug, info, warn or error (env LOG_LEVEL)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}

	c := &Config{
		ListenAddr:     *listen,
		Tenant:         *tenant,
		NamespaceLabel: *nsLabel,
		PodLabel:       *podLabel,
		ContainerLabel: *ctrLabel,
		TokenFile:      *tokenFile,
		LogLevel:       *logLevel,
	}

	if *lokiURL == "" {
		return nil, errors.New("loki URL is required (-loki-url or LOKI_URL)")
	}
	u, err := url.Parse(*lokiURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("loki URL %q must be an absolute http(s) URL", *lokiURL)
	}
	c.LokiURL = u

	for name, v := range map[string]string{"namespace-label": c.NamespaceLabel, "pod-label": c.PodLabel, "container-label": c.ContainerLabel} {
		if !labelPattern.MatchString(v) {
			return nil, fmt.Errorf("%s %q is not a valid Loki label name", name, v)
		}
	}

	if c.Since, err = positiveDuration("since", *since); err != nil {
		return nil, err
	}
	if c.Lookback, err = positiveDuration("window-lookback", *lookback); err != nil {
		return nil, err
	}
	if c.WindowStep, err = positiveDuration("window-step", *step); err != nil {
		return nil, err
	}
	if c.LokiTimeout, err = positiveDuration("loki-timeout", *timeout); err != nil {
		return nil, err
	}
	if c.Limit, err = strconv.Atoi(*limit); err != nil || c.Limit < 1 {
		return nil, fmt.Errorf("limit %q must be a positive integer", *limit)
	}

	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return nil, fmt.Errorf("log-level %q must be debug, info, warn or error", c.LogLevel)
	}

	if c.TokenFile != "" {
		b, err := os.ReadFile(c.TokenFile)
		if err != nil {
			return nil, fmt.Errorf("read token file: %w", err)
		}
		c.Token = strings.TrimSpace(string(b))
		if c.Token == "" {
			return nil, fmt.Errorf("token file %s is empty", c.TokenFile)
		}
	}
	return c, nil
}

func positiveDuration(name, v string) (time.Duration, error) {
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s %q must be a positive duration such as 1h", name, v)
	}
	return d, nil
}
