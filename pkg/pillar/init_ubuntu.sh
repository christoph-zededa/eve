#!/usr/bin/bash

sudo mkdir -p /sys/fs/cgroup/init
sudo echo 1 > /sys/fs/cgroup/init/cgroup.procs
sudo echo +cpu > /sys/fs/cgroup/cgroup.subtree_control

cd /tmp
sudo nohup /usr/local/bin/containerd &

cd /src

exec /bin/bash
