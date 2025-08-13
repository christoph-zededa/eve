// Copyright (c) 2025 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package zedagent

import (
	"bytes"
	"fmt"

	"github.com/lf-edge/eve-api/go/info"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (zc *zedagentContext) sendZiLOCInfo() {
	zc.getconfigCtx.sideController.Lock()
	defer zc.getconfigCtx.sideController.Unlock()

	// do not re-send same message again
	if zc.getconfigCtx.sideController.locInfo.String() == zc.getconfigCtx.sideController.sentLocInfo {
		return
	}

	infoMsg := &info.ZInfoMsg{
		Ztype: info.ZInfoTypes_ZiPatchEnvelope,
		DevId: devUUID.String(),
		InfoContent: &info.ZInfoMsg_Loc{
			Loc: &zc.getconfigCtx.sideController.locInfo,
		},
		AtTimeStamp: timestamppb.Now(),
	}

	log.Tracef("sending %v", infoMsg)
	data, err := proto.Marshal(infoMsg)
	if err != nil {
		log.Warnf("proto marshaling error: %+v", err)
		return
	}
	buf := bytes.NewBuffer(data)

	const bailOnHTTPErr = false
	const withNetTrace = false
	key := fmt.Sprintf("sendZiLOCInfo: %+v", &zc.getconfigCtx.sideController.locInfo)

	const forcePeriodic = false
	queueInfoToDest(zc, LOCDest, key, buf, bailOnHTTPErr, withNetTrace,
		forcePeriodic, info.ZInfoTypes_ZiLOC)

	zc.getconfigCtx.sideController.sentLocInfo = zc.getconfigCtx.sideController.locInfo.String()
}
