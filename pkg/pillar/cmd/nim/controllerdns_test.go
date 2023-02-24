// Copyright (c) 2023 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package nim

import (
	"flag"
	"net"
	"testing"

	"github.com/lf-edge/eve/pkg/pillar/agentbase"
	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/dpcmanager"
	"github.com/lf-edge/eve/pkg/pillar/netmonitor"
	"github.com/lf-edge/eve/pkg/pillar/types"
	"github.com/sirupsen/logrus"
)

var testDomains = flag.String("domains", "", "comma separated list of domains to resolve")

func createTestNim() *nim {
	logger := logrus.StandardLogger()
	log := base.NewSourceLogObject(logger, "zedrouter", 1234)

	mockInterface := []netmonitor.MockInterface{
		{
			Attrs: netmonitor.IfAttrs{
				IfIndex: 0,
				IfName:  "if0",
			},
			IPAddrs: []*net.IPNet{
				{
					IP:   []byte{192, 168, 0, 1},
					Mask: []byte{255, 255, 255, 0},
				},
				{
					IP:   []byte{192, 168, 0, 2},
					Mask: []byte{255, 255, 255, 0},
				},
			},
			HwAddr: []byte{},
			DNS: netmonitor.DNSInfo{
				ResolvConfPath: "/etc/resolv.conf",
				Domains:        []string{},
				DNSServers: []net.IP{
					{1, 1, 1, 1},
					{9, 9, 9, 9},
				},
			},
		},
		{
			Attrs: netmonitor.IfAttrs{
				IfIndex: 1,
				IfName:  "if1",
			},
			IPAddrs: []*net.IPNet{
				{
					IP:   []byte{192, 168, 1, 1},
					Mask: []byte{255, 255, 255, 0},
				},
				{
					IP:   []byte{192, 168, 1, 2},
					Mask: []byte{255, 255, 255, 0},
				},
			},
			HwAddr: []byte{},
			DNS: netmonitor.DNSInfo{
				ResolvConfPath: "/etc/resolv.conf",
				Domains:        []string{},
				DNSServers: []net.IP{
					{1, 0, 0, 1},
					{8, 8, 8, 8},
				},
			},
		},
		{
			Attrs: netmonitor.IfAttrs{
				IfIndex: 2,
				IfName:  "ExpensiveIf",
			},
			IPAddrs: []*net.IPNet{{
				IP:   []byte{6, 6, 6, 6},
				Mask: []byte{255, 255, 255, 0},
			}},
			HwAddr: []byte{},
			DNS: netmonitor.DNSInfo{
				ResolvConfPath: "/etc/resolv.conf",
				Domains:        []string{},
				DNSServers: []net.IP{
					{0, 6, 6, 6},
					{8, 8, 8, 8},
				},
			},
		},
	}

	dpcManagerMock := dpcmanager.DpcManagerMock{}
	deviceNetworkStatus := types.DeviceNetworkStatus{
		CurrentIndex: 0,
		Ports: []types.NetworkPortStatus{
			{
				IfName: mockInterface[0].Attrs.IfName,
				AddrInfoList: []types.AddrInfo{
					{Addr: mockInterface[0].IPAddrs[0].IP},
					{Addr: mockInterface[0].IPAddrs[1].IP},
				},
			},
			{
				IfName: mockInterface[1].Attrs.IfName,
				AddrInfoList: []types.AddrInfo{
					{Addr: mockInterface[1].IPAddrs[0].IP},
					{Addr: mockInterface[1].IPAddrs[1].IP},
				},
			},
			{
				IfName: mockInterface[2].Attrs.IfName,
				Cost:   0,
				AddrInfoList: []types.AddrInfo{
					{Addr: net.IP{6, 6, 6, 6}},
				},
			},
		},
	}
	dpcManagerMock.SetDNS(deviceNetworkStatus)

	networkMonitor := netmonitor.MockNetworkMonitor{Log: log}
	networkMonitor.AddOrUpdateInterface(mockInterface[0])
	networkMonitor.AddOrUpdateInterface(mockInterface[1])
	networkMonitor.AddOrUpdateInterface(mockInterface[2])
	nim := &nim{
		AgentBase:      agentbase.AgentBase{},
		Log:            log,
		Logger:         logger,
		dpcManager:     &dpcManagerMock,
		dpcReconciler:  nil,
		networkMonitor: &networkMonitor,
	}

	return nim
}

func TestDnsResolve(t *testing.T) {
	t.Parallel()
	nim := createTestNim()
	r := nim.resolveWithSrcIP("www.google.com", net.IP{1, 1, 1, 1}, net.IP{172, 17, 0, 3})
	t.Logf("resolved %+v", r)
}

func TestDnsResolveTimeout(t *testing.T) {
	t.Parallel()
	nim := createTestNim()
	r := nim.resolveWithSrcIP("www.google.com", net.IP{93, 184, 216, 34}, net.IP{172, 17, 0, 3})
	if r != nil {
		t.Fatalf("resolving with dns server 127.1.1.1 should fail, but succeeded: %+v", r)
	}
}

func TestResolveWithPortsLambda(t *testing.T) {
	t.Parallel()

	nim := createTestNim()

	resolverFunc := func(domain string, dnsServer net.IP, srcIP net.IP) net.IP {
		t.Logf("req for %s from %+v to %+v", domain, srcIP, dnsServer)
		return nil
	}

	nim.resolveWithPortsLambda("example.com", resolverFunc)
}
