package mibImps

import (
	"github.com/bingoohuang/gosnmpd"
	"github.com/bingoohuang/gosnmpd/mibImps/dismanEventMib"
	"github.com/bingoohuang/gosnmpd/mibImps/ifMib"
	"github.com/bingoohuang/gosnmpd/mibImps/ucdMib"
)

// SetupLogger Setups Logger for All sub mibs.
func SetupLogger(i gosnmpd.ILogger) {
	dismanEventMib.SetupLogger(i)
	ifMib.SetupLogger(i)
	ucdMib.SetupLogger(i)
}

// All function provides a list of common used OID
//
//	includes part of ucdMib, ifMib, and dismanEventMib
func All() []*gosnmpd.PDUValueControlItem {
	var toRet []*gosnmpd.PDUValueControlItem
	toRet = append(toRet, dismanEventMib.All()...)
	toRet = append(toRet, ifMib.All()...)
	toRet = append(toRet, ucdMib.All()...)
	return toRet
}
