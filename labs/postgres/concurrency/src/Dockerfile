FROM golang:1.23-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /driver .

FROM alpine:3.20
RUN adduser -D -u 10001 app
USER app
COPY --from=build /driver /usr/local/bin/driver
ENTRYPOINT ["driver"]
