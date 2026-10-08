# syntax=docker/dockerfile:1

FROM golang:1.26.5-alpine3.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/lantern ./cmd/lantern

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/lantern /lantern
USER nonroot:nonroot
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 CMD ["/lantern", "healthcheck"]
ENTRYPOINT ["/lantern"]
CMD ["serve"]
