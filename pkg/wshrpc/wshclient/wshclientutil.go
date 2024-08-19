// Copyright 2024, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package wshclient

import (
	"errors"

	"github.com/wavetermdev/thenextwave/pkg/util/utilfn"
	"github.com/wavetermdev/thenextwave/pkg/wshrpc"
	"github.com/wavetermdev/thenextwave/pkg/wshutil"
)

func sendRpcRequestCallHelper[T any](w *wshutil.WshRpc, command string, data interface{}, opts *wshrpc.RpcOpts) (T, error) {
	if opts == nil {
		opts = &wshrpc.RpcOpts{}
	}
	var respData T
	if w == nil {
		return respData, errors.New("nil wshrpc passed to wshclient")
	}
	if opts.NoResponse {
		err := w.SendCommand(command, data, opts)
		if err != nil {
			return respData, err
		}
		return respData, nil
	}
	resp, err := w.SendRpcRequest(command, data, opts)
	if err != nil {
		return respData, err
	}
	err = utilfn.ReUnmarshal(&respData, resp)
	if err != nil {
		return respData, err
	}
	return respData, nil
}

func rtnErr[T any](ch chan wshrpc.RespOrErrorUnion[T], err error) {
	go func() {
		ch <- wshrpc.RespOrErrorUnion[T]{Error: err}
		close(ch)
	}()
}

func sendRpcRequestResponseStreamHelper[T any](w *wshutil.WshRpc, command string, data interface{}, opts *wshrpc.RpcOpts) chan wshrpc.RespOrErrorUnion[T] {
	if opts == nil {
		opts = &wshrpc.RpcOpts{}
	}
	respChan := make(chan wshrpc.RespOrErrorUnion[T])
	if w == nil {
		rtnErr(respChan, errors.New("nil wshrpc passed to wshclient"))
		return respChan
	}
	reqHandler, err := w.SendComplexRequest(command, data, opts)
	if err != nil {
		rtnErr(respChan, err)
		return respChan
	} else {
		go func() {
			defer close(respChan)
			for {
				if reqHandler.ResponseDone() {
					break
				}
				resp, err := reqHandler.NextResponse()
				if err != nil {
					respChan <- wshrpc.RespOrErrorUnion[T]{Error: err}
					break
				}
				var respData T
				err = utilfn.ReUnmarshal(&respData, resp)
				if err != nil {
					respChan <- wshrpc.RespOrErrorUnion[T]{Error: err}
					break
				}
				respChan <- wshrpc.RespOrErrorUnion[T]{Response: respData}
			}
		}()
	}
	return respChan
}
