# --- the web app ---
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN if [ -f package-lock.json ]; then npm ci; else npm install; fi
COPY web/ ./
RUN npm run build

# --- the binary ---
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /shoebox .

# --- runtime: Tesseract, Poppler, qpdf, ExifTool ---
FROM debian:13-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      tesseract-ocr tesseract-ocr-deu tesseract-ocr-eng poppler-utils qpdf libimage-exiftool-perl ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 1000 --user-group --home-dir /data --shell /usr/sbin/nologin shoebox \
    && mkdir -p /data /archive && chown shoebox:shoebox /data /archive
COPY --from=build /shoebox /usr/local/bin/shoebox
ENV SB_DATA=/data SB_ARCHIVE=/archive SB_LISTEN=:8080
VOLUME ["/data", "/archive"]
EXPOSE 8080
USER shoebox
HEALTHCHECK --interval=30s --timeout=3s CMD ["/usr/local/bin/shoebox", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/shoebox"]
