// Copyright 2017 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"bytes"
	"github.com/pingcap/tidb/mysql"
	"github.com/pingcap/tidb/util"
	"github.com/pingcap/tidb/util/auth"
	xutil "github.com/pingcap/tidb/xprotocol/util"
	"net"
)

type authMysql41State int32

const (
	sStarting authMysql41State = iota
	sWaitingResponse
	sDone
	sError
)

type saslMysql41Auth struct {
	mState authMysql41State
	mSalts []byte
	xauth  *xAuth
}

func (spa *saslMysql41Auth) handleStart(mechanism *string, data []byte, initialResponses []byte) *response {
	r := response{}

	if spa.mState == sStarting {
		spa.mSalts = util.RandomBuf(mysql.ScrambleLength)
		r.data = string(spa.mSalts)
		r.status = authOngoing
		r.errCode = 0
		spa.mState = sWaitingResponse
	} else {
		r.status = authError
		r.errCode = mysql.ErrNetPacketsOutOfOrder

		spa.mState = sError
	}

	return &r
}

func (spa *saslMysql41Auth) handleContinue(data []byte) *response {
	if spa.mState == sWaitingResponse {
		dbname, user, passwd := spa.extractNullTerminatedElement(data)
		if dbname == nil || user == nil || passwd == nil {
			return &response{
				status:  authFailed,
				data:    xutil.ErrXBadMessage.ToSQLError().Message,
				errCode: xutil.ErrXBadMessage.ToSQLError().Code,
			}
		}

		xcc := spa.xauth.xcc
		xcc.dbname = string(dbname)
		xcc.user = string(user)

		spa.mState = sDone
		if !spa.xauth.xcc.server.skipAuth() {
			// Do Auth
			addr := spa.xauth.xcc.conn.RemoteAddr().String()
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return &response{
					status:  authFailed,
					data:    xutil.ErrAccessDenied.ToSQLError().Message,
					errCode: xutil.ErrAccessDenied.ToSQLError().Code,
				}
			}
			if !spa.xauth.xcc.ctx.Auth(&auth.UserIdentity{Username: string(user), Hostname: host},
				passwd, spa.mSalts) {
				return &response{
					status:  authFailed,
					data:    xutil.ErrAccessDenied.ToSQLError().Message,
					errCode: xutil.ErrAccessDenied.ToSQLError().Code,
				}
			}
		}

		return &response{
			status:  authSucceed,
			errCode: 0,
		}
	}
	spa.mState = sError

	return &response{
		status:  authError,
		errCode: mysql.ErrNetPacketsOutOfOrder,
	}
}

func (spa *saslMysql41Auth) extractNullTerminatedElement(data []byte) ([]byte, []byte, []byte) {
	slices := bytes.Split(data, []byte{0})

	if len(slices) != 3 {
		return nil, nil, nil
	}
	return slices[0], slices[1], slices[2]
}
