// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package security_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	// revive:disable:dot-imports
	. "github.com/onsi/gomega"

	eveconfig "github.com/lf-edge/eve-api/go/config"
	"github.com/lf-edge/eve-api/go/evecommon"
	eveinfo "github.com/lf-edge/eve-api/go/info"
	"github.com/lf-edge/eve/evetest"
	"github.com/lf-edge/eve/evetest/netmodels"
	"github.com/lf-edge/eve/pkg/pillar/types"
	uuid "github.com/satori/go.uuid"
)

const (
	// januscapePoCFile is the public CVE-2026-53359 PoC kernel module source,
	// kept verbatim under testdata/ (see testdata/README.md for provenance).
	januscapePoCFile = "testdata/januscape_cve_2026_53359_poc.c"

	// Ubuntu 26.04 LTS ("resolute") server cloud image (qcow2), pinned to a dated
	// daily build (the same image the working SEELE setup boots). EVE boots it as a real VM, so the guest runs its own
	// Ubuntu kernel and `apt-get install linux-headers-$(uname -r)` provides
	// matching headers for compiling the out-of-tree PoC. Ubuntu cloud images boot
	// under EVE's app-VM profile (SeaBIOS + virtio-console); Debian genericcloud
	// does NOT (it reset-loops at GRUB before the kernel starts).
	//
	// A non-OCI datastore requires a sha256 (EVE rejects it otherwise). And EVE
	// treats MaxDownloadBytes as the EXPECTED total size (ContentTree.MaxSizeBytes),
	// not merely a cap, so it must equal the image's exact byte size or the download
	// fails with "premature EOF after <actual> out of <MaxDownloadBytes>".
	januscapeImageServer  = "cloud-images.ubuntu.com"
	januscapeImagePath    = "resolute/20260520/resolute-server-cloudimg-amd64.img"
	januscapeImageSHA256  = "dced94c031cc1f23dee14419a3723a5b110df9938de0ac31913a2bfd07c755b4"
	januscapeImageMaxSize = 858720256

	// The edge-app VM needs several vCPUs: the PoC dedicates cpu0 to a "writer"
	// thread racing the remaining "faulter" vCPUs. MinCPUs leaves EVE a core so it
	// keeps reporting to the controller (the host-liveness signal).
	januscapeAppCPUs   = 3
	januscapeDevMinCPU = 4

	// Bounded run for the PoC's race loop (module run_ms), so the test terminates.
	januscapePoCRunMillis = 90_000

	januscapeAppMAC = "02:16:3e:00:53:59"

	// SSH login user. Root SSH is not permitted on the cloud image, so we log in
	// as an unprivileged sudo user (via password) and use sudo for privileged ops.
	januscapeSSHUser = "januser"
	januscapeSSHPass = "januspass"
)

// januscapeCloudInit is the cloud-init user-data EVE delivers to the guest VM (as
// a NoCloud "cidata" drive). It creates an unprivileged sudo user with a password
// and enables SSH password auth, so the test can log in as that user (evetest's
// ClientCertAuth forces user "root", which the image forbids). The (slow)
// toolchain + kernel-headers install is done by the test over SSH afterwards.
const januscapeCloudInit = `#cloud-config
ssh_pwauth: true
users:
  - name: januser
    groups: sudo
    shell: /bin/bash
    sudo: ALL=(ALL) NOPASSWD:ALL
    lock_passwd: false
chpasswd:
  expire: false
  list:
    - januser:januspass
runcmd:
  - touch /run/janus-ssh-ready
`

