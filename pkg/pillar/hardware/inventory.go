// Copyright (c) 2025 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package hardware

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jaypipes/ghw"
	"github.com/jaypipes/ghw/pkg/option"
	"github.com/jaypipes/ghw/pkg/pci/address"
	"github.com/jaypipes/pcidb"
	pcitypes "github.com/jaypipes/pcidb/types"
	"github.com/lf-edge/eve-api/go/info"
	"github.com/lf-edge/eve/pkg/pillar/types"
)

const (
	agentName = "inventory"
	// Time limits for event loop handlers
	errorTime            = 3 * time.Minute
	warningTime          = 40 * time.Second
	stillRunningInterval = 25 * time.Second
)

type boardingStatusType uint32

const (
	unknownStatus boardingStatusType = iota
	onboardedStatus
	offboardStatus
)

type inventoryReporter struct {
	dns types.DeviceNetworkStatus

	needUpload     atomic.Bool
	boardingStatus atomic.Uint32

	uploading sync.Mutex
}

type inventoryMsgCreator struct {
	inventory *info.HardwareInventory
}

func (imc *inventoryMsgCreator) fillCPU() error {
	imc.inventory.CpuInfo = &info.CPUInfo{
		Cpus: []*info.CPU{
			{
				Model:  "12th Gen Intel(R) Core(TM) i7-1270P",
				Vendor: "MMSXMAS25",
				Id:     0,
				Freq:   4800000000,
			},
			{
				Model:  "12th Gen Intel(R) Core(TM) i7-1270P",
				Vendor: "MMSXMAS25",
				Id:     0,
				Freq:   4800000000,
			},
			{
				Model:  "12th Gen Intel(R) Core(TM) i7-1270P",
				Vendor: "MMSXMAS25",
				Id:     0,
				Freq:   4800000000,
			},
			{
				Model:  "12th Gen Intel(R) Core(TM) i7-1270P",
				Vendor: "MMSXMAS25",
				Id:     0,
				Freq:   4800000000,
			},
			{
				Model:  "12th Gen Intel(R) Core(TM) i7-1270P",
				Vendor: "MMSXMAS25",
				Id:     0,
				Freq:   3500000000,
			},
			{
				Model:  "12th Gen Intel(R) Core(TM) i7-1270P",
				Vendor: "MMSXMAS25",
				Id:     0,
				Freq:   3500000000,
			},
		},
	}

	return nil
}
func (imc *inventoryMsgCreator) fillSerial() error {
	// ANAN
	imc.inventory.SerialDevices = []*info.SerialPort{
		{
			Parent:      &info.BusParent{},
			IoportRange: "3f8-3ff",
			Irq:         1,
			Devpath:     "/dev/ttyS0",
		},
		{
			Parent:      nil,
			IoportRange: "4f8-4ff",
			Irq:         2,
			Devpath:     "/dev/ttyS1",
		},
		{
			Parent: &info.BusParent{
				UsbParent: &info.USBAddress{
					Bus:    1,
					Devnum: 1,
				},
			},
			IoportRange: "5f8-5ff",
			Irq:         3,
			Devpath:     "/dev/ttyS2",
		},
		{
			Parent: &info.BusParent{
				PciParent: &info.PCIAddress{
					Domain:   0,
					Bus:      2,
					Device:   0,
					Function: 0,
				},
			},
			IoportRange: "6f8-6ff",
			Irq:         4,
			Devpath:     "/dev/ttyS3",
		},
		{
			Parent: &info.BusParent{
				PciParent: &info.PCIAddress{
					Domain:   0,
					Bus:      4,
					Device:   0,
					Function: 0,
				},
				UsbParent: &info.USBAddress{
					Bus:    4,
					Devnum: 1,
				},
			},
			IoportRange: "7f8-7ff",
			Irq:         4,
			Devpath:     "/dev/ttyS4",
		},
	}
	return nil
}
func (imc *inventoryMsgCreator) fillNetworkDevices() error {
	// ANAN
	imc.inventory.NetworkDevices = []*info.NetworkDevice{
		{
			Parent: &info.BusParent{
				UsbParent: &info.USBAddress{
					Bus:    1101,
					Devnum: 2,
				},
			},
			Ifname:     "eth0",
			Type:       info.NetworkDeviceType_NETWORK_DEVICE_TYPE_ETHERNET,
			MacAddress: "00:09:45:5d:af:23",
			SpeedMbps:  1000,
		},
	}
	return nil
}
func (imc *inventoryMsgCreator) fillCAN() error {
	imc.inventory.CanDevices = []*info.CANDevice{
		{
			Parent: &info.BusParent{
				PciParent: &info.PCIAddress{
					Domain:   0,
					Bus:      4,
					Device:   1,
					Function: 0,
				},
			},
			Ifname: "can0",
		},
	}
	return nil
}
func (imc *inventoryMsgCreator) fillBIOS() error {
	imc.inventory.Bios = &info.BIOS{
		Vendor:     "LENOVO",
		Version:    "N3AET88W (1.53 )",
		Attributes: map[string]string{},
	}
	return nil
}
func (imc *inventoryMsgCreator) fillTPM() error {
	imc.inventory.Tpm = &info.TPM{
		Present:         false,
		Manufacturer:    "Infineon",
		FirmwareVersion: "7.85",
		SpecVersion:     "2.0",
	}

	return nil
}
func AddInventoryInfo(msg *info.ZInfoHardware) error {
	imc := &inventoryMsgCreator{}

	imc.inventory = &info.HardwareInventory{
		PciDevices:        []*info.PCIDevice{},
		UsbDevices:        []*info.USBDevice{},
		SerialDevices:     []*info.SerialPort{},
		NetworkDevices:    []*info.NetworkDevice{},
		CanDevices:        []*info.CANDevice{},
		Bios:              &info.BIOS{},
		CpuInfo:           &info.CPUInfo{},
		TotalMemoryBytes:  100663296,   // ANAN
		TotalStorageBytes: 4294967296,  // ANAN
		WatchdogPresent:   false,       // ANAN
		Tpm:               &info.TPM{}, // ANAN
		StatusLedPresent:  false,       // ANAN
		Misc: map[string]string{
			"ANAN Key": "ANAN Value",
		},
	}

	errs := make(map[string]error)
	errs["PCI"] = imc.fillPCI()
	errs["USB"] = imc.fillUSB()
	errs["CPU"] = imc.fillCPU()
	errs["Serial"] = imc.fillSerial()
	errs["Network"] = imc.fillNetworkDevices()
	errs["CAN"] = imc.fillCAN()
	errs["BIOS"] = imc.fillBIOS()
	errs["TPM"] = imc.fillTPM()

	imc.inventory.CpuCount = uint32(len(imc.inventory.CpuInfo.Cpus))

	var errStr string
	for key, err := range errs {
		if err != nil {
			errStr += fmt.Sprintf("failed to query for %s: %v", key, err)
		}
	}

	var err error
	if len(errStr) > 0 {
		err = fmt.Errorf("querying for hardware failed: %s", errStr)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "BBBBB AddInventoryInfo err: %+v\n", err) /// XXX
	}

	msg.Inventory = imc.inventory

	return err
}

