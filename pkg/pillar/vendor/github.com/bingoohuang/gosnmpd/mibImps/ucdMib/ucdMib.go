package ucdMib

import "github.com/bingoohuang/gosnmpd"

func init() {
	g_Logger = gosnmpd.NewDiscardLogger()
}

var g_Logger gosnmpd.ILogger

// SetupLogger Setups Logger for this mib
func SetupLogger(i gosnmpd.ILogger) {
	g_Logger = i
}

// All function provides a list of common used OID in UCD-MIB
func All() []*gosnmpd.PDUValueControlItem {
	var result []*gosnmpd.PDUValueControlItem
	result = append(result, MemoryOIDs()...)
	result = append(result, SystemStatsOIDs()...)
	result = append(result, SystemLoadOIDs()...)
	result = append(result, DiskUsageOIDs()...)
	return result
}
