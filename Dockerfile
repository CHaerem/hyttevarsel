# Kryssbygget for amd64 og arm64 uten emulering: Go bygger for TARGETARCH
# på byggerens egen arkitektur.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY templates ./templates
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /hyttevarsel .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /hyttevarsel /hyttevarsel
ENV DATA_DIR=/data LISTEN=:8080
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/hyttevarsel"]
