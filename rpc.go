package tcp

import (
	"context"

	"connectrpc.com/connect"
	tcpV1 "github.com/roadrunner-server/api-go/v6/tcp/v1"
)

type rpc struct {
	p *Plugin
}

func (r *rpc) Close(_ context.Context, req *connect.Request[tcpV1.CloseRequest]) (*connect.Response[tcpV1.Response], error) {
	if err := r.p.Close(req.Msg.GetUuid()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&tcpV1.Response{Ok: true}), nil
}
