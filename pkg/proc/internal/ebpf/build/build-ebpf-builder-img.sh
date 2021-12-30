#!/bin/bash -e

sudo docker build \
    -t delve-ebpf-builder:v0.0.1 \
    -f pkg/proc/internal/ebpf/build/ebpf-Dockerfile ./pkg/proc/internal/ebpf/build
