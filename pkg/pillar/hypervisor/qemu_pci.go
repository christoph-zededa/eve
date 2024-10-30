// Copyright (c) 2024 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package hypervisor

import (
	"fmt"
	"io"
	"strconv"
	"text/template"
)

type pciAddr struct {
	domainId   uint16
	busId      uint8
	deviceId   uint8
	functionId byte
}

func (p pciAddr) String() string {
	return p.longAddr()
}

func (p pciAddr) longAddr() string {
	return fmt.Sprintf("%s:%s:%s.%s",
		p.domain(),
		p.bus(),
		p.device(),
		p.function(),
	)
}

func (p pciAddr) addrWOFunction() string {
	return fmt.Sprintf("%s:%s:%s",
		p.domain(),
		p.bus(),
		p.device(),
	)
}

func (p pciAddr) shortAddr() string {
	return fmt.Sprintf("%s:%s.%s",
		p.bus(),
		p.device(),
		p.function(),
	)
}

func (p pciAddr) domain() string {
	return fmt.Sprintf("%04x", p.domainId)
}
func (p pciAddr) bus() string {
	return fmt.Sprintf("%02x", p.busId)
}
func (p pciAddr) device() string {
	return fmt.Sprintf("%02x", p.deviceId)
}
func (p pciAddr) function() string {
	return fmt.Sprintf("%x", p.functionId)
}

func (p *pciAddr) setBus(b string) error {
	busId, err := strconv.ParseUint(b, 16, 8)
	if err != nil {
		return fmt.Errorf("could not parse '%s' to uint8: %w", b, err)
	}

	p.busId = uint8(busId)

	return nil
}

func (p *pciAddr) setDomain(d string) error {
	domainId, err := strconv.ParseUint(d, 16, 16)
	if err != nil {
		return fmt.Errorf("could not parse '%s' to uint16: %w", d, err)
	}

	p.domainId = uint16(domainId)

	return nil
}

func (p *pciAddr) setDevice(d string) error {
	deviceId, err := strconv.ParseUint(d, 16, 8)
	if err != nil {
		return fmt.Errorf("could not parse '%s' to uint8: %w", d, err)
	}

	if deviceId > 0x1f {
		return fmt.Errorf("could not parse '%s' to uint8: cannot be greater than 0x1f", d)
	}

	p.deviceId = uint8(deviceId)

	return nil
}

func (p *pciAddr) setFunction(f string) error {
	functionId, err := strconv.ParseUint(f, 16, 8)
	if err != nil {
		return fmt.Errorf("could not parse '%s' to uint8: %w", f, err)
	}

	p.functionId = uint8(functionId)

	return nil
}

func newPCIAddr(a string) (pciAddr, error) {
	p := pciAddr{}

	_, err := fmt.Sscanf(a, "%4x:%2x:%2x.%x", &p.domainId, &p.busId, &p.deviceId, &p.functionId)
	if err != nil {
		return pciAddr{}, fmt.Errorf("could not parse '%s': %w", a, err)
	}

	return p, nil
}

type devNamer interface {
	qemuDevName() string
}
type pciDevParent interface {
	addr() int
	devNamer
}

type pcieRoot struct {
	tree    *pciTree
	devs    []*pciDev
	bridges []*pcieBridge
}

func (r *pcieRoot) addr() int {
	a := 0
	for i, root := range r.tree.roots {
		if r == r.tree.roots[i] {
			return a + r.tree.startPCIId
		}

		a += len(root.devs)
		for _, br := range root.bridges {
			a += len(br.devs)
		}
	}

	return -1
}

func (r *pcieRoot) qemuDevName() string {
	return fmt.Sprintf("pci.%d", r.addr())
}

func (r *pcieRoot) String() string {
	return fmt.Sprintf("pci.%d", r.addr())
}

type pcieBridge struct {
	parent *pcieRoot
	devs   []*pciDev
}

func (b *pcieBridge) qemuDevName() string {
	return fmt.Sprintf("pcie-bridge.%d", b.parent.addr())
}

func (b *pcieBridge) addr() int {
	return b.parent.addr()
}

func (b *pcieBridge) String() string {
	return fmt.Sprintf("pcie-bridge.%d/%d", b.parent.addr(), b.addr())
}

