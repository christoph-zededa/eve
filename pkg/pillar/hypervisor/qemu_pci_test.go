// Copyright (c) 2024 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package hypervisor

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestPCIString(t *testing.T) {
	pa, err := newPCIAddr("2000:04:08.2")
	if err != nil {
		panic(err)
	}

	str := pa.String()
	if str != "2000:04:08.2" {
		t.Fatalf("got %s", str)
	}
}

func FuzzPCIBusAddr(f *testing.F) {
	f.Fuzz(func(t *testing.T,
		domain string,
		bus string,
		device string,
		function string,
	) {

		pa := pciAddr{}

		for _, f := range []func() error{
			func() error { return pa.setDomain(domain) },
			func() error { return pa.setBus(bus) },
			func() error { return pa.setDevice(device) },
			func() error { return pa.setFunction(function) },
		} {
			if f() != nil {
				return
			}
		}

		addrStr := pa.String()

		pa.setDomain(pa.domain())
		pa.setBus(pa.bus())
		pa.setDevice(pa.device())
		pa.setFunction(pa.function())

		str := pa.String()
		if str != addrStr {
			t.Fatalf("got %s, expected %s", str, addrStr)
		}

	})
}

func compareOutputs(t *testing.T, tree *pciTree, startPCIId int) {
	addrs := make([]string, 0)

	for _, root := range tree.roots {
		for _, dev := range root.devs {
			addrs = append(addrs, dev.hostAddr.longAddr())
		}

		for _, br := range root.bridges {
			for _, dev := range br.devs {
				addrs = append(addrs, dev.hostAddr.longAddr())
			}
		}
	}

	var newQemuConfWriter bytes.Buffer
	var oldQemuConfWriter bytes.Buffer

	writePCIQemuConf(&newQemuConfWriter, addrs, startPCIId)

	pciAssignments := []pciDevice{}
	for _, addr := range addrs {
		pciAssignments = append(pciAssignments, pciDevice{
			pciLong: addr,
			ioType:  0,
		})
	}
	pciAssignmentsFiller := pciAssignmentsTemplateFiller{
		multifunctionsDevices: multifunctionDevGroup(pciAssignments),
		file:                  &oldQemuConfWriter,
	}

	err := pciAssignmentsFiller.do(&oldQemuConfWriter, pciAssignments, startPCIId)
	if err != nil {
		t.Fatalf("writing to template file failed: %v", err)
	}

	newQemuConfStr := newQemuConfWriter.String()
	oldQemuConfStr := oldQemuConfWriter.String()

	if newQemuConfStr != oldQemuConfStr {
		t.Log(cmp.Diff(newQemuConfStr, oldQemuConfStr))
		t.Log("---------------------------")
		t.Log("---- Full output (old) ----")
		t.Log("---------------------------")
		t.Log(oldQemuConfStr)
		t.Log("---------------------------")
		t.Log("---- Full output (new) ----")
		t.Log("---------------------------")
		t.Log(newQemuConfStr)
		t.FailNow()
	}
}

func FuzzPCITreeInsert(f *testing.F) {
	fuzzParamLength := 5
	dirEntries, err := os.ReadDir("/sys/bus/pci/devices/")
	if err == nil {
		for i := fuzzParamLength - 1; i < len(dirEntries); i++ {
			addrs := make([]string, 0, fuzzParamLength)

			for _, entry := range dirEntries {
				addrs = append(addrs, entry.Name())
			}

			f.Add(addrs[0], addrs[1], addrs[2], addrs[3], addrs[4])
		}
	}

	f.Add(
		"0000:00:00.0",
		"0000:00:02.0",
		"0000:00:1f.3",
		"0000:00:1f.4",
		"0000:00:1f.5",
	)

	f.Fuzz(func(t *testing.T,
		addr1, addr2, addr3, addr4, addr5 string) {

		tree := pciTree{
			roots: []*pcieRoot{},
		}

		addrs := []string{
			addr1, addr2, addr2, addr4, addr5,
		}

		for _, addr := range addrs {
			pciAddr, err := newPCIAddr(addr)
			if err != nil {
				return
			}

			tree.insert(pciAddr)
		}

		for i := 0; i < 20; i++ {
			compareOutputs(t, &tree, i)
		}

		for i := range tree.roots {
			root := tree.roots[i]
			for _, br := range root.bridges {
				if br.parent != root {
					t.Fatalf("parent: %v, root: %v", br.parent, root)
				}
			}
		}

		for _, root := range tree.roots {
			for _, dev := range root.devs {
				if dev.bridgeParent != nil && dev.rootParent != nil {
					t.Fatalf("dev %+v cannot be directly under bridge and root dev at the same time", dev)
				}
			}
		}
		for _, root := range tree.roots {
			for _, br := range root.bridges {
				for _, dev := range br.devs {
					if dev.hostAddr.addrWOFunction() != br.devs[0].hostAddr.addrWOFunction() {
						t.Fatalf("non matching pci bus addresses: %s <-> %s", dev.hostAddr.addrWOFunction(), br.devs[0].hostAddr.addrWOFunction())
					}
					if dev.bridgeParent != nil && dev.rootParent != nil {
						t.Fatalf("dev %+v cannot be directly under bridge and root dev at the same time", dev)
					}
				}
			}
		}
	})
}

