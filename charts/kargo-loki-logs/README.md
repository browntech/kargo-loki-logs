# kargo-loki-logs Helm chart

Kargo AnalysisRun log adapter that serves Loki logs as plain text.

![Version: 0.1.0](https://img.shields.io/badge/Version-0.1.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.1.0](https://img.shields.io/badge/AppVersion-0.1.0-informational?style=flat-square)

The adapter serves Kargo's AnalysisRun log API and reads the lines from Loki. Kargo shows the
result as plain text.

## Install

```sh
helm install kargo-loki-logs oci://ghcr.io/browntech/charts/kargo-loki-logs \
  --namespace kargo \
  --set config.lokiUrl=http://loki.example:3100
```

Pin the image by digest in production:

```sh
--set image.digest=sha256:<digest>
```

With a digest set, the image renders as `ghcr.io/browntech/kargo-loki-logs@sha256:<digest>` and
`image.tag` is ignored.

## Environment variables

The chart passes the adapter configuration as environment variables:

| Variable | Value |
|---|---|
| `LISTEN_ADDR` | `:<config.port>` |
| `LOKI_URL` | `config.lokiUrl` |
| `LOKI_TENANT` | `config.tenant` (set only when not empty; sent as `X-Scope-OrgID`) |
| `LOKI_LABEL_NAMESPACE` | `config.labels.namespace` |
| `LOKI_LABEL_POD` | `config.labels.pod` |
| `LOKI_LABEL_CONTAINER` | `config.labels.container` |
| `DEFAULT_SINCE` | `config.defaultSince` |
| `LIMIT` | `config.limit` |
| `AUTH_TOKEN_FILE` | `<bearerToken.mountPath>/<bearerToken.secretKey>` (set only when `bearerToken.enabled`) |

## Access control

The adapter can read any log line its Loki tenant holds. Two controls limit who can ask it:

- `bearerToken`: callers must send `Authorization: Bearer <token>`. A request without the
  correct token gets 401.
- `networkPolicy`: only pods in the selected namespaces can open a connection.

Both are off by default. Enable both. With neither, any pod that can reach the Service can read
any log through the adapter.

- Keep `service.type` at `ClusterIP`. Any other type exposes the adapter outside the cluster.
- Put secrets in `extraEnv` only as `valueFrom.secretKeyRef`, never as a literal `value`: a
  literal is readable by anyone who can read the Deployment.

## Bearer token

To require a bearer token from callers, create a Secret and point the chart at it:

```sh
kubectl -n kargo create secret generic adapter-token --from-file=token=./token
helm upgrade --install kargo-loki-logs oci://ghcr.io/browntech/charts/kargo-loki-logs \
  --namespace kargo \
  --set config.lokiUrl=http://loki.example:3100 \
  --set bearerToken.enabled=true \
  --set bearerToken.existingSecret=adapter-token
```

The chart mounts the Secret read-only and sets `AUTH_TOKEN_FILE` to the file path. The adapter
then answers 401 to any request that does not send `Authorization: Bearer <token>`. The token
value never appears in the pod spec. Configure Kargo to send the same token with each log
request (its `httpHeaders` / `tokenSecret` settings for the log endpoint).

## NetworkPolicy

Set `networkPolicy.enabled=true` to restrict the adapter pods:

- **Ingress:** only from pods that match `networkPolicy.ingress.namespaceSelector` (and
  `podSelector`, when set), on the HTTP port. With both empty, no ingress is allowed. A
  `podSelector` set without a `namespaceSelector` matches pods in the release namespace only.
- **Egress:** to the peers in `networkPolicy.egress.loki` on `networkPolicy.egress.lokiPort`,
  and to cluster DNS on port 53. Nothing else. `networkPolicy.egress.loki` must list at least
  one peer, or the render fails.

```yaml
networkPolicy:
  enabled: true
  ingress:
    namespaceSelector:
      matchLabels:
        kubernetes.io/metadata.name: kargo
  egress:
    loki:
      - namespaceSelector:
          matchLabels:
            kubernetes.io/metadata.name: loki
        podSelector:
          matchLabels:
            app.kubernetes.io/name: loki
    lokiPort: 3100
```

## Security

The pod runs as non-root (UID 65532), with a read-only root filesystem, no privilege
escalation, all capabilities dropped and the `RuntimeDefault` seccomp profile. The ServiceAccount
token is not mounted: the adapter does not call the Kubernetes API.

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` | Affinity. |
| bearerToken.enabled | bool | `false` | Require callers to send the token. Mounts it from an existing Secret and passes its path to the adapter. |
| bearerToken.existingSecret | string | `""` | Name of the existing Secret that holds the token. |
| bearerToken.mountPath | string | `"/var/run/secrets/kargo-loki-logs"` | Directory the Secret is mounted at. |
| bearerToken.secretKey | string | `"token"` | Key in the Secret that holds the token. |
| config.defaultSince | string | `"1h"` | Default look-back window when a request gives none (`DEFAULT_SINCE`), as a Go duration. |
| config.labels.container | string | `"container"` | Container label name (`LOKI_LABEL_CONTAINER`). |
| config.labels.namespace | string | `"namespace"` | Namespace label name (`LOKI_LABEL_NAMESPACE`). |
| config.labels.pod | string | `"pod"` | Pod label name (`LOKI_LABEL_POD`). |
| config.limit | int | `5000` | Maximum log lines returned per request (`LIMIT`). |
| config.lokiUrl | string | `"http://loki.example:3100"` | Loki base URL (`LOKI_URL`). Required. |
| config.port | int | `8080` | Port the adapter listens on. Renders `LISTEN_ADDR` as `:<port>`. |
| config.tenant | string | `""` | Loki tenant sent as `X-Scope-OrgID` (`LOKI_TENANT`). Empty sends no header. |
| extraEnv | list | `[]` | Extra environment variables for the adapter container. Put a secret here only as `valueFrom.secretKeyRef`, never as a literal `value`. |
| fullnameOverride | string | `""` | Override the full resource name. |
| image.digest | string | `""` | Image digest (`sha256:...`). When set, the image renders as `repository@digest` and `tag` is ignored. |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy. |
| image.repository | string | `"ghcr.io/browntech/kargo-loki-logs"` | Image repository. |
| image.tag | string | `""` | Image tag. Defaults to the chart appVersion when empty. |
| imagePullSecrets | list | `[]` | Image pull secrets for a private registry mirror. |
| livenessProbe | object | `{"httpGet":{"path":"/healthz","port":"http"},"initialDelaySeconds":5,"periodSeconds":20}` | Liveness probe on `/healthz`. |
| nameOverride | string | `""` | Override the chart name in resource names. |
| networkPolicy.egress.dns | object | `{"enabled":true,"namespaceSelector":{"matchLabels":{"kubernetes.io/metadata.name":"kube-system"}},"podSelector":{"matchLabels":{"k8s-app":"kube-dns"}}}` | Allow DNS to kube-dns / CoreDNS. |
| networkPolicy.egress.loki | list | `[]` | Peers that reach Loki (namespaceSelector / podSelector / ipBlock entries). |
| networkPolicy.egress.lokiPort | int | `3100` | Loki port. |
| networkPolicy.enabled | bool | `false` | Render a NetworkPolicy for the adapter pods. Recommended together with `bearerToken`: with neither, any pod that can reach the Service can read any log through the adapter. Needs at least one `egress.loki` peer. |
| networkPolicy.ingress.namespaceSelector | object | `{}` | Namespace selector for clients allowed to reach the adapter (for example the Kargo API). Empty allows no ingress. |
| networkPolicy.ingress.podSelector | object | `{}` | Optional pod selector, combined with `namespaceSelector`. Set alone, it matches pods in the release namespace only. |
| nodeSelector | object | `{}` | Node selector. |
| podAnnotations | object | `{}` | Extra pod annotations. |
| podLabels | object | `{}` | Extra pod labels. |
| podSecurityContext | object | `{"fsGroup":65532,"runAsGroup":65532,"runAsNonRoot":true,"runAsUser":65532,"seccompProfile":{"type":"RuntimeDefault"}}` | Pod security context. |
| readinessProbe | object | `{"httpGet":{"path":"/healthz","port":"http"},"initialDelaySeconds":2,"periodSeconds":10}` | Readiness probe on `/healthz`. |
| replicaCount | int | `1` | Number of adapter replicas. |
| resources | object | `{"limits":{"memory":"128Mi"},"requests":{"cpu":"10m","memory":"32Mi"}}` | Container resources. Requests and a memory limit are set by default. |
| securityContext | object | `{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"privileged":false,"readOnlyRootFilesystem":true,"runAsNonRoot":true}` | Container security context. |
| service.annotations | object | `{}` | Extra Service annotations. |
| service.port | int | `80` | Service port. |
| service.type | string | `"ClusterIP"` | Service type. A type other than `ClusterIP` exposes the adapter, and every log it can read, outside the cluster. |
| serviceAccount.annotations | object | `{}` | ServiceAccount annotations. |
| serviceAccount.automountServiceAccountToken | bool | `false` | Mount the ServiceAccount token. The adapter does not call the Kubernetes API. |
| serviceAccount.create | bool | `true` | Create a ServiceAccount. |
| serviceAccount.name | string | `""` | ServiceAccount name. Generated when empty and `create` is true. |
| tolerations | list | `[]` | Tolerations. |

The values table is generated with [helm-docs](https://github.com/norwoodj/helm-docs): run
`helm-docs` in this directory after a change to `values.yaml`.
