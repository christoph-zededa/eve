// Copyright (c) 2025 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package collectinfo

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/lf-edge/eve/pkg/pillar/agentbase"
	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/cipher"
	"github.com/lf-edge/eve/pkg/pillar/containerd"
	"github.com/lf-edge/eve/pkg/pillar/pubsub"
	"github.com/lf-edge/eve/pkg/pillar/types"
	"github.com/lf-edge/eve/pkg/pillar/utils/wait"
	"github.com/sirupsen/logrus"
)

const (
	agentName = "collectinfo"
	// Time limits for event loop handlers
	errorTime            = 3 * time.Minute
	warningTime          = 40 * time.Second
	stillRunningInterval = 25 * time.Second
)

var (
	logger *logrus.Logger
	log    *base.LogObject
)

type collectInfo struct {
	agentbase.AgentBase
	subscriptions map[string]pubsub.Subscription
}

// Run - Main function - invoked from zedbox.go
func Run(ps *pubsub.PubSub, loggerArg *logrus.Logger, logArg *base.LogObject, arguments []string, baseDir string) int {
	logger = loggerArg
	log = logArg

	ci := collectInfo{}

	agentbase.Init(&ci, logger, log, agentName,
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

	ci.subscribe(ps)

	ci.process(ps)

	return 0
}

func isToken(str string) bool {
	// https://datatracker.ietf.org/doc/html/rfc7230#section-3.2.6
	rex := regexp.MustCompile(`^[a-zA-Z0-9-._~+/!#\$%&'\*\+^` + "`" + `|']*$`)

	return rex.MatchString(str)
}

func isToken68(str string) bool {
	// https://datatracker.ietf.org/doc/html/rfc7235#appendix-C
	rex := regexp.MustCompile(`^[a-zA-Z0-9-._~+/]*$`)

	return rex.MatchString(str)
}

func (ci *collectInfo) handleCollectInfoCmd(ctx interface{}, _ string, _ interface{}) {
	authMethod, authPassword, err := ci.retrieveDSCredentials()
	if err != nil {
		log.Warnf("reading datastore credentials failed: %v", err)
		return
	}

	// authMethod can be f.e. Bearer, Basic, NTLM
	authorizationHeader := fmt.Sprintf("%s %s", authMethod, authPassword)

	authorizationHeader = strings.TrimSpace(authorizationHeader)

	authEnv := ""
	if authorizationHeader != "" {
		authEnv = fmt.Sprintf("AUTHORIZATION=%s", authorizationHeader)
	}

	go func(authMethod string, authPassword string) {
		var buf bytes.Buffer

		args := []string{"/usr/bin/collect-info.sh"}

		env := []string{authEnv}

		taskID := fmt.Sprintf("%d", time.Now().Unix())
		err := containerd.RunInDebugContainer(context.Background(), taskID, &buf, args, env, 15*time.Minute)
		if err != nil {
			log.Warnf("running %+v failed: %+v", args, err)
		}

		log.Noticef("collect-info.sh output:\n%s\n", buf.String())
	}(authMethod, authPassword)
}

func (ci *collectInfo) retrieveDSCredentials() (string, string, error) {
	lc := ci.subscriptions["loc"].GetAll()
	var apiKey string
	var apiPassword string

	for _, v := range lc {
		locConfig, ok := v.(types.LOCConfig)
		if !ok {
			log.Warnf("could not cast %T to LOCConfig: %+v", v, v)
			continue
		}

		_, decBlock, err := cipher.GetCipherCredentials(
			&cipher.DecryptCipherContext{
				Log:                  log,
				AgentName:            agentName,
				AgentMetrics:         cipher.NewAgentMetrics(agentName),
				PubSubControllerCert: ci.subscriptions["controllercert"],
				PubSubEdgeNodeCert:   ci.subscriptions["edgenodecert"],
			},
			locConfig.CollectInfoDatastore.CipherBlockStatus)
		if err != nil {
			log.Warnf("could not decipher datastore cipherblock: %+v", err)
			apiKey = locConfig.CollectInfoDatastore.ApiKey
			apiPassword = locConfig.CollectInfoDatastore.Password
		} else {
			apiKey = decBlock.DsAPIKey
			apiPassword = decBlock.DsPassword
		}

		if apiKey != "" || apiPassword != "" {
			break
		}
	}

	if !isToken68(apiPassword) {
		return "", "", fmt.Errorf("DsPassword is not token68-conform")
	}
	if !isToken(apiKey) {
		return "", "", fmt.Errorf("DsAPIKey is not token-conform")
	}
	return apiKey, apiPassword, nil
}

func (ci *collectInfo) subscribe(ps *pubsub.PubSub) {
	subLOCConfig, err := ps.NewSubscription(pubsub.SubscriptionOptions{
		WarningTime: warningTime,
		ErrorTime:   errorTime,
		AgentName:   "zedagent",
		MyAgentName: agentName,
		TopicImpl:   types.LOCConfig{},
		Activate:    true,
	})
	if err != nil {
		log.Fatalf("could not subscribe to LOCConfig: %+v", err)
		return
	}

	subCollectInfoCmd, err := ps.NewSubscription(pubsub.SubscriptionOptions{
		WarningTime:   warningTime,
		ErrorTime:     errorTime,
		CreateHandler: ci.handleCollectInfoCmd,
		ModifyHandler: func(ctx interface{}, key string, status, oldStatus interface{}) {
			ci.handleCollectInfoCmd(ctx, key, status)
		},
		AgentName:   "zedagent",
		MyAgentName: agentName,
		TopicImpl:   types.CollectInfoCmd{},
		Activate:    true,
	})
	if err != nil {
		log.Fatalf("could not subscribe to CollectInfoCmd: %+v", err)
		return
	}

	// Look for controller certs which will be used for decryption.
	subControllerCert, err := ps.NewSubscription(pubsub.SubscriptionOptions{
		AgentName:   "zedagent",
		MyAgentName: agentName,
		TopicImpl:   types.ControllerCert{},
		Activate:    false,
		WarningTime: warningTime,
		ErrorTime:   errorTime,
		Persistent:  true,
	})
	if err != nil {
		log.Fatalf("could not subscribe to ControllerCert: %+v", err)
		return
	}

	// Look for edge node certs which will be used for decryption
	subEdgeNodeCert, err := ps.NewSubscription(pubsub.SubscriptionOptions{
		AgentName:   "tpmmgr",
		MyAgentName: agentName,
		TopicImpl:   types.EdgeNodeCert{},
		Activate:    false,
		Persistent:  true,
		WarningTime: warningTime,
		ErrorTime:   errorTime,
	})
	if err != nil {
		log.Fatalf("could not subscribe to EdgeNodeCert: %+v", err)
		return
	}

	// I hate that this is a map - reflection would be better, but others hate reflection ...
	ci.subscriptions = map[string]pubsub.Subscription{
		"loc":            subLOCConfig,
		"collectinfo":    subCollectInfoCmd,
		"controllercert": subControllerCert,
		"edgenodecert":   subEdgeNodeCert,
	}

	for _, sub := range ci.subscriptions {
		err := sub.Activate()
		if err != nil {
			log.Fatalf("cannot subscribe to %+v: %+v", sub, err)
		}
	}

}

func (ci *collectInfo) process(ps *pubsub.PubSub) {
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
