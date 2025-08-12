package snmp

import (
	"testing"

	"github.com/lf-edge/eve/pkg/pillar/agentlog"
)

func init() {
	logger, log = agentlog.Init(agentName)
}

func TestServe(t *testing.T) {
	ServeSnmp()
}