// TestJanuscapeGuestToHostEscape deploys an Ubuntu VM **edge app** on EVE and
// runs the public CVE-2026-53359 ("Januscape") proof-of-concept kernel module
// inside it, then watches whether **EVE itself (the KVM host) crashes/hangs**.
//
// CVE-2026-53359 is a guest-to-host use-after-free in the KVM/x86 shadow MMU
// (kvm_mmu_get_child_sp() role-mismatch shadow-page reuse -> pte_list_remove()
// host DoS). The PoC module, running inside a guest, enters VMX/SVM operation
// itself (its own tiny hypervisor via VMXON/vmlaunch) and races nested
// page-table entries so that the *host* KVM corrupts its shadow page tables. A
// successful trigger takes down the host (EVE), not the guest. EVE hands KVM
// guests "-cpu host" (pkg/pillar/hypervisor/kvm.go), so the VM sees vmx/svm in
// CPUID whenever the underlying host has nested KVM enabled — the attack surface.
//
// Guest image: a real Ubuntu 26.04 LTS server cloud image booted as an HVM VM,
// so the guest runs its own Ubuntu kernel and `apt-get install
// linux-headers-$(uname -r)` provides matching headers — which lets the
// out-of-tree PoC module be compiled in-guest.
//
// Intent: reproduce/observe. The build+load of the PoC is expected to succeed
// (a real failure there fails the test), but the host-health outcome is only
// reported, never asserted: a host DoS is detected and loudly logged (VERDICT +
// checkpoint) rather than failing CI, so a vulnerable build is visible and a
// patched one ends cleanly. (Caveat: if the PoC does take EVE down and it
// reboots, the harness end-of-test reboot-count check in Close() will also flag
// the run — the desired loud signal; use EVETEST_PAUSE_ON_FAILURE=true to
// inspect.)
//
// Network model
// -------------
//   - netmodels.SingleEthWithDHCP + RequireInternetConnectivity: EVE needs
//     Internet to download the guest image. The guest is on a Local (NAT) NI
//     (EVE assigns its IP and NATs it to the Internet for apt); the test reaches
//     it via an SSH port-forward on the device uplink (device-IP:2222 -> app:22).
//
// Phases
// ------
//  1. Setup + arch gate: skip on non-x86 EVE devices (the PoC is x86-only).
//  2. Deploy the Ubuntu VM app with cloud-init; wait until RUNNING (lenient
//     download wait).
//  3. Wait for SSH (unprivileged sudo user); recon (uname, vmx/svm exposure);
//     apt-install the toolchain + matching kernel headers over SSH.
//  4. Compile the PoC in-guest (expected to succeed; fatal otherwise).
//  5. insmod the module (amd/nvcpu auto-detected, bounded run_ms); tolerate an
//     SSH drop here (it can mean EVE just went down mid-insmod).
//  6. Observe EVE's ZInfoDevice stream + boot time (SSH liveness fallback) for
//     run_ms + 60s and emit a VERDICT: host DoS reproduced (reboot/hang) or EVE
//     survived.
//
// Test params
// -----------
//   - HYPERVISOR. Calls evetest.SkipIfHypervisorKubevirt() -- Kubevirt is
//     reserved for cluster tests, and this PoC targets bare KVM on x86.
func TestJanuscapeGuestToHostEscape(test *testing.T) {
	evetestT := evetest.Init(test)
	t := NewGomegaWithT(evetestT)
	defer evetest.Close()
	log := evetest.Logger()

	evetest.DefineTestParameters(
		evetest.HypervisorParameter(),
	)
	hypervisor := evetest.GetHypervisorParameterValue()
	evetest.SkipIfHypervisorKubevirt()

	const devName = "edge-dev"
	evetest.Setup(
		evetest.RequireEdgeDevice{
			Name:              devName,
			MinCPUs:           januscapeDevMinCPU,
			WithHypervisor:    hypervisor,
			DeviceReusePolicy: evetest.ResetDeviceConfig,
		},
		evetest.RequireNetworkModel{
			NetworkModel: netmodels.SingleEthWithDHCP,
		},
		// EVE downloads the guest image; the guest apt-installs the toolchain.
		evetest.RequireInternetConnectivity{},
	)
	device := evetest.GetEdgeDevice(devName)
	evetest.Checkpoint("setup-done")

	// Phase 1: arch gate. The PoC uses Intel VMX / AMD SVM and is x86-only.
	var devInfo *eveinfo.ZInfoDevice
	t.Eventually(func() *eveinfo.ZInfoDevice {
		devInfo = device.GetDeviceInfo()
		return devInfo
	}, 2*time.Minute, 5*time.Second).ShouldNot(BeNil(),
		"expected EVE to publish device info after onboarding")
	arch := strings.ToLower(devInfo.GetMachineArch())
	if arch != "" && !strings.Contains(arch, "x86") && !strings.Contains(arch, "amd64") {
		evetestT.Skipf("CVE-2026-53359 PoC is x86-only; EVE device arch is %q", arch)
	}

	appAuth := evetest.UsernamePasswordAuth{
		Username: januscapeSSHUser,
		Password: januscapeSSHPass,
	}

	// Phase 2: deploy the Ubuntu VM edge app.
	devConfig := evetest.NewEdgeDeviceConfig(devName)
	dhcpNet := devConfig.AddNetwork(evetest.DHCPNetworkConfig{
		NetworkType: evecommon.NetworkType_V4Only,
	})
	devConfig.AddNetworkAdapter(evetest.NetworkAdapterConfig{
		LogicalLabel:  "ethernet0",
		PhysicalLabel: "eth0",
		InterfaceName: "eth0",
		NetworkUUID:   dhcpNet,
		Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
	})
	// Apply the base network config first and wait until EVE confirms it, so a
	// cost-0 management port with an IP is active before the image download starts.
	// Otherwise EVE's downloader can transiently report "No IP management port
	// addresses with cost <= 0" and the download stalls at 0%.
	device.ApplyConfig(devConfig, true, true)
	evetest.Checkpoint("network-ready")

	// Local (L3, NAT) NI: EVE runs the DHCP server and assigns/reports the guest's
	// IP, and the test reaches the guest via an SSH port-forward on the device
	// uplink (device-IP:2222 -> app:22). This mirrors the working SEELE setup. (A
	// Switch NI relies on the guest being reachable at an IP EVE does not assign,
	// which EVE does not report for a VM app here.)
	niUUID := devConfig.AddNetworkInstance(evetest.LocalNetworkInstanceConfig{
		DisplayName: "januscape-ni",
		Port:        "ethernet0",
		Subnet:      evetest.IPSubnet("10.53.59.0/24"),
		DHCPRange: types.IPRange{
			Start: evetest.IPAddress("10.53.59.2"),
			End:   evetest.IPAddress("10.53.59.254"),
		},
		Gateway: evetest.IPAddress("10.53.59.1"),
		MTU:     1500,
	})
	appUUID := devConfig.AddApplication(evetest.ApplicationInstanceConfig{
		DisplayName: "januscape-poc-vm",
		Activate:    true,
		Image: evetest.HTTPStorage{
			ImageFormat:       eveconfig.Format_QCOW2,
			ImageSHA256:       januscapeImageSHA256,
			MaxDownloadBytes:  januscapeImageMaxSize,
			ImageRelativePath: januscapeImagePath,
			ServerAddress:     januscapeImageServer,
			UseHTTPS:          true,
		},
		VirtualizationMode: eveconfig.VmMode_HVM,
		CPUs:               januscapeAppCPUs,
		MemoryBytes:        4 * evetest.GiB,
		DiskBytes:          10 * evetest.GiB,
		UserData:           base64.StdEncoding.EncodeToString([]byte(januscapeCloudInit)),
		NetworkAdapters: []evetest.AppNetworkAdapter{
			evetest.VirtualNetworkAdapter{
				LogicalLabel:        "vif0",
				NetworkInstanceUUID: niUUID,
				MAC:                 evetest.MACAddress(januscapeAppMAC),
				PortFwdRules: []evetest.PortFwdRule{
					{Protocol: evetest.NetworkProtocolTCP, EdgeNodePort: 2222, AppPort: 22},
				},
				ACLAllowRules: []evetest.ACLAllowRule{
					// Allow all traffic to/from the guest (apt out, SSH in).
					{
						Protocol:     evetest.NetworkProtocolAny,
						RemoteSubnet: evetest.IPSubnet("0.0.0.0/0"),
					},
				},
			},
		},
	})
	device.ApplyConfig(devConfig, false, false)

	// Wait for the app to reach RUNNING with a lenient overall deadline. We avoid
	// device.WaitUntilAppIsRunning here: its 1-minute download-stall tolerance is
	// too tight for the image download over the slow/jittery NAT uplink. We only
	// fail on a terminal ERROR or the overall timeout.
	log.Infof("Waiting for the guest VM to download its image and reach RUNNING...")
	waitAppRunningLenient(evetestT, device, appUUID, 60*time.Minute)
	evetest.Checkpoint("app-running")

	// Phase 3: wait for SSH reachability (cloud-init created the login user). Also
	// gives the guest time to DHCP on the Switch NI and start sshd.
	log.Infof("Waiting for the guest VM to become SSH-reachable (cloud-init)...")
	sshReady := false
	lastDiag := ""
	sshDeadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(sshDeadline) {
		out, _, sshErr := device.RunShellScriptInsideApp(appUUID, appAuth,
			"test -f /run/janus-ssh-ready && echo JANUS_SSH_READY", 30*time.Second, 0)
		if sshErr == nil && strings.Contains(out, "JANUS_SSH_READY") {
			sshReady = true
			break
		}
		// Diagnostics: report the app's reported network IPs and the SSH probe
		// error (logged only when they change) so a reachability failure is
		// attributable to "no app IP" vs "unreachable" vs "connection refused".
		var ips []string
		if ai := device.GetAppInfo(appUUID); ai != nil {
			for _, n := range ai.GetNetwork() {
				ips = append(ips, n.GetIPAddrs()...)
			}
		}
		diag := fmt.Sprintf("appIPs=%v sshErr=%v", ips, sshErr)
		if diag != lastDiag {
			log.Infof("guest not SSH-ready yet: %s", diag)
			lastDiag = diag
		}
		time.Sleep(15 * time.Second)
	}
	if !sshReady {
		evetestT.Fatalf("guest VM did not become SSH-reachable within 10m (%s)", lastDiag)
	}
	evetest.Checkpoint("guest-ready")

	// Recon: report the guest attack surface.
	reconOut, _, err := device.RunShellScriptInsideApp(appUUID, appAuth,
		januscapeReconScript(), time.Minute, 30*time.Second)
	if err != nil {
		evetestT.Fatalf("failed to run recon inside the guest VM: %v", err)
	}
	log.Infof("guest VM recon:\n%s", strings.TrimSpace(reconOut))
	if !strings.Contains(reconOut, "vmx") && !strings.Contains(reconOut, "svm") {
		log.Warnf("VERDICT: attack surface ABSENT — the guest VM does not see " +
			"vmx/svm in /proc/cpuinfo, so EVE is not exposing nested virtualization " +
			"to app VMs on this build/host. The CVE-2026-53359 PoC cannot reach " +
			"EVE's KVM shadow MMU via an edge app here.")
		evetest.Checkpoint("attack-surface-absent")
		return
	}

	// Phase 3b: install the build toolchain + matching kernel headers over SSH
	// (generous timeout for the slow apt over the NAT uplink).
	log.Infof("Installing build toolchain + kernel headers in the guest (apt)...")
	provOut, _, err := device.RunShellScriptInsideApp(appUUID, appAuth,
		januscapeProvisionScript(), 20*time.Minute, 5*time.Minute)
	log.Infof("guest VM provisioning output:\n%s", strings.TrimSpace(provOut))
	if err != nil {
		evetestT.Fatalf("failed to provision the guest toolchain over SSH: %v", err)
	}
	if !strings.Contains(provOut, "JANUS_HEADERS_OK") {
		evetestT.Fatalf("guest toolchain / kernel headers unavailable after apt; "+
			"provisioning output:\n%s", strings.TrimSpace(provOut))
	}
	evetest.Checkpoint("guest-provisioned")

	// Phase 4: compile the PoC in-guest against the guest kernel.
	pocSrc, err := os.ReadFile(januscapePoCFile)
	if err != nil {
		evetestT.Fatalf("failed to read PoC source %q: %v", januscapePoCFile, err)
	}
	buildOut, _, err := device.RunShellScriptInsideApp(appUUID, appAuth,
		januscapeBuildScript(pocSrc), 5*time.Minute, 2*time.Minute)
	log.Infof("guest VM PoC build output:\n%s", strings.TrimSpace(buildOut))
	if err != nil {
		evetestT.Fatalf("failed to run the PoC build over SSH: %v", err)
	}
	if !strings.Contains(buildOut, "JANUS_BUILT") {
		evetestT.Fatalf("failed to compile the CVE-2026-53359 PoC module inside the "+
			"guest VM; build output:\n%s", strings.TrimSpace(buildOut))
	}
	evetest.Checkpoint("poc-built")

	// Phase 5: baseline EVE liveness, start watching, then arm the PoC. Watch
	// BEFORE insmod so we cannot miss a post-trigger reboot/silence.
	baselineBoot := bootTimeOf(device.GetDeviceInfo())
	updates, stopWatch := device.WatchDeviceInfo()
	defer stopWatch()

	log.Infof("Loading the PoC module inside the guest VM (run_ms=%d)...",
		januscapePoCRunMillis)
	armOut, _, armErr := device.RunShellScriptInsideApp(appUUID, appAuth,
		januscapeInsmodScript(januscapePoCRunMillis), 60*time.Second, 30*time.Second)
	log.Infof("guest VM insmod output:\n%s", strings.TrimSpace(armOut))
	if armErr != nil {
		// An SSH error here can mean EVE crashed mid-insmod and tore down the
		// connectivity path; fall through to the health observation to tell.
		log.Warnf("insmod over SSH returned an error "+
			"(may indicate EVE went down during insmod): %v", armErr)
	}
	evetest.Checkpoint("poc-armed")

	// Capture EVE's kernel log right after arming, so we retain it even if the DoS
	// takes EVE down during observation (a later capture would then be impossible).
	captureEVEHostLogs(device, "after-arm")

	// Phase 6: observe EVE (the host) and report a verdict.
	window := time.Duration(januscapePoCRunMillis)*time.Millisecond + time.Minute
	log.Infof("PoC running inside the guest VM; observing EVE (host) health "+
		"for %s (a host DoS is the expected trigger of CVE-2026-53359)...", window)
	verdict, crashed := observeEVEHostHealth(device, updates, baselineBoot, window)
	// Best-effort final capture of EVE's kernel log (fails if the host went down).
	captureEVEHostLogs(device, "final")
	if crashed {
		log.Errorf("VERDICT: HOST DoS REPRODUCED — EVE (the KVM host) went down "+
			"after the guest VM ran the CVE-2026-53359 PoC: %s", verdict)
		evetest.Checkpoint("host-dos-reproduced")
		return
	}
	log.Infof("VERDICT: EVE SURVIVED — no host crash/hang/reboot observed within "+
		"%s after the PoC. The KVM host kernel appears patched against "+
		"CVE-2026-53359, or the PoC could not trigger it in this environment.", window)
	evetest.Checkpoint("host-survived")
}

