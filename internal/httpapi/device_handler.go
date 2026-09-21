package httpapi

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"modbus-scan/internal/model"
)

func (h *handlers) registerDevices(api *gin.RouterGroup) {
	api.GET("/devices", h.listDevices)
	api.POST("/devices", h.createDevice)
	api.GET("/devices/:id", h.getDevice)
	api.PUT("/devices/:id", h.updateDevice)
	api.DELETE("/devices/:id", h.deleteDevice)
	api.POST("/devices/:id/start", h.startDevice)
	api.POST("/devices/:id/stop", h.stopDevice)
	api.POST("/devices/:id/restart", h.restartDevice)
	api.GET("/devices/:id/status", h.deviceStatus)
	api.GET("/devices/:id/values", h.deviceValues)
}

func (h *handlers) listDevices(c *gin.Context) {
	v, e := h.devices.List(c.Request.Context())
	if e != nil {
		handleError(c, e)
		return
	}
	success(c, http.StatusOK, v)
}
func (h *handlers) createDevice(c *gin.Context) {
	var d model.Device
	if e := decodeJSON(c, &d); e != nil {
		failure(c, http.StatusBadRequest, "invalid_json", "请求 JSON 无效", nil)
		return
	}
	v, e := h.devices.Create(c.Request.Context(), d)
	if e != nil {
		handleError(c, e)
		return
	}
	success(c, http.StatusCreated, v)
}
func (h *handlers) getDevice(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	v, e := h.devices.Get(c.Request.Context(), id)
	if e != nil {
		handleError(c, e)
		return
	}
	success(c, http.StatusOK, v)
}
func (h *handlers) updateDevice(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	var d model.Device
	if e := decodeJSON(c, &d); e != nil {
		failure(c, http.StatusBadRequest, "invalid_json", "请求 JSON 无效", nil)
		return
	}
	v, e := h.devices.Update(c.Request.Context(), id, d)
	if e != nil {
		handleError(c, e)
		return
	}
	success(c, http.StatusOK, v)
}
func (h *handlers) deleteDevice(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	if e := h.devices.Delete(c.Request.Context(), id); e != nil {
		handleError(c, e)
		return
	}
	success(c, http.StatusOK, gin.H{"deleted": true})
}
func (h *handlers) lifecycle(c *gin.Context, fn func(context.Context, int64) error) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	if e := fn(c.Request.Context(), id); e != nil {
		handleError(c, e)
		return
	}
	success(c, http.StatusOK, gin.H{"ok": true})
}
func (h *handlers) startDevice(c *gin.Context)   { h.lifecycle(c, h.devices.Start) }
func (h *handlers) stopDevice(c *gin.Context)    { h.lifecycle(c, h.devices.Stop) }
func (h *handlers) restartDevice(c *gin.Context) { h.lifecycle(c, h.devices.Restart) }
func (h *handlers) deviceStatus(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	v, e := h.devices.Snapshot(c.Request.Context(), id)
	if e != nil {
		handleError(c, e)
		return
	}
	v.Values = nil
	success(c, http.StatusOK, v)
}
func (h *handlers) deviceValues(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	v, e := h.devices.Snapshot(c.Request.Context(), id)
	if e != nil {
		handleError(c, e)
		return
	}
	success(c, http.StatusOK, v.Values)
}
