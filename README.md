# kargo-loki-logs

An adapter that implements [Kargo](https://kargo.io)'s AnalysisRun log API
(`api.rollouts.logs.urlTemplate`) with [Grafana Loki](https://grafana.com/oss/loki/)
behind it. It returns a verification Job's log lines as plain text, so the Kargo UI
shows readable logs instead of Loki's JSON.

## What it does

`GET /logs/{jobNamespace}/{jobName}/{container}` returns `text/plain`, one line per
log entry, oldest first. For each request the adapter:

1. Checks each path parameter against the Kubernetes name pattern
   (`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`, at most 253 characters). Anything else gets
   `400` and Loki is not called.
2. Builds the LogQL stream selector
   `{namespace="<jobNamespace>", pod=~"<jobName>-.*", container="<container>"}`.
   Regex metacharacters in the Job name (`.`) are escaped. The label names are
   configurable.
3. Finds the Job's time window with one `index/volume_range` call over
   `WINDOW_LOOKBACK`. This reads only the Loki index, so it stays fast over a long
   range. If the lookup fails or finds nothing, the log query uses the last
   `DEFAULT_SINCE` instead.
4. Calls `query_range` over that window with `direction=forward` and `limit`, merges
   the streams (a retried pod is a second stream) by timestamp, and writes only the
   lines.

The adapter only reads from Loki. `GET /healthz` returns `ok` for probes.

## Configuration

Each setting is an environment variable; a flag of the same meaning overrides it
(`kargo-loki-logs -h` lists the flags).

| Env var | Flag | Default | Meaning |
|---|---|---|---|
| `LOKI_URL` | `-loki-url` | (required) | Loki base URL, for example `http://loki.example:3100` |
| `LOKI_TENANT` | `-tenant` | empty | Sent as `X-Scope-OrgID`; empty sends no header |
| `LOKI_LABEL_NAMESPACE` | `-namespace-label` | `namespace` | Loki label that holds the namespace |
| `LOKI_LABEL_POD` | `-pod-label` | `pod` | Loki label that holds the pod name |
| `LOKI_LABEL_CONTAINER` | `-container-label` | `container` | Loki label that holds the container name |
| `DEFAULT_SINCE` | `-since` | `1h` | Range of the log query when the window lookup finds no window |
| `LIMIT` | `-limit` | `5000` | Maximum lines returned |
| `AUTH_TOKEN_FILE` | `-auth-token-file` | empty | File that holds the token callers must send; empty turns auth off |
| `LISTEN_ADDR` | `-listen` | `:8080` | Listen address |
| `WINDOW_LOOKBACK` | `-window-lookback` | `720h` | How far back the window lookup searches; set it to your Loki retention |
| `WINDOW_STEP` | `-window-step` | `1h` | Bucket size of the window lookup; the window is padded by one bucket each side |
| `LOKI_TIMEOUT` | `-loki-timeout` | `30s` | Timeout of each Loki request |
| `LOG_LEVEL` | `-log-level` | `info` | `debug`, `info`, `warn` or `error` |

When a token is set, a request must send it in the `Authorization` header, as
`Bearer <token>` or as the bare token. A missing or wrong token gets `401`. The
compare takes constant time. The token file is read once at start.

The window lookup needs Loki's volume endpoint (`volume_enabled: true` in
`limits_config`). Without it every request falls back to `DEFAULT_SINCE`.

## Kargo values

Point Kargo's API server at the adapter in the Kargo Helm chart values:

```yaml
api:
  rollouts:
    logs:
      enabled: true
      urlTemplate: "http://kargo-loki-logs.logging.svc.cluster.local:8080/logs/${{jobNamespace}}/${{jobName}}/${{container}}"
      # Optional, when AUTH_TOKEN_FILE is set on the adapter:
      tokenSecret:
        name: kargo-loki-logs-token
        key: token
```

`kargo-loki-logs-token` is a Secret you create in Kargo's namespace. Mount the same
token into the adapter and set `AUTH_TOKEN_FILE` to its path. Kargo streams the
adapter's response body to the UI unchanged.

## Build

```sh
go test ./...
docker buildx build --platform linux/amd64,linux/arm64 -t kargo-loki-logs .
```

The image is distroless static and runs as UID 65532. The build cross-compiles with
`GOARCH`, so it needs no QEMU.

## Contributing

Issues are welcome. Outside pull requests are not accepted; a pull request from
outside the project is closed with a pointer to the issue tracker.

## License

[MIT](LICENSE)
