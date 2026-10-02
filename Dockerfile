# SPDX-FileCopyrightText: 2026 Latere AI
# SPDX-License-Identifier: Apache-2.0

# The developer image: compiles cribrumd inside the image, so `docker build .`
# from a checkout is enough. The release image, Dockerfile.ci, copies the
# binary the release pipeline already built and attested; its runtime stage
# is this one.

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-X latere.ai/x/cribrum/internal/version.Version=${VERSION} -X latere.ai/x/cribrum/internal/version.Commit=${COMMIT} -X latere.ai/x/cribrum/internal/version.Date=${DATE}" \
      -o /out/cribrumd ./cmd/cribrumd

# >>> shared runtime base <<<
# cribrumd forks no binary, so the runtime stage is distroless: CA roots for
# the issuers a hosted cribrumd reads keys from, nothing else. It runs as the
# image's non-root user, listens on every interface of the container rather
# than on loopback, and keeps the ledger in a volume that user owns: the
# directory is the base image's own empty home of that user, copied with its
# owner, because a directory the image creates is root's and a volume
# mounted on it starts out as root's. Dockerfile.ci carries this stage byte
# for byte, so the released image differs from this one in where the binary
# came from and in nothing else.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=gcr.io/distroless/static-debian12:nonroot --chown=nonroot:nonroot /home/nonroot /var/lib/cribrum
VOLUME ["/var/lib/cribrum"]
USER nonroot:nonroot
ENV CRIBRUM_ADDR=:7720 CRIBRUM_DATA_DIR=/var/lib/cribrum
EXPOSE 7720
# <<< shared runtime base >>>
COPY --from=build /out/cribrumd /usr/local/bin/cribrumd
ENTRYPOINT ["/usr/local/bin/cribrumd"]
