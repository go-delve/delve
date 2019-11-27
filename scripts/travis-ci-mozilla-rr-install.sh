#!/bin/bash

wget https://github.com/mozilla/rr/releases/download/5.2.0/rr-5.2.0-Linux-$(uname -m).deb
sudo dpkg -i rr-5.2.0-Linux-$(uname -m).deb
echo 0 > /proc/sys/kernel/yama/ptrace_scope
echo 1 > /proc/sys/kernel/perf_event_paranoid