func dotLine(w io.Writer, indent uint, from, to string) {
	for i := 0; i < int(indent); i++ {
		fmt.Fprintf(w, "\t")
	}

	fmt.Fprintf(w, `"%s" -> "%s";`, from, to)
	fmt.Fprintf(w, "\n")
}

func Dot(w io.Writer, t pciTree) {
	fmt.Fprintf(w, "digraph pciTree {\n")
	fmt.Fprintf(w, "\trankdir=LR;\n\n")
	fmt.Fprintf(w, "\tpcie0;\n\n")

	for i := range t.roots {
		root := t.roots[i]
		dotLine(w, 1, "pcie0", root.String())

		for _, dev := range root.devs {
			dotLine(w, 2, root.String(), dev.String())
		}

		for _, bridge := range root.bridges {
			dotLine(w, 2, root.String(), bridge.String())

			for _, dev := range bridge.devs {
				dotLine(w, 3, bridge.String(), dev.String())
			}
		}

		fmt.Fprintf(w, "\n")
	}

	fmt.Fprintf(w, "}\n")
}

func ExampleDot() {
	// generated with: lspci | perl -ne 'print "\"0000:$1\",\n" if (m/^(\S+) /);'
	addrs := []string{
		"0000:00:00.0",
		"0000:00:02.0",
		"0000:00:04.0",
		"0000:00:06.0",
		"0000:00:07.0",
		"0000:00:07.2",
		"0000:00:08.0",
		"0000:00:0a.0",
		"0000:00:0d.0",
		"0000:00:0d.2",
		"0000:00:0d.3",
		"0000:00:14.0",
		"0000:00:14.2",
		"0000:00:14.3",
		"0000:00:15.0",
		"0000:00:15.1",
		"0000:00:16.0",
		"0000:00:16.3",
		"0000:00:1f.0",
		"0000:00:1f.3",
		"0000:00:1f.4",
		"0000:00:1f.5",
		"0000:04:00.0",
	}

	t := pciTree{
		roots: []*pcieRoot{},
	}
	for _, addr := range addrs {
		pciAddr, err := newPCIAddr(addr)
		if err != nil {
			panic(err)
		}
		dev := t.insert(pciAddr)
		if dev == nil {
			panic("no dev")
		}

	}
	Dot(os.Stdout, t)
	// Output: digraph pciTree {
	//	rankdir=LR;
	//
	//	pcie0;
	//
	//	"pcie0" -> "pci.0";
	//		"pci.0" -> "pci-0000:00:00.0";
	//
	//	"pcie0" -> "pci.1";
	//		"pci.1" -> "pci-0000:00:02.0";
	//
	//	"pcie0" -> "pci.2";
	//		"pci.2" -> "pci-0000:00:04.0";
	//
	//	"pcie0" -> "pci.3";
	//		"pci.3" -> "pci-0000:00:06.0";
	//
	//	"pcie0" -> "pci.4";
	//		"pci.4" -> "pcie-bridge.4/4";
	//			"pcie-bridge.4/4" -> "pci-0000:00:07.0";
	//			"pcie-bridge.4/4" -> "pci-0000:00:07.2";
	//
	//	"pcie0" -> "pci.6";
	//		"pci.6" -> "pci-0000:00:08.0";
	//
	//	"pcie0" -> "pci.7";
	//		"pci.7" -> "pci-0000:00:0a.0";
	//
	//	"pcie0" -> "pci.8";
	//		"pci.8" -> "pcie-bridge.8/8";
	//			"pcie-bridge.8/8" -> "pci-0000:00:0d.0";
	//			"pcie-bridge.8/8" -> "pci-0000:00:0d.2";
	//			"pcie-bridge.8/8" -> "pci-0000:00:0d.3";
	//
	//	"pcie0" -> "pci.11";
	//		"pci.11" -> "pcie-bridge.11/11";
	//			"pcie-bridge.11/11" -> "pci-0000:00:14.0";
	//			"pcie-bridge.11/11" -> "pci-0000:00:14.2";
	//			"pcie-bridge.11/11" -> "pci-0000:00:14.3";
	//
	//	"pcie0" -> "pci.14";
	//		"pci.14" -> "pcie-bridge.14/14";
	//			"pcie-bridge.14/14" -> "pci-0000:00:15.0";
	//			"pcie-bridge.14/14" -> "pci-0000:00:15.1";
	//
	//	"pcie0" -> "pci.16";
	//		"pci.16" -> "pcie-bridge.16/16";
	//			"pcie-bridge.16/16" -> "pci-0000:00:16.0";
	//			"pcie-bridge.16/16" -> "pci-0000:00:16.3";
	//
	//	"pcie0" -> "pci.18";
	//		"pci.18" -> "pcie-bridge.18/18";
	//			"pcie-bridge.18/18" -> "pci-0000:00:1f.0";
	//			"pcie-bridge.18/18" -> "pci-0000:00:1f.3";
	//			"pcie-bridge.18/18" -> "pci-0000:00:1f.4";
	//			"pcie-bridge.18/18" -> "pci-0000:00:1f.5";
	//
	//	"pcie0" -> "pci.22";
	//		"pci.22" -> "pci-0000:04:00.0";
	//
	//}

}
