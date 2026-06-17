FROM golang:1.23-alpine AS build
WORKDIR /app
RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
ARG LAB
RUN CGO_ENABLED=0 go build -trimpath -o /driver ./cmd/${LAB}

FROM alpine:3.21
RUN adduser -D -u 10001 app
USER app
COPY --from=build /driver /usr/local/bin/driver
ENTRYPOINT ["driver"]
