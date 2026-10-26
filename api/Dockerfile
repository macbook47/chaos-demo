# Build stage: Red Hat Go toolset (avoids Docker Hub pull limits)
FROM registry.access.redhat.com/ubi9/go-toolset:latest AS build
USER 0
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -o /chaos-api .

# Runtime stage: small UBI image, runs as non-root (OpenShift requirement)
FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
COPY --from=build /chaos-api /usr/local/bin/chaos-api
USER 1001
EXPOSE 8080
CMD ["/usr/local/bin/chaos-api"]
