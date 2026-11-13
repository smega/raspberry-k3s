FROM golang:1.24-alpine AS build
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /status-api .

FROM alpine:3.20
RUN adduser -D -u 10001 appuser
USER appuser
WORKDIR /app

COPY --from=build /status-api /app/status-api

EXPOSE 8080
CMD ["/app/status-api"]
