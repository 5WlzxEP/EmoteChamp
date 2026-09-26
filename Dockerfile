FROM golang:1.27-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY . ./
COPY templates ./templates/

ENV CGO_ENABLED=0
RUN go build -o server .

FROM scratch

ENV WAYLAND_DISPLAY="wayland-0"

COPY --from=builder /app/server /server

EXPOSE 8080

ENTRYPOINT ["/server"]