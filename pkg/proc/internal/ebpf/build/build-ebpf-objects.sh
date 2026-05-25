#!/bin/bash -e

# The go generate command seems to not like being run from
# the vendor directory. Remove it and restore it after.
rm -rf vendor

restore_vendor() {
  git checkout vendor
}

trap restore_vendor EXIT

# Run as the current user to avoid root-owned output files that
# would cause permission errors when restoring the vendor directory.
#
# GOCACHE and GOPATH are set in the image's ENV to /tmp paths so
# the Go toolchain works even when running as a non-root uid that
# has no home directory inside the container.
docker run \
    --rm \
    -u "$(id -u):$(id -g)" \
    -v "$(pwd)":/delve-bpf \
    delve-ebpf-builder:v0.0.1
