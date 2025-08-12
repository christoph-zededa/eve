package ifMib

import "github.com/bingoohuang/gosnmpd"

func init() {
	g_Logger = gosnmpd.NewDiscardLogger()
}

var g_Logger gosnmpd.ILogger

// SetupLogger Setups Logger for this mib
func SetupLogger(i gosnmpd.ILogger) {
	g_Logger = i
}

// All function provides a list of common used OID in IF-MIB
func All() []*gosnmpd.PDUValueControlItem {
	return NetworkOIDs()
}
