FROM golang:1.25 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /manager cmd/main.go
FROM gcr.io/distroless/static:nonroot
COPY --from=builder /manager /manager
USER 65532:65532
ENTRYPOINT ["/manager"]