type pciDev struct {
	rootParent   *pcieRoot
	bridgeParent *pcieBridge
	hostAddr     pciAddr
}

func (d *pciDev) qemuDevName() string {
	return ""
}

func (d *pciDev) parent() pciDevParent {
	if d.bridgeParent != nil {
		return d.bridgeParent
	}

	return d.rootParent
}

func (d pciDev) String() string {
	return fmt.Sprintf("pci-%s", d.hostAddr.String())
}

func (d *pciDev) addr() int {
	if d.rootParent != nil {
		return 0
	}

	for i := range d.bridgeParent.devs {
		if d == d.bridgeParent.devs[i] {
			// Unsupported PCI slot 0 for standard hotplug controller. Valid slots are between 1 and 31
			return i + 1
		}
	}

	return -1
}

type pciTree struct {
	roots      []*pcieRoot
	startPCIId int
}

func (b pcieBridge) hostAddrWOFunction() string {
	if len(b.devs) == 0 {
		return ""
	}

	return b.devs[0].hostAddr.addrWOFunction()
}

func (t *pciTree) findPCIDev(hostAddr pciAddr) *pciDev {
	for _, root := range t.roots {
		for i, dev := range root.devs {
			if dev.hostAddr == hostAddr {
				return root.devs[i]
			}
		}

		for _, bridge := range root.bridges {
			for i, dev := range bridge.devs {
				if dev.hostAddr == hostAddr {
					return bridge.devs[i]
				}
			}
		}
	}

	return nil
}

func (t *pciTree) findMatchingPCIEBridge(hostAddr pciAddr) *pcieBridge {
	for _, root := range t.roots {
		for i, br := range root.bridges {
			if hostAddr.addrWOFunction() == br.hostAddrWOFunction() {
				return root.bridges[i]
			}
		}
	}

	return nil
}

func (t *pciTree) findPCIRoot(hostAddr pciAddr) *pcieRoot {
	dev := t.findPCIDev(hostAddr)
	if dev == nil {
		return nil
	}

	if dev.rootParent != nil {
		return dev.rootParent
	}

	if dev.bridgeParent.parent != nil {
		return dev.bridgeParent.parent
	}

	return nil
}

func (t *pciTree) addNewPCIEBridge() *pcieBridge {

	root := &pcieRoot{
		devs:    []*pciDev{},
		bridges: []*pcieBridge{},
		tree:    t,
	}
	br := &pcieBridge{
		parent: root,
		devs:   []*pciDev{},
	}

	root.bridges = []*pcieBridge{br}

	t.roots = append(t.roots, root)

	return br
}

func (t *pciTree) insert(hostAddr pciAddr) *pciDev {
	if dev := t.findPCIDev(hostAddr); dev != nil {
		return dev
	}

	dev := &pciDev{
		hostAddr: hostAddr,
	}

	br := t.findMatchingPCIEBridge(hostAddr)
	if br != nil {
		dev.bridgeParent = br
		br.devs = append(br.devs, dev)

		return dev
	}

	for i := range t.roots {
		root := t.roots[i]
		for _, dev := range root.devs {
			if dev.hostAddr.addrWOFunction() == hostAddr.addrWOFunction() {
				t.roots = append(t.roots[:i], t.roots[i+1:]...)

				dev.rootParent = nil

				br := t.addNewPCIEBridge()
				br.devs = append(br.devs, dev)
				dev.bridgeParent = br

				newDev := &pciDev{
					bridgeParent: br,
					hostAddr:     hostAddr,
				}
				br.devs = append(br.devs, newDev)

				return newDev
			}
		}
	}

	newRoot := &pcieRoot{
		devs:    []*pciDev{dev},
		bridges: []*pcieBridge{},
		tree:    t,
	}

	dev.rootParent = newRoot

	t.roots = append(t.roots, newRoot)

	return dev
}

type qemuConf struct {
	tree      *pciTree
	tRootPort *template.Template
	tBridge   *template.Template
	tPCI      *template.Template

	devWritten map[string]struct{}
}

