# Build a static binary, then run it from a small image.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -tags timetzdata -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/set-admin ./cmd/set-admin

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
WORKDIR /app
COPY --from=build /out/ /app/
RUN mkdir -p /app/uploads && chown app /app/uploads
USER app
ENV PORT=8080 UPLOAD_DIR=/app/uploads
EXPOSE 8080
CMD ["/app/server"]
