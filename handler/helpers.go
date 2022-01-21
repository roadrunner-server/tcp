package handler

import (
	"github.com/goccy/go-json"
	"go.uber.org/zap"
)

func (h *Handler) generate(event string) ([]byte, error) {
	si := h.getServInfo(event)
	pld, err := json.Marshal(si)
	if err != nil {
		h.putServInfo(si)
		return nil, err
	}

	h.putServInfo(si)
	return pld, nil
}

func (h *Handler) sendClose() {
	c, err := h.generate(EventClose)
	if err != nil {
		h.log.Error("payload marshaling error", zap.Error(err))
		return
	}
	pld := h.getPayload()
	pld.Context = c
	_, _ = h.wPool(pld)
	h.putPayload(pld)
}
