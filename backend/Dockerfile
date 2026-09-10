FROM golang:1.22-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /sovereign-media-hub .

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /sovereign-media-hub .
RUN mkdir -p /app/data/blobs

EXPOSE 8080
ENV PORT=8080
ENV DATA_DIR=/app/data
ENV BASE_URL=http://localhost:8080

CMD ["./sovereign-media-hub"]
