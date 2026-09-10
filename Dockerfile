# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build
WORKDIR /src
ENV GOTOOLCHAIN=auto
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/server ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata wget
WORKDIR /app
COPY --from=build /out/server /app/server
EXPOSE 8080
USER nobody
ENTRYPOINT ["/app/server"]