// waitAppRunningLenient waits until the app reaches RUNNING, failing only on a
// terminal ERROR state or after the overall timeout. Unlike
// EdgeDevice.WaitUntilAppIsRunning it does not impose a short per-progress
// download-stall deadline, so it tolerates a slow/jittery image download (and
// EVE's own download retries) over a constrained uplink.
func waitAppRunningLenient(evetestT *evetest.T, device *evetest.EdgeDevice,
	appUUID uuid.UUID, overall time.Duration) {
	updates, stop := device.WatchAppInfo(appUUID)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), overall)
	defer cancel()
	for {
		select {
		case info, ok := <-updates:
			if !ok {
				evetestT.Fatalf("app %q info subscription closed before RUNNING", appUUID)
				return
			}
			switch info.GetState() {
			case eveinfo.ZSwState_RUNNING:
				return
			case eveinfo.ZSwState_ERROR:
				var errs []string
				for _, e := range info.GetAppErr() {
					if d := e.GetDescription(); d != "" {
						errs = append(errs, d)
					}
				}
				evetestT.Fatalf("app %q entered ERROR before RUNNING: %s",
					appUUID, strings.Join(errs, "; "))
				return
			}
		case <-ctx.Done():
			evetestT.Fatalf("timed out after %s waiting for app %q to reach RUNNING",
				overall, appUUID)
			return
		}
	}
}

