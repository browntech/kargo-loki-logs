# syntax=docker/dockerfile:1

# The build stage runs on the build host's platform and cross-compiles with
# GOOS/GOARCH, so a multi-platform build needs no QEMU.
FROM --platform=$BUILDPLATFORM golang:1.25 AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/kargo-loki-logs ./cmd/kargo-loki-logs

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/kargo-loki-logs /kargo-loki-logs
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/kargo-loki-logs"]
