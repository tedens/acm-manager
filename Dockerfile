# Build stage
FROM golang:1.25 as builder

WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download

# Copy entire project (excluding files in .dockerignore)
COPY . .

# Build the controller binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o manager cmd/main.go

# Final image stage
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
USER nonroot:nonroot

EXPOSE 8080 9443

ENTRYPOINT ["/manager"]