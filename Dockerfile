FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /grpcmcp .

FROM alpine:3.21
COPY --from=builder /grpcmcp /usr/local/bin/grpcmcp
ENTRYPOINT ["grpcmcp"]