// bootTimeOf returns the EVE boot time from a device-info message, or the zero
// time when unavailable.
func bootTimeOf(info *eveinfo.ZInfoDevice) time.Time {
	if info == nil || info.GetBootTime() == nil {
		return time.Time{}
	}
	return info.GetBootTime().AsTime()
}

// observeEVEHostHealth watches EVE's device-info stream for up to window and
// reports whether the host went down (reboot detected via an advancing boot
// time, or a hang detected via a stalled info stream confirmed by a failed SSH
// liveness probe). It never fails the test; the decision is returned to the
// caller, matching this test's reproduce/observe intent.
func observeEVEHostHealth(device *evetest.EdgeDevice, updates <-chan *eveinfo.ZInfoDevice,
	baselineBoot time.Time, window time.Duration) (verdict string, crashed bool) {
	log := evetest.Logger()
	const stalenessThreshold = 90 * time.Second
	deadline := time.Now().Add(window)
	lastMsg := time.Now()

	for time.Now().Before(deadline) {
		tick := 15 * time.Second
		if remaining := time.Until(deadline); remaining < tick {
			tick = remaining
		}
		select {
		case info, ok := <-updates:
			if !ok {
				return "EVE device-info subscription closed unexpectedly", true
			}
			lastMsg = time.Now()
			bt := bootTimeOf(info)
			if baselineBoot.IsZero() {
				baselineBoot = bt
			} else if !bt.IsZero() && bt.After(baselineBoot.Add(2*time.Second)) {
				return fmt.Sprintf(
					"EVE rebooted (boot time advanced %s -> %s; last reboot reason: %q)",
					baselineBoot, bt, info.GetLastRebootReason()), true
			}
		case <-time.After(tick):
		}

		if time.Since(lastMsg) > stalenessThreshold {
			// EVE stopped publishing device info; confirm with an SSH liveness
			// probe before declaring a hang (info can lag under heavy load).
			if _, _, err := device.RunShellScript("true", 15*time.Second, 0); err != nil {
				return fmt.Sprintf(
					"EVE stopped publishing device info for %s and an SSH liveness "+
						"probe failed: %v", time.Since(lastMsg).Round(time.Second), err), true
			}
			log.Warnf("EVE has not published device info for %s but still answered "+
				"an SSH probe; continuing to watch...",
				time.Since(lastMsg).Round(time.Second))
			lastMsg = time.Now()
		}
	}
	return "", false
}

