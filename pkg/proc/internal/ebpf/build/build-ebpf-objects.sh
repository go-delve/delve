#!/bin/bash -e

# The go generate command seems to not like being run from
# the vendor directory. Remove it and restore it after.
rm -rf vendor

restore_vendor() {
  git checkout vendor
}

trap restore_vendor EXIT

docker run \
    -it \
    --rm \
    -v "$(pwd)":/delve-bpf \
    delve-ebpf-builder:v0.0.1
