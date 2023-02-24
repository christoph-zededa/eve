// Copyright (c) 2021 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package nim

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	oldDns "github.com/Focinfi/go-dns-resolver"
	"github.com/lf-edge/eve/pkg/pillar/types"
	"github.com/miekg/dns"
)

const (
	minTTLSec              int = 30
	maxTTLSec              int = 3600
	extraSec               int = 10
	etcHostFileName            = "/etc/hosts"
	tmpHostFileName            = "/tmp/etchosts"
	resolvFileName             = "/etc/resolv.conf"
	dnsMaxParallelRequests     = 5
	dnsTimeout                 = 30 * time.Second
)

// go routine for dns query to the controller
func (n *nim) queryControllerDNS() {
	var etchosts, controllerServer []byte
	var ttlSec int
	var ipaddrCached string

	if _, err := os.Stat(etcHostFileName); err == nil {
		etchosts, err = os.ReadFile(etcHostFileName)
		if err == nil {
			controllerServer, _ = os.ReadFile(types.ServerFileName)
			controllerServer = bytes.TrimSuffix(controllerServer, []byte("\n"))
			if bytes.Contains(controllerServer, []byte(":")) {
				serverport := bytes.Split(controllerServer, []byte(":"))
				if len(serverport) == 2 {
					controllerServer = serverport[0]
				}
			}
		}
	}

	if len(controllerServer) == 0 {
		n.Log.Errorf("can't read /etc/hosts or server file")
		return
	}

	dnsTimer := time.NewTimer(time.Duration(minTTLSec) * time.Second)

	wdName := agentName + "dnsQuery"
	stillRunning := time.NewTicker(stillRunTime)
	n.PubSub.StillRunning(wdName, warningTime, errorTime)
	n.PubSub.RegisterFileWatchdog(wdName)

	for {
		select {
		case <-dnsTimer.C:
			// base on ttl from server dns update frequency for controller IP resolve
			// even if the dns server implementation returns the remaining value of the TTL it caches,
			// it will still work.
			ipaddrCached, ttlSec = n.controllerDNSCache(etchosts, controllerServer, ipaddrCached)
			dnsTimer = time.NewTimer(time.Duration(ttlSec) * time.Second)

		case <-stillRunning.C:
		}
		n.PubSub.StillRunning(wdName, warningTime, errorTime)
	}
}

func (n *nim) resolveWithSrcIP(domain string, dnsServerIP net.IP, srcIP net.IP) net.IP {
	sourceUdpAddr := net.UDPAddr{IP: srcIP}
	dialer := net.Dialer{LocalAddr: &sourceUdpAddr}
	dnsClient := dns.Client{Dialer: &dialer}
	msg := dns.Msg{}
	if domain[len(domain)-1] != '.' {
		domain = domain + "."
	}
	msg.SetQuestion(domain, dns.TypeA)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(dnsTimeout))
	defer cancel()
	reply, _, err := dnsClient.ExchangeContext(ctx, &msg, net.JoinHostPort(dnsServerIP.String(), "53"))
	if err != nil {
		n.Log.Tracef("dns exchange failed: %v", err)
		return nil
	}
	if len(reply.Answer) > 0 {
		for _, answer := range reply.Answer {
			if aRecord, ok := answer.(*dns.A); ok {
				return aRecord.A
			}
		}
	}
	return nil
}

func (n *nim) resolveWithPorts(domain string) net.IP {
	return n.resolveWithPortsLambda(domain, n.resolveWithSrcIP)
}

func (n *nim) resolveWithPortsLambda(domain string, resolve func(string, net.IP, net.IP) net.IP) net.IP {
	work := make(chan struct{}, dnsMaxParallelRequests)
	resolvedIPsChan := make(chan net.IP)
	var wg sync.WaitGroup
	for _, port := range n.dpcManager.GetDNS().Ports {
		if port.Cost > 0 {
			continue
		}

		var srcIPs []net.IP
		for _, addrInfo := range port.AddrInfoList {
			srcIPs = append(srcIPs, addrInfo.Addr)
		}

		ifIndex, exist, err := n.networkMonitor.GetInterfaceIndex(port.IfName)
		if !exist {
			continue
		}
		if err != nil {
			n.Log.Warnf("converting ifName to ifIndex failed: %+v", err)
			continue
		}

		dnsInfo, err := n.networkMonitor.GetInterfaceDNSInfo(ifIndex)
		if err != nil {
			n.Log.Warnf("get interface dns info for %s failed: %+v", port.IfName, err)
			continue
		}

		for _, dnsIP := range dnsInfo.DNSServers {
			for _, srcIP := range srcIPs {
				wg.Add(1)
				dnsIPCopy := make(net.IP, len(dnsIP))
				copy(dnsIPCopy, dnsIP)
				srcIPCopy := make(net.IP, len(srcIP))
				copy(srcIPCopy, srcIP)
				go func(dnsIP, srcIP net.IP) {
					work <- struct{}{}
					ip := resolve(domain, dnsIP, srcIP)
					if ip != nil {
						resolvedIPsChan <- ip
					}
					<-work
					wg.Done()
				}(dnsIPCopy, srcIPCopy)
			}
		}
	}

	wgChan := make(chan struct{})
	go func() {
		wg.Wait()
		close(wgChan)
	}()

	select {
	case <-wgChan:
		return nil
	case ip := <-resolvedIPsChan:
		return ip
	}
}

