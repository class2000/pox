FROM golang:1.23-alpine AS builder

RUN apk update && apk add --no-cache git gcc musl-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN ls -l proto/

RUN CGO_ENABLED=1 GOOS=linux go build -ldflags "-linkmode external -extldflags -static" -o pbftnode .

FROM alpine:latest

WORKDIR /root/

COPY --from=builder /app/pbftnode .

ENTRYPOINT ["./pbftnode"]

CMD []
