// Copyright (c) 2025 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package inventory

import (
	"reflect"
	"time"

	"github.com/lf-edge/eve/pkg/pillar/agentbase"
	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/pubsub"
	"github.com/lf-edge/eve/pkg/pillar/utils/wait"
	"github.com/sirupsen/logrus"
)

const (
	agentName = "inventory"
	// Time limits for event loop handlers
	errorTime            = 3 * time.Minute
	warningTime          = 40 * time.Second
	stillRunningInterval = 25 * time.Second
)

var (
	logger *logrus.Logger
	log    *base.LogObject
)

type inventoryReporter struct {
	agentbase.AgentBase
	subscriptions map[string]pubsub.Subscription
}

// Run - Main function - invoked from zedbox.go
func Run(ps *pubsub.PubSub, loggerArg *logrus.Logger, logArg *base.LogObject, arguments []string, baseDir string) int {
	logger = loggerArg
	log = logArg

	cr := inventoryReporter{}

	agentbase.Init(&cr, logger, log, agentName,
		agentbase.WithPidFile(),
		agentbase.WithBaseDir(baseDir),
		agentbase.WithWatchdog(ps, warningTime, errorTime),
		agentbase.WithArguments(arguments))

	// Wait until we have been onboarded aka know our own UUID, but we don't use the UUID
	err := wait.WaitForOnboarded(ps, log, agentName, warningTime, errorTime)
	if err != nil {
		log.Fatal(err)
	}
	log.Functionf("processed onboarded")

	if err := wait.WaitForVault(ps, log, agentName, warningTime, errorTime); err != nil {
		log.Fatal(err)
	}
	log.Functionf("processed Vault Status")

	cr.subscribe(ps)

	cr.process(ps)

	return 0
}

func (ci *inventoryReporter) subscribe(ps *pubsub.PubSub) {

	// I hate that this is a map - reflection would be better, but others hate reflection ...
	ci.subscriptions = map[string]pubsub.Subscription{
		// "loc":            subLOCConfig,
		// "controllercert": subControllerCert,
		// "edgenodecert":   subEdgeNodeCert,
	}

	for _, sub := range ci.subscriptions {
		err := sub.Activate()
		if err != nil {
			log.Fatalf("cannot subscribe to %+v: %+v", sub, err)
		}
	}

}

func (ci *inventoryReporter) process(ps *pubsub.PubSub) {
	stillRunning := time.NewTicker(stillRunningInterval)

	watches := make([]pubsub.ChannelWatch, 0)
	for i := range ci.subscriptions {
		sub := ci.subscriptions[i]
		watches = append(watches, pubsub.WatchAndProcessSubChanges(sub))
	}

	watches = append(watches, pubsub.ChannelWatch{
		Chan: reflect.ValueOf(stillRunning.C),
		Callback: func(_ interface{}) (exit bool) {
			ps.StillRunning(agentName, warningTime, errorTime)
			return false
		},
	})

	pubsub.MultiChannelWatch(watches)
}