// periodical cache the controller DNS resolution into /etc/hosts file
// it returns the cached ip string, and TTL setting from the server
func (n *nim) controllerDNSCache(etchosts, controllerServer []byte, ipaddrCached string) (string, int) {
	if len(etchosts) == 0 || len(controllerServer) == 0 {
		return ipaddrCached, maxTTLSec
	}

	// Check to see if the server domain is already in the /etc/hosts as in eden,
	// then skip this DNS queries
	if ipaddrCached == "" {
		hostsEntries := bytes.Split(etchosts, []byte("\n"))
		for _, entry := range hostsEntries {
			fields := bytes.Fields(entry)
			if len(fields) == 2 {
				if bytes.Compare(fields[1], controllerServer) == 0 {
					n.Log.Tracef("server entry %s already in /etc/hosts, skip", controllerServer)
					return ipaddrCached, maxTTLSec
				}
			}
		}
	}

	var nameServers []string
	dnsServer, _ := os.ReadFile(resolvFileName)
	dnsRes := bytes.Split(dnsServer, []byte("\n"))
	for _, d := range dnsRes {
		d1 := bytes.Split(d, []byte("nameserver "))
		if len(d1) == 2 {
			nameServers = append(nameServers, string(d1[1]))
		}
	}
	if len(nameServers) == 0 {
		nameServers = append(nameServers, "8.8.8.8")
	}

	if _, err := os.Stat(tmpHostFileName); err == nil {
		_ = os.Remove(tmpHostFileName)
	}

	var newhosts []byte
	var gotipentry bool
	var lookupIPaddr string
	var ttlSec int

	domains := []string{string(controllerServer)}
	dtypes := []oldDns.QueryType{oldDns.TypeA}
	for _, nameServer := range nameServers {
		resolver := oldDns.NewResolver(nameServer)
		resolver.Targets(domains...).Types(dtypes...)

		res := resolver.Lookup()
		for target := range res.ResMap {
			for _, r := range res.ResMap[target] {
				dIP := net.ParseIP(r.Content)
				if dIP == nil {
					continue
				}
				lookupIPaddr = dIP.String()
				ttlSec = getTTL(r.Ttl)
				if ipaddrCached == lookupIPaddr {
					n.Log.Tracef("same IP address %s, return", lookupIPaddr)
					return ipaddrCached, ttlSec
				}
				serverEntry := fmt.Sprintf("%s %s\n", lookupIPaddr, controllerServer)
				newhosts = append(etchosts, []byte(serverEntry)...)
				gotipentry = true
				// a rare event for dns address change, log it
				n.Log.Noticef("dnsServer %s, ttl %d, entry add to /etc/hosts: %s", nameServer, ttlSec, serverEntry)
				break
			}
			if gotipentry {
				break
			}
		}
		if gotipentry {
			break
		}
	}

	if ipaddrCached == lookupIPaddr {
		return ipaddrCached, minTTLSec
	}
	if !gotipentry { // put original /etc/hosts file back
		newhosts = append(newhosts, etchosts...)
	}

	ipaddrCached = ""
	err := os.WriteFile(tmpHostFileName, newhosts, 0644)
	if err == nil {
		if err := os.Rename(tmpHostFileName, etcHostFileName); err != nil {
			n.Log.Errorf("can not rename /etc/hosts file %v", err)
		} else {
			if gotipentry {
				ipaddrCached = lookupIPaddr
			}
			n.Log.Tracef("append controller IP %s to /etc/hosts", lookupIPaddr)
		}
	} else {
		n.Log.Errorf("can not write /tmp/etchosts file %v", err)
	}

	return ipaddrCached, ttlSec
}

func getTTL(ttl time.Duration) int {
	ttlSec := int(ttl.Seconds())
	if ttlSec < minTTLSec {
		// this can happen often, when the dns server returns ttl being the remaining value
		// of it's own cached ttl, we set it to minTTLSec and retry. Next time will get the
		// upper range value of it's remaining ttl.
		ttlSec = minTTLSec
	} else if ttlSec > maxTTLSec {
		ttlSec = maxTTLSec
	}

	// some dns server returns actual remaining time of TTL, to avoid next time
	// get 0 or 1 those numbers, add some extra seconds
	return ttlSec + extraSec
}
