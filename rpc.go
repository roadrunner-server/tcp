package tcp

import (
	tcpV1 "github.com/roadrunner-server/api-go/v6/tcp/v1"
)

type rpc struct {
	p *Plugin
}

func (r *rpc) Close(in *tcpV1.CloseRequest, out *tcpV1.Response) error {
	if err := r.p.Close(in.GetUuid()); err != nil {
		return err
	}

	out.Ok = true
	return nil
}
