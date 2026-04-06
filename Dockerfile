FROM golang:1.26-alpine AS builder
WORKDIR /src

# Copy shared library first (needed by replace directive)
COPY etafovivian-shared/ ./etafovivian-shared/

# Copy module files and download deps
COPY dbaas-service/go.mod dbaas-service/go.sum ./dbaas-service/
WORKDIR /src/dbaas-service
RUN go mod download

# Copy source and build
COPY dbaas-service/ ./
RUN go mod tidy
ARG VERSION=dev
RUN CGO_ENABLED=0 go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /bin/dbaas-service \
    ./cmd/dbaas-service

# Build a tiny static healthcheck binary
RUN printf 'package main\nimport("fmt";"net/http";"os")\nfunc main(){r,e:=http.Get("http://localhost:8100/healthz");if e!=nil||r.StatusCode!=200{fmt.Fprintln(os.Stderr,e);os.Exit(1)}}\n' > /tmp/hc.go && \
    CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/healthcheck /tmp/hc.go

# --- runtime ---
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /bin/dbaas-service /dbaas-service
COPY --from=builder /bin/healthcheck /healthcheck
EXPOSE 8100
ENTRYPOINT ["/dbaas-service"]