// captureEVEHostLogs saves EVE's (the host's) kernel logs to the test artifact
// directory (EVETEST_ARTIFACT_DIR, else a temp dir). It captures two sources:
//
//   - The EVE serial console, which the broker logs live to
//     $ARTIFACT_DIR/evetest-qemu-vms/eve-*/console.log. This survives a host
//     crash/hang (it is written by the outer QEMU, not by EVE), so it is the
//     reliable place to see a CVE-2026-53359 trigger — a KVM shadow-MMU oops /
//     panic on EVE, versus merely the guest VM dying.
//   - The live dmesg ring buffer over SSH (best-effort; fails if EVE is down).
//
// It never fails the test.
func captureEVEHostLogs(device *evetest.EdgeDevice, label string) {
	log := evetest.Logger()
	dir := os.Getenv("EVETEST_ARTIFACT_DIR")
	if dir == "" {
		dir = os.TempDir()
	}

	// EVE serial console (crash-proof; written by the broker's QEMU).
	consoles, _ := filepath.Glob(
		filepath.Join(dir, "evetest-qemu-vms", "eve-*", "console.log"))
	for _, src := range consoles {
		data, rerr := os.ReadFile(src)
		if rerr != nil {
			log.Warnf("could not read EVE serial console %q: %v", src, rerr)
			continue
		}
		dst := filepath.Join(dir, fmt.Sprintf("eve-console-%s.txt", label))
		if werr := os.WriteFile(dst, data, 0o644); werr != nil {
			log.Warnf("failed to write EVE serial console (%s) to %s: %v", label, dst, werr)
		} else {
			log.Infof("saved EVE serial console (%s) to %s (%d bytes)", label, dst, len(data))
		}
	}
	if len(consoles) == 0 {
		log.Warnf("no EVE serial console.log found under %s/evetest-qemu-vms/eve-*/", dir)
	}

	// Live dmesg ring buffer over SSH (best-effort).
	out, _, err := device.RunShellScript("dmesg -T 2>/dev/null || dmesg", 30*time.Second, 0)
	if err != nil {
		log.Warnf("could not capture EVE dmesg over SSH (%s): %v (EVE may be down)", label, err)
		return
	}
	dst := filepath.Join(dir, fmt.Sprintf("eve-dmesg-%s.txt", label))
	if werr := os.WriteFile(dst, []byte(out), 0o644); werr != nil {
		log.Warnf("failed to write EVE dmesg (%s) to %s: %v", label, dst, werr)
		return
	}
	log.Infof("saved EVE dmesg (%s) to %s (%d bytes)", label, dst, len(out))
}

