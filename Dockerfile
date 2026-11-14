# syntax=docker/dockerfile:1

# The build stage runs on the build host's platform and cross-compiles with
# GOOS/GOARCH, so a multi-platform build needs no QEMU.
FROM --platform=$BUILDPLATFORM golang:1.27.4@sha256:5e8afd493a7124a8586e134d9b532731161d4b9becf9dee5a5956b107426f93d AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/kargo-loki-logs ./cmd/kargo-loki-logs

FROM gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931
COPY --from=build /out/kargo-loki-logs /kargo-loki-logs
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/kargo-loki-logs"]
