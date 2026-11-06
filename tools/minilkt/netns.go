package main

import (
	"fmt"
	"minilkt/dockerproxy"
	"net"
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

// privateNetns is a network namespace holding nothing but loopback, for build
// steps to run in: the proxy listens inside it and nothing else is reachable.
//
// A goroutine locks its OS thread, moves the thread into a new namespace and
// parks there for the rest of the process. It cannot come back: the original
// namespace belongs to the host's user namespace, where this process, after
// re-executing into its own user namespace, has no capabilities. Go clones
// new threads from a template thread, never from a locked one, so the rest of
// the process stays where it was. The thread keeps the namespace alive and
// Path stays valid as long as it lives.
type privateNetns struct {
	Path string // /proc/<pid>/task/<tid>/ns/net, what buildah's network namespace option takes
	run  chan func()
	done chan struct{}
}

// newPrivateNetns creates the namespace. It needs CAP_SYS_ADMIN in the
// current user namespace, so it has to run after the re-exec into one.
func newPrivateNetns() (*privateNetns, error) {
	n := &privateNetns{run: make(chan func()), done: make(chan struct{})}
	ready := make(chan error)
	go func() {
		runtime.LockOSThread() // never unlocked on purpose
		if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
			ready <- fmt.Errorf("unshare(CLONE_NEWNET): %w", err)
			return
		}
		if err := loopbackUp(); err != nil {
			ready <- fmt.Errorf("bringing lo up: %w", err)
			return
		}
		n.Path = fmt.Sprintf("/proc/%d/task/%d/ns/net", os.Getpid(), unix.Gettid())
		close(ready)
		for f := range n.run {
			f()
			n.done <- struct{}{}
		}
	}()
	if err := <-ready; err != nil {
		return nil, err
	}
	return n, nil
}

// Do runs f on the namespace's thread, so every socket f opens lives in the
// namespace.
func (n *privateNetns) Do(f func()) {
	n.run <- f
	<-n.done
}

// listenBothSides opens the proxy's loopback listeners on port twice: on the
// host side, for minilkt's own pulls and the host-side git of ADD, and inside
// n for the build steps. The two namespaces have separate port spaces.
func listenBothSides(n *privateNetns, port uint16) ([]net.Listener, error) {
	listeners, err := dockerproxy.ListenLoopback(port)
	if err != nil {
		return nil, err
	}
	n.Do(func() {
		var inside []net.Listener
		if inside, err = dockerproxy.ListenLoopback(port); err == nil {
			listeners = append(listeners, inside...)
		}
	})
	if err != nil {
		for _, ln := range listeners {
			ln.Close()
		}
		return nil, err
	}
	return listeners, nil
}

// loopbackUp brings up lo in the calling thread's network namespace.
func loopbackUp() error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	ifr, err := unix.NewIfreq("lo")
	if err != nil {
		return err
	}
	if err := unix.IoctlIfreq(fd, unix.SIOCGIFFLAGS, ifr); err != nil {
		return err
	}
	ifr.SetUint16(ifr.Uint16() | unix.IFF_UP)
	return unix.IoctlIfreq(fd, unix.SIOCSIFFLAGS, ifr)
}