// januscapeReconScript reports the guest's architecture, kernel, CPU vendor and
// whether hardware-virtualization (vmx/svm) is exposed to the edge-app VM.
func januscapeReconScript() string {
	return strings.Join([]string{
		`echo "ARCH=$(uname -m)"`,
		`echo "KERNEL=$(uname -r)"`,
		`echo "VENDOR=$(grep -m1 '^vendor_id' /proc/cpuinfo | awk '{print $3}')"`,
		`echo "NPROC=$(nproc)"`,
		`echo "VIRT=$(grep -oE 'vmx|svm' /proc/cpuinfo | sort -u | tr '\n' ' ')"`,
	}, "\n")
}

// januscapeProvisionScript installs the toolchain and matching kernel headers in
// the guest so the PoC module can be compiled against the guest kernel. Runs as
// an unprivileged user, so apt is invoked via sudo.
func januscapeProvisionScript() string {
	return `set -x
export DEBIAN_FRONTEND=noninteractive
sudo -E apt-get update
sudo -E apt-get install -y build-essential "linux-headers-$(uname -r)" || sudo -E apt-get install -y build-essential linux-headers-generic || sudo -E apt-get install -y build-essential linux-headers-virtual
if command -v gcc >/dev/null 2>&1 && [ -d "/lib/modules/$(uname -r)/build" ]; then
  echo "JANUS_HEADERS_OK kver=$(uname -r)"
else
  echo "JANUS_HEADERS_MISSING kver=$(uname -r) gcc=$(command -v gcc || echo none)"
  ls -1 /lib/modules 2>/dev/null
fi
`
}

