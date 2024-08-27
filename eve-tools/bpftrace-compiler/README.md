# Bpftrace-Compiler

Bpftrace-Compiler is the counterpart to the `bpftrace-aotrt` tool in the debug container in EVE.

`bpftrace-aotrt` needs a compiled bpftrace script specific for the node architecture and EVE
version.

The `bpftrace-compiler` helps to do this by creating a specific virtual machine with the same
architecture and kernel and run `bpftrace` in it to compile your bpftrace script.

## How it works

| Step | Local Machine                                                 | Eve Node                                       |
|------|---------------------------------------------------------------|------------------------------------------------|
| 1    | Connect to EVE (ssh/http-debug)                               | Accept connection                              |
| 2    | Retrieve architecture, userspace container version            | Answer with architecture, userspace container  |
|      | and eve-kernel version                                        | version and eve-kernel version                 |
| 3    | Create VM with architecture (arm64|amd64), userspace container|                                                |
|      | and eve-kernel                                                |                                                |
| 4    | Run `bpftrace` on bpftrace script from 9p share in created VM |                                                |
| 5    | Shutdown the VM                                               |                                                |
| 6    | Copy compiled bpftrace script to EVE                          | Retrieve file                                  |
| 7    |                                                               | Run `bpftrace` with provided file              |
| 8    | Collect the output and show it to the user                    | Forward output of `bpftrace`                   |

## Build

```bash
go build
```

## Example Usage With Local EVE in Qemu

Run EVE:

```bash
make -j$(nproc) pkgs && make LIVE_UPDATE=1 ROOTFS_FORMAT=ext4 live run
```

### Via SSH

```bash
./bpftrace-compiler run-ssh 127.1:2222 examples/tcpconnect.bt
```

### Via Http-Debug

Do a port-forwarding of the http-debug port via ssh:

```bash
ssh -L 6543:localhost:6543 -p 2222 root@localhost
```

Enable http-debug on EVE:

```sh
eve http-debug
```

```bash
./bpftrace-compiler run-http 127.1:6543 examples/tcpconnect.bt
```

Stop http-debug on EVE:

```sh
eve http-debug stop
```

## List available probes

### For a specific binary

```bash
./bpftrace-compiler list -u service,pillar,docker.io/lfedge/eve-pillar:latest amd64 docker.io/lfedge/eve-kernel:eve-kernel-amd64-v6.1.38-generic-fb31ce85306c-gcc /containers/services/pillar/rootfs/opt/zededa/bin/zedbox | tee tracepoints.zedbox
```

### For the kernel

```bash
./bpftrace-compiler list amd64 docker.io/lfedge/eve-kernel:eve-kernel-amd64-v6.1.38-generic-fb31ce85306c-gcc | tee tracepoints.kernel
```

## Debug

You can enter the VM manually:

```bash
./bpftrace-compiler debug -u service,debug,docker.io/lfedge/eve-debug:latest amd64 docker.io/lfedge/eve-kernel:eve-kernel-amd64-v6.1.38-generic-fb31ce85306c-gcc ./
```

This will create a VM with

* kernel eve-kernel-amd64-v6.1.38-generic-fb31ce85306c-gcc
* eve-debug:latest image in /containers/services/debug
* mount the current directory to /mnt

### Compile bpftrace script

```sh
bpftrace --aot /dev/null /mnt/script.bt
mv /mnt/temp_btaot /mnt/script.aot
```

### List symbols of a binary

```bash
bpftrace -l 'u:/containers/services/pillar/rootfs/opt/zededa/bin/zedbox:*'
```
