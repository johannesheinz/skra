# syntax=docker/dockerfile:1

# --- Build stage: produce a fully static, CGO-free binary ---
FROM golang:1.26-bookworm AS build
WORKDIR /src

# Cache dependencies separately from source for faster rebuilds.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO_ENABLED=0 keeps the binary static (modernc.org/sqlite is pure Go).
# -trimpath and stripped symbols (-s -w) reduce size and leak less build info.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/skra . \
    && mkdir -p /data

# --- Runtime stage: distroless static, non-root ---
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/skra /skra

# Data directory owned by the non-root runtime user (uid 65532) so SQLite can
# create its files. A named volume mounted here inherits this ownership.
# SKRA_DB_PATH should point inside this directory.
COPY --from=build --chown=nonroot:nonroot /data /var/lib/skra
VOLUME ["/var/lib/skra"]
EXPOSE 3000

ENTRYPOINT ["/skra"]
CMD ["serve"]
