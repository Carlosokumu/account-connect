FROM golang:latest AS build

WORKDIR /build
COPY . .

RUN go mod download
RUN go build -o main ./cmd/http/main.go

FROM debian:bullseye-slim


RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*

WORKDIR /service
COPY --from=build /build/main .

EXPOSE 8080
CMD ["./main"]
