# syntax=docker/dockerfile:1.7
FROM golang:1.26.11 AS gosu
ENV CGO_ENABLED=0 GOBIN=/out
# Rebuild the upstream entrypoint helper with the release Go toolchain.
RUN go install -trimpath github.com/tianon/gosu@6456aaa0f3c854d199d0f037f068eb97515b7513

FROM mysql:8.4.13@sha256:a81d043930e51745522bd5c430961cf8286f0a23f0a9a2a6e0108f7ea71b0902
# The server, entrypoint and logical backup tools do not require MySQL Shell.
RUN microdnf -y --disablerepo="*" --enablerepo=ol9_baseos_latest update libblkid libmount libsmartcols libuuid \
    && rpm -e mysql-shell \
    && microdnf clean all
COPY --from=gosu /out/gosu /usr/local/bin/gosu
RUN gosu mysql true
