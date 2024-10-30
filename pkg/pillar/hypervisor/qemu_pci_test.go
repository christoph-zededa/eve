// Copyright (c) 2024 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package hypervisor

import (
	"fmt"
	"io"
	"os"
	"testing"
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
