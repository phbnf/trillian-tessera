FROM public.ecr.aws/docker/library/golang:1.23-alpine3.19 AS builder

ARG GOFLAGS="-trimpath -buildvcs=false -buildmode=exe"
ENV GOFLAGS=$GOFLAGS

# Move to working directory /build
WORKDIR /build

# Copy and download dependency using go mod
COPY go.mod .
COPY go.sum .
RUN go mod download

# Copy the code into the container
COPY . .

# Build the application
RUN go build -o bin/conformance-aws ./cmd/conformance/aws

# Build release image
FROM public.ecr.aws/docker/library/alpine:3.20.2

COPY --from=builder /build/bin/conformance-aws /bin/conformance-aws
ENTRYPOINT ["/bin/conformance-aws"]
