FROM golang:alpine AS builder

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/celerum ./cmd/celerum

FROM alpine:3.21

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /bin/celerum /usr/local/bin/celerum

VOLUME ["/app/data"]

ENTRYPOINT ["celerum"]
CMD ["run", "-c", "celerum.yaml"]

