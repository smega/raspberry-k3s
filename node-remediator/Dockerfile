FROM golang:1.27-alpine AS build
WORKDIR /app

COPY go.mod ./
COPY *.go ./
RUN go test ./... && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /node-remediator .

FROM alpine:3.24
# Changes daily ( build-image.yml ), so this layer isn't served stale
# from the build cache.
ARG APK_DAY
RUN apk upgrade --no-cache && adduser -D -u 10001 appuser
USER appuser
WORKDIR /app

COPY --from=build /node-remediator /app/node-remediator

EXPOSE 8080
CMD ["/app/node-remediator"]