func newQemuConf(tree *pciTree) (qemuConf, error) {
	q := qemuConf{
		tree:       tree,
		devWritten: map[string]struct{}{},
	}

	for _, t := range []struct {
		tmpl **template.Template
		name string
		str  string
	}{
		{
			tmpl: &q.tRootPort,
			name: "root port",
			str:  qemuRootPortPciPassthruTemplate,
		},
		{
			tmpl: &q.tBridge,
			name: "bridge",
			str:  qemuPCIPassthruBridgeTemplate,
		},
		{
			tmpl: &q.tPCI,
			name: "pci device",
			str:  qemuPciPassthruTemplate,
		},
	} {
		var err error
		*t.tmpl, err = template.New("template").Parse(t.str)
		if err != nil {
			return q, fmt.Errorf("parsing template '%s' failed: %w", t.str, err)
		}
	}

	return q, nil
}

func (q *qemuConf) compareAndDevWritten(dev devNamer) bool {
	_, written := q.devWritten[dev.qemuDevName()]

	q.devWritten[dev.qemuDevName()] = struct{}{}

	return written
}

func (q *qemuConf) writeDevParent(w io.Writer, dev *pciDev) error {
	pciRootArgs := struct {
		Name  string
		PCIId int
	}{}

	var root *pcieRoot
	if dev.rootParent != nil {
		root = dev.rootParent
	} else if dev.bridgeParent != nil {
		root = dev.bridgeParent.parent
	} else {
		return fmt.Errorf("device is orphaned: %+v", dev)
	}

	pciRootArgs.Name = root.qemuDevName()
	pciRootArgs.PCIId = root.addr()

	if !q.compareAndDevWritten(root) {
		err := q.tRootPort.Execute(w, pciRootArgs)
		if err != nil {
			return fmt.Errorf("writing root template failed: %w", err)
		}
	}

	if dev.bridgeParent != nil {
		pciBridgeArgs := struct {
			Name  string
			Bus   int
			PCIId int
		}{
			Name:  dev.bridgeParent.qemuDevName(),
			Bus:   dev.bridgeParent.addr(),
			PCIId: 0,
		}
		if !q.compareAndDevWritten(dev.bridgeParent) {
			err := q.tBridge.Execute(w, pciBridgeArgs)
			if err != nil {
				return fmt.Errorf("writing bridge template failed: %w", err)
			}
		}
	}

	return nil
}

func (q *qemuConf) write(w io.Writer, addresses []string) error {
	hostAddresses := make([]pciAddr, 0, len(addresses))

	for _, addr := range addresses {
		pciAddr, err := newPCIAddr(addr)
		if err != nil {
			fmt.Errorf("could not create PCIAddr object: %w", err)
		}
		hostAddresses = append(hostAddresses, pciAddr)
		dev := q.tree.insert(pciAddr)
		if dev == nil {
			return fmt.Errorf("could not insert device %s", addr)
		}
	}

	for _, addr := range hostAddresses {
		dev := q.tree.findPCIDev(addr)

		pd := pciDevice{
			pciLong: dev.hostAddr.longAddr(),
		}
		isVGA := pd.isVGA()
		var xopRegion bool
		if isVGA {
			vendor, err := pd.vid()
			xopRegion = err == nil && vendor == "0x8086"
		}

		pciDevArgs := struct {
			PciShortAddr string
			Bus          string
			Xvga         bool
			Xopregion    bool
			Addr         string
		}{
			PciShortAddr: dev.hostAddr.shortAddr(),
			Bus:          dev.parent().qemuDevName(),
			Xvga:         isVGA,
			Xopregion:    xopRegion,
			Addr:         fmt.Sprintf("0x%x", dev.addr()),
		}

		q.writeDevParent(w, dev)

		err := q.tPCI.Execute(w, pciDevArgs)
		if err != nil {
			return fmt.Errorf("writing PCI template failed: %w", err)
		}
	}

	return nil
}

func writePCIQemuConf(w io.Writer, addresses []string, startPCIId int) error {
	t := pciTree{
		roots:      []*pcieRoot{},
		startPCIId: startPCIId,
	}

	for _, addr := range addresses {
		pciAddr, err := newPCIAddr(addr)
		if err != nil {
			fmt.Errorf("could not create PCIAddr object: %w", err)
		}
		dev := t.insert(pciAddr)
		if dev == nil {
			return fmt.Errorf("could not insert device %s", addr)
		}
	}

	q, err := newQemuConf(&t)
	if err != nil {
		return fmt.Errorf("creating qemu conf failed: %w", err)
	}

	return q.write(w, addresses)
}
