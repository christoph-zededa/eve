package dismanEventMib

import (
	"github.com/bingoohuang/gosnmpd"
	"github.com/shirou/gopsutil/host"
	"github.com/slayercat/gosnmp"
)

func init() {
	g_Logger = gosnmpd.NewDiscardLogger()
}

var g_Logger gosnmpd.ILogger

// SetupLogger Setups Logger for this mib
func SetupLogger(i gosnmpd.ILogger) {
	g_Logger = i
}

// DismanEventOids function provides sysUptime
//
//	see http://www.oid-info.com/get/1.3.6.1.2.1.1.3.0
//	    http://www.net-snmp.org/docs/mibs/dismanEventMIB.html
func DismanEventOids() []*gosnmpd.PDUValueControlItem {
	return []*gosnmpd.PDUValueControlItem{
		{
			OID:  "1.3.6.1.2.1.1.3.0",
			Type: gosnmp.TimeTicks,
			OnGet: func() (value interface{}, err error) {
				if val, err := host.Uptime(); err != nil {
					return nil, err
				} else {
					return gosnmpd.Asn1TimeTicksWrap(uint32(val)), nil
				}
			},
			Document: "Uptime",
		},
	}
}

// All function provides a list of common used OID in DISMAN-EVENT-MIB
func All() []*gosnmpd.PDUValueControlItem {
	return DismanEventOids()
}
