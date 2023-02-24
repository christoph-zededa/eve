// Copyright (c) 2023 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package nim

import (
	"flag"
	"net"
	"testing"

	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/dpcmanager"
	"github.com/lf-edge/eve/pkg/pillar/netmonitor"
	"github.com/sirupsen/logrus"
)

var testDomains = flag.String("domains", "", "comma separated list of domains to resolve")

func createTestNim() *nim {
	logger := logrus.StandardLogger()
	log := base.NewSourceLogObject(logger, "zedrouter", 1234)
	nim := &nim{
		Log:                      log,
		Logger:                   logger,
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
