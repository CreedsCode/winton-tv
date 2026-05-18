# syntax=docker/dockerfile:1.7

FROM golang:1.25-alpine AS build
WORKDIR /src

# Cache deps
COPY go.mod go.sum* ./
RUN go mod download

# Build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /out/winton-tv \
    ./cmd/server

# Runtime: distroless, nonroot, static binary
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/winton-tv /app/winton-tv
COPY --from=build /src/web /app/web

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/winton-tv"]
