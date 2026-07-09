// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package security_test

import (
	"testing"

	"github.com/lf-edge/eve/evetest"
)

// TestSecuritySuite groups security-focused, CVE-reproduction scenarios. These
// are reproduce/observe tests (they document behavior rather than gate CI), so
// the suite is typically run to evaluate a specific EVE build.
//
// Subtests
// --------
//   - TestJanuscapeGuestToHostEscape -- runs the public CVE-2026-53359
//     ("Januscape") KVM/x86 guest-to-host PoC inside an edge-app VM and
//     observes whether EVE (the KVM host) is taken down.
func TestSecuritySuite(test *testing.T) {
	evetest.Init(test)
	defer evetest.Close()

	evetest.DefineTestParameters(
		evetest.HypervisorParameter(),
	)

	evetest.RunTestSuite(
		evetest.TestCase{
			Test: TestJanuscapeGuestToHostEscape,
		},
	)
}
