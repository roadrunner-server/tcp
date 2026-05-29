package handler

import (
	"encoding/json"
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
		h.log.Error("payload marshaling error", "error", err)
		return
	}
	pld := h.getPayload()
	pld.Context = c
	defer h.putPayload(pld)
	if _, err = h.wPool(pld); err != nil {
		h.log.Warn("failed to send close event to worker", "error", err)
	}
}
