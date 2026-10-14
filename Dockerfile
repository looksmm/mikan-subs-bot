# syntax=docker/dockerfile:1.7
# The bot as an addon image of mikan's protocol 2: one static binary, no shell, run as 65532
# on a read-only root, as the host runs every addon.
#   docker buildx build --platform linux/amd64,linux/arm64 --build-arg VERSION=1.0.0 --build-arg ADDON_ID=subs-bot .
FROM --platform=$BUILDPLATFORM golang:1.27 AS build
WORKDIR /src
COPY go.mod ./
COPY cmd/ cmd/
COPY internal/ internal/
ARG TARGETOS TARGETARCH
ARG VERSION=dev
ARG ADDON_ID=subs-bot
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X main.addonID=${ADDON_ID}" -o /out/bot ./cmd/subs-bot

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/bot /bot
USER 65532:65532
ENTRYPOINT ["/bot"]
