FROM golang:1.23 as builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
RUN go build -o raftkv main.go

FROM debian:stable-slim
WORKDIR /app
COPY --from=builder /app/raftkv .
RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*
EXPOSE 7000 8080
CMD ["./raftkv"]
