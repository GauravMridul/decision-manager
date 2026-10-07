# syntax=docker/dockerfile:1.4

# Stage 1: Base build with Go and Git
FROM golang:1.23-alpine AS base

RUN apk add --no-cache git

WORKDIR /app

# Configure Git to use HTTPS with GitHub token
ARG GITHUB_TOKEN
RUN git config --global url."https://${GITHUB_TOKEN}@github.com/".insteadOf "https://github.com/"

ENV GOPRIVATE=github.com/dmi-infotech/*

# Stage 2: Download dependencies
FROM base AS dependencies

COPY go.mod go.sum ./

RUN sed -i '/replace github.com\/dmi-infotech\/common-modules\/go/d' go.mod && \
    go mod edit -droprequire=github.com/dmi-infotech/common-modules/go && \
    go mod edit -require=github.com/dmi-infotech/common-modules/go@main && \
    go mod download -x

# Stage 3: Build stage
FROM base AS build

# Copy dependencies from previous stage
COPY --from=dependencies /go/pkg /go/pkg

# Copy source code
COPY . .

# Remove the replace directive again in build stage and ensure dependencies are properly resolved
RUN sed -i '/replace github.com\/dmi-infotech\/common-modules\/go/d' go.mod && \
    go mod edit -droprequire=github.com/dmi-infotech/common-modules/go && \
    go mod edit -require=github.com/dmi-infotech/common-modules/go@main && \
    go mod download -x && go mod tidy 

# Build the application
RUN go build -o main ./cmd/main.go

# Stage 4: Runtime
FROM alpine:latest AS runtime

RUN apk --no-cache add ca-certificates tzdata

RUN addgroup -g 1001 -S appgroup && \
    adduser -u 1001 -S appuser -G appgroup

WORKDIR /app

COPY --from=build /app/main .
COPY --from=build /app/internal/app/db/migrations ./internal/app/db/migrations
COPY --from=build /app/VERSION .

USER appuser

CMD ["./main"]
