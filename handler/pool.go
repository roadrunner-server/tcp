package handler

import (
	"bytes"

	"github.com/roadrunner-server/api/v2/payload"
)

func (h *Handler) getServInfo(event string) *ServerInfo {
	si := h.servInfoPool.Get().(*ServerInfo)
	si.Event = event
	si.Server = h.serverName
	si.UUID = h.uuid
	si.RemoteAddr = h.conn.RemoteAddr().String()
	return si
}

func (h *Handler) putServInfo(si *ServerInfo) {
	si.Event = ""
	si.RemoteAddr = ""
	si.Server = ""
	si.UUID = ""
	h.servInfoPool.Put(si)
}

func (h *Handler) getReadBuf() *[]byte {
	return h.readBufPool.Get().(*[]byte)
}

func (h *Handler) putReadBuf(buf *[]byte) {
	h.readBufPool.Put(buf)
}

func (h *Handler) getResBuf() *bytes.Buffer {
	return h.resBufPool.Get().(*bytes.Buffer)
}

func (h *Handler) putResBuf(buf *bytes.Buffer) {
	buf.Reset()
	h.resBufPool.Put(buf)
}

func (h *Handler) getPayload() *payload.Payload {
	return h.pldPool.Get().(*payload.Payload)
}

func (h *Handler) putPayload(pld *payload.Payload) {
	pld.Body = nil
	pld.Context = nil
	h.pldPool.Put(pld)
}