// januscapeBuildScript writes the PoC source + Makefile into a user-writable dir
// and compiles the module against the guest kernel (no privilege needed to build).
func januscapeBuildScript(cSrc []byte) string {
	var b strings.Builder
	b.WriteString("set -e\n")
	b.WriteString(`mkdir -p "$HOME/janus" && cd "$HOME/janus"` + "\n")
	fmt.Fprintf(&b, "echo '%s' | base64 -d > poc.c\n",
		base64.StdEncoding.EncodeToString(cSrc))
	fmt.Fprintf(&b, "echo '%s' | base64 -d > Makefile\n",
		base64.StdEncoding.EncodeToString([]byte(januscapeMakefile)))
	b.WriteString(`KVER=$(uname -r)
if [ ! -d "/lib/modules/$KVER/build" ]; then
  echo "JANUS_NO_KBUILD kver=$KVER"; ls -1 /lib/modules 2>/dev/null; exit 0
fi
if make >/tmp/janus-build.log 2>&1; then
  echo "JANUS_BUILT"; ls -l poc.ko
else
  echo "JANUS_BUILD_FAILED"; tail -n 40 /tmp/janus-build.log
fi
`)
	return b.String()
}

// januscapeInsmodScript loads the compiled PoC (via sudo). amd and nvcpu are
// auto-detected in the guest; runMillis bounds the PoC's race loop. It removes any
// in-guest KVM modules first (the PoC enters VMX/SVM operation itself).
func januscapeInsmodScript(runMillis int) string {
	return fmt.Sprintf(`cd "$HOME/janus"
if grep -q svm /proc/cpuinfo; then AMD=1; else AMD=0; fi
NVCPU=$(nproc)
sudo rmmod kvm_intel kvm_amd 2>/dev/null || true
sudo dmesg -C 2>/dev/null || true
KO="$HOME/janus/poc.ko"
# Arm via a transient systemd timer 3s in the future. insmod can freeze the whole
# guest (including sshd) almost instantly, which would hang this SSH session --
# the framework does not cleanly abort a frozen remote session, so a synchronous
# (or even nohup-backgrounded) insmod hangs the test. systemd-run --no-block
# returns immediately and the deferred start lets this command finish and the SSH
# session close BEFORE the PoC runs.
sudo systemd-run --no-block --on-active=3s --collect \
  /usr/sbin/insmod "$KO" amd=$AMD nvcpu=$NVCPU run_ms=%d
echo "JANUS_ARMED (scheduled t+3s) amd=$AMD nvcpu=$NVCPU run_ms=%d"
`, runMillis, runMillis)
}

// januscapeMakefile is a standard out-of-tree kbuild Makefile for the PoC.
const januscapeMakefile = "obj-m += poc.o\n" +
	"KDIR ?= /lib/modules/$(shell uname -r)/build\n" +
	"all:\n" +
	"\t$(MAKE) -C $(KDIR) M=$(PWD) modules\n" +
	"clean:\n" +
	"\t$(MAKE) -C $(KDIR) M=$(PWD) clean\n"