func stringToPCIAddress(str string) *info.PCIAddress {
	pciAddr := address.FromString(str)
	if pciAddr == nil {
		return nil
	}
	domain := pciHexToUint32(pciAddr.Domain)
	bus := pciHexToUint32(pciAddr.Bus)
	device := pciHexToUint32(pciAddr.Device)
	function := pciHexToUint32(pciAddr.Function)

	return &info.PCIAddress{
		Domain:   domain,
		Bus:      bus,
		Device:   device,
		Function: function,
	}
}

func (imc *inventoryMsgCreator) fillUSB() error {
	usbs, err := ghw.USB()
	if err != nil {
		return err
	}

	for _, usb := range usbs.Devices {
		vendorId := pciHexToUint32(usb.VendorID)
		productId := pciHexToUint32(usb.ProductID)
		// busnum := pciHexToUint32(usb.Busnum)
		// devnum := pciHexToUint32(usb.Devnum)
		// parentBusnum := pciHexToUint32(usb.ParentBusnum)
		// parentDevnum := pciHexToUint32(usb.ParentDevnum)

		ud := info.USBDevice{
			VendorId:  vendorId,
			ProductId: productId,
			BusDevnum: &info.USBAddress{
				Bus:    1101, // ANAN
				Devnum: 1102, // ANAN
			},
			Parent:             &info.BusParent{},
			Driver:             "usbdriver", // ANAN
			ClassId:            1103,        // ANAN
			SuggestedAssigngrp: "2",         // ANAN
			AcsEnabled:         false,
		}
		imc.inventory.UsbDevices = append(imc.inventory.UsbDevices, &ud)
	}

	return nil
}

func (imc *inventoryMsgCreator) fillPCI() error {
	db := pcidb.PCIDB{
		Classes:  map[string]*pcitypes.Class{},
		Vendors:  map[string]*pcitypes.Vendor{},
		Products: map[string]*pcitypes.Product{},
	}
	pcis, err := ghw.PCI(option.WithPCIDB(&db))
	if err != nil {
		return fmt.Errorf("could not retrieve PCI information: %+w", err)
	}

	imc.inventory.PciDevices = make([]*info.PCIDevice, 0)

	for _, pci := range pcis.Devices {
		vendorId := pciHexToUint32(pci.Vendor.ID)
		if vendorId == 0 {
			continue
		}
		productId := pciHexToUint32(pci.Product.ID)
		if productId == 0 {
			continue
		}
		revisionId := pciHexToUint32(pci.Revision)
		subsystemId := pciHexToUint32(pci.Subsystem.ID)
		classId := pciHexToUint32(pci.Class.ID)

		imc.inventory.PciDevices = append(imc.inventory.PciDevices, &info.PCIDevice{
			ParentPciDeviceAddress: stringToPCIAddress(pci.ParentAddress),
			Driver:                 pci.Driver,
			Address:                stringToPCIAddress(pci.Address),
			VendorId:               vendorId,
			DeviceId:               productId,
			Revision:               revisionId,
			SubsystemId:            subsystemId,
			ClassId:                classId,
			IommuGroup:             pci.IOMMUGroup,
		})
	}

	return nil
}

func pciHexToUint32(idString string) uint32 {
	if strings.HasPrefix(idString, "0x") {
		var id uint32
		_, err := fmt.Sscanf(idString, "0x%x", &id)
		if err != nil {
			return 0
		}

		return id
	}

	// otherwise we get an "odd length hex string"
	if len(idString)%2 == 1 {
		idString = "0" + idString
	}
	bs, err := hex.DecodeString(idString)
	if err != nil {
		return 0
	}
	if len(bs) == 4 {
		return binary.BigEndian.Uint32(bs)
	}
	if len(bs) == 2 {
		return uint32(binary.BigEndian.Uint16(bs))
	}
	if len(bs) == 1 {
		var id uint32

		_, err := fmt.Sscanf(idString, "%x", &id)
		if err != nil {
			return 0
		}

		return id
	}

	return 0
}
