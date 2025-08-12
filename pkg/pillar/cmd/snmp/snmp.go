// Copyright (c) 2025 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package snmp

import (
	"fmt"
	"time"

	"github.com/bingoohuang/gosnmpd"
	"github.com/bingoohuang/gosnmpd/mibImps/dismanEventMib"
	"github.com/lf-edge/eve/pkg/pillar/agentbase"
	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/pubsub"
	"github.com/lf-edge/eve/pkg/pillar/types"
	"github.com/sirupsen/logrus"
	"github.com/slayercat/gosnmp"
)

const (
	agentName = "snmp"
	// Time limits for event loop handlers
	errorTime            = 3 * time.Minute
	warningTime          = 40 * time.Second
	stillRunningInterval = 25 * time.Second
)

var (
	logger *logrus.Logger
	log    *base.LogObject
)

type snmpServer struct {
	agentbase.AgentBase
	subscriptions       map[string]pubsub.Subscription
	retrieveLOCConfigs  func() map[string]interface{}
	decipherCredentials func(datastore types.DatastoreConfig) (string, string)
}

// Run - Main function - invoked from zedbox.go
func Run(ps *pubsub.PubSub, loggerArg *logrus.Logger, logArg *base.LogObject, arguments []string, baseDir string) int {
	logger = loggerArg
	log = logArg

	s := snmpServer{}

	agentbase.Init(&s, logger, log, agentName,
		agentbase.WithPidFile(),
		agentbase.WithBaseDir(baseDir),
		agentbase.WithWatchdog(ps, warningTime, errorTime),
		agentbase.WithArguments(arguments))

	go ServeSnmp()

	for {
		ps.StillRunning(agentName, warningTime, errorTime)
	}

}

func ServeSnmp() {
	users := gosnmp.UsmSecurityParameters{
		UserName:                 "admin",
		AuthenticationProtocol:   gosnmp.NoAuth,
		AuthenticationPassphrase: "adminadmin",
		PrivacyProtocol:          gosnmp.NoPriv,
		PrivacyPassphrase:        "",
	}

	master := gosnmpd.MasterAgent{
		Logger: logger,
		SecurityConfig: gosnmpd.SecurityConfig{
			AuthoritativeEngineBoots: 1,
			Users:                    []gosnmp.UsmSecurityParameters{users},
		},
		SubAgents: []*gosnmpd.SubAgent{
			{
				CommunityIDs: []string{"public"},
				// OIDs:         mibImps.All(),
				OIDs: dismanEventMib.All(),
			},
		},
	}

	server := gosnmpd.NewSNMPServer(master)
	fmt.Printf("Listening ...\n")
	err := server.ListenUDP("udp", ":1161")
	if err != nil {
		logger.Errorf("Error in listen: %+v", err)
	}
	server.ServeForever()
}
