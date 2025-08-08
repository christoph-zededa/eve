// Copyright (c) 2025 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package inventory

import (
	"bytes"
	"context"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lf-edge/eve/pkg/pillar/agentbase"
	"github.com/lf-edge/eve/pkg/pillar/agentlog"
	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/controllerconn"
	"github.com/lf-edge/eve/pkg/pillar/hardware"
	"github.com/lf-edge/eve/pkg/pillar/netmonitor"
	"github.com/lf-edge/eve/pkg/pillar/pubsub"
	"github.com/lf-edge/eve/pkg/pillar/types"
	uuid "github.com/satori/go.uuid"
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

type boardingStatusType uint32

const (
	unknownStatus boardingStatusType = iota
	onboardedStatus
	offboardStatus
)

type inventoryReporter struct {
	agentbase.AgentBase
	subscriptions map[string]pubsub.Subscription
	dns           types.DeviceNetworkStatus
	agentMetrics  *controllerconn.AgentMetrics

	needUpload     atomic.Bool
	boardingStatus atomic.Uint32

	uploading sync.Mutex
}

// Run - Main function - invoked from zedbox.go
func Run(ps *pubsub.PubSub, loggerArg *logrus.Logger, logArg *base.LogObject, arguments []string, baseDir string) int {
	logger = loggerArg
	log = logArg

	ir := inventoryReporter{}
	ir.needUpload.Store(true)
	ir.boardingStatus.Store(uint32(unknownStatus))
	ir.subscriptions = make(map[string]pubsub.Subscription)

	agentbase.Init(&ir, logger, log, agentName,
		agentbase.WithPidFile(),
		agentbase.WithBaseDir(baseDir),
		agentbase.WithWatchdog(ps, warningTime, errorTime),
		agentbase.WithArguments(arguments))

	// Wait until we have been onboarded aka know our own UUID, but we don't use the UUID
	// err := wait.WaitForOnboarded(ps, log, agentName, warningTime, errorTime)
	// if err != nil {
	// 	log.Fatal(err)
	// }
	// log.Functionf("processed onboarded")

	// if err := wait.WaitForVault(ps, log, agentName, warningTime, errorTime); err != nil {
	// 	log.Fatal(err)
	// }
	// log.Functionf("processed Vault Status")

	ir.agentMetrics = controllerconn.NewAgentMetrics()

	ir.subscribe(ps)

	ir.process(ps)

	return 0
}

func (ir *inventoryReporter) upload() {
	if ir.boardingStatus.Load() == uint32(unknownStatus) {
		return
	}
	if ir.boardingStatus.Load() == uint32(onboardedStatus) {
		return
	}
	if !ir.needUpload.Load() {
		return
	}

	if !ir.uploading.TryLock() {
		return
	}

	defer ir.uploading.Unlock()

	dnsAny, err := ir.subscriptions["deviceNetworkStatus"].Get("global")
	if err != nil {
		log.Warnf("Getting deviceNetworkStatus with key 'global' failed: %v", err)
		return
	}

	dns, ok := dnsAny.(*types.DeviceNetworkStatus)
	if !ok {
		log.Warnf("Failed to cast %v (%T) to *types.DeviceNetworkStatus", dnsAny, dnsAny)
		return
	}

	gcp := agentlog.HandleGlobalConfig(log, ir.subscriptions["globalConfig"], agentName,
		false, logger)

	timeout := gcp.GlobalValueInt(types.NetworkSendTimeout)
	dialTimeoutSecs := gcp.GlobalValueInt(types.NetworkDialTimeout)

	networkMonitor := &netmonitor.LinuxNetworkMonitor{Log: log}

	nilUUID := uuid.UUID{}
	productSerial := hardware.GetProductSerial(log)
	softSerial := hardware.GetSoftSerial(log)
	ctrlClient := controllerconn.NewClient(log, controllerconn.ClientOptions{
		AgentName:           agentName,
		NetworkMonitor:      networkMonitor,
		DeviceNetworkStatus: dns,
		TLSConfig:           nil,
		AgentMetrics:        ir.agentMetrics,
		NetworkSendTimeout:  time.Second * time.Duration(timeout),
		NetworkDialTimeout:  time.Second * time.Duration(dialTimeoutSecs),
		DevUUID:             nilUUID,
		DevSerial:           productSerial,
		DevSoftSerial:       softSerial,
		ResolverCacheFunc:   nil,
		NoLedManager:        false,
	})

	server, err := types.Server()
	if err != nil {
		log.Warnf("could not get server name: %+v", err)
		return
	}

	inventoryURL := controllerconn.URLPath(
		server, ctrlClient.UsingV2API(), nilUUID, "inventory")

	inventoryURL.Path = filepath.Join(inventoryURL.Path, productSerial, softSerial)

	buf := bytes.Buffer{}

	rv, err := ctrlClient.SendOnAllIntf(context.Background(), inventoryURL.String(), &buf, controllerconn.RequestOptions{
		WithNetTracing: false,
		BailOnHTTPErr:  false,
		Iteration:      0,
		AllowProxy:     true,
	})
	if err != nil {
		log.Noticef("Posting to %s failed: %v", inventoryURL.String(), err)
		return
	}
	if rv.Status.Failure() {
		log.Noticef("Posting to %s failed, status is %v", inventoryURL.String(), rv.Status.String())
		return
	}

	ir.needUpload.Store(false)
}

func (ir *inventoryReporter) requestOnboardStatus() {
	status := ir.subscriptions["onboardStatus"].GetAll()

	nilUUID := uuid.UUID{}
	for key, status := range status {
		onboardingStatus, ok := status.(types.OnboardingStatus)
		if !ok {
			log.Warnf("could not use %T with key %s as types.OnboardingStatus: %+v", status, key, status)
			return
		}
		if onboardingStatus.DeviceUUID == nilUUID {
			ir.boardingStatus.Store(uint32(offboardStatus))
		} else {
			ir.boardingStatus.Store(uint32(onboardedStatus))
		}
	}
}

func (ir *inventoryReporter) subscribe(ps *pubsub.PubSub) {
	var err error
	ir.subscriptions["deviceNetworkStatus"], err = ps.NewSubscription(pubsub.SubscriptionOptions{
		WarningTime: warningTime,
		ErrorTime:   errorTime,
		AgentName:   "nim",
		MyAgentName: agentName,
		TopicImpl:   types.DeviceNetworkStatus{},
	})

	if err != nil {
		log.Fatal(err)
	}

	ir.subscriptions["globalConfig"], err = ps.NewSubscription(
		pubsub.SubscriptionOptions{
			AgentName:   "zedagent",
			MyAgentName: agentName,
			TopicImpl:   types.ConfigItemValueMap{},
			Persistent:  true,
			Activate:    false,
			WarningTime: warningTime,
			ErrorTime:   errorTime,
		})
	if err != nil {
		log.Fatal(err)
	}

	ir.subscriptions["onboardStatus"], err = ps.NewSubscription(pubsub.SubscriptionOptions{
		AgentName:   "zedclient",
		MyAgentName: agentName,
		TopicImpl:   types.OnboardingStatus{},
		Activate:    false,
		Persistent:  true,
		WarningTime: warningTime,
		ErrorTime:   errorTime,
	})
	if err != nil {
		log.Fatal(err)
	}

	for _, sub := range ir.subscriptions {
		err := sub.Activate()
		if err != nil {
			log.Fatalf("cannot subscribe to %+v: %+v", sub, err)
		}
	}

}

func (ir *inventoryReporter) process(ps *pubsub.PubSub) {
	stillRunning := time.NewTicker(stillRunningInterval)

	// TODO: get initial onboarding status
	watches := make([]pubsub.ChannelWatch, 0)
	for i := range ir.subscriptions {
		sub := ir.subscriptions[i]
		watches = append(watches, pubsub.WatchAndProcessSubChanges(sub))
	}

	watches = append(watches, pubsub.ChannelWatch{
		Chan: reflect.ValueOf(stillRunning.C),
		Callback: func(_ interface{}) (exit bool) {
			ps.StillRunning(agentName, warningTime, errorTime)
			return false
		},
	})

	uploadTicker := time.NewTicker(2 * time.Minute)
	watches = append(watches, pubsub.ChannelWatch{
		Chan: reflect.ValueOf(uploadTicker.C),
		Callback: func(value interface{}) bool {
			ir.requestOnboardStatus()
			go func() {
				ir.upload()
			}()
			return false
		},
	})

	pubsub.MultiChannelWatch(watches)
}
