package httpapi

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"modbus-scan/internal/model"
	"modbus-scan/internal/store"
)

func (h *handlers) registerPoints(api *gin.RouterGroup) {
	api.GET("/devices/:id/points", h.listPoints)
	api.DELETE("/devices/:id/points", h.deletePoints)
	api.POST("/devices/:id/points", h.createPoint)
	api.GET("/devices/:id/points/:pointID", h.getPoint)
	api.PUT("/devices/:id/points/:pointID", h.updatePoint)
	api.DELETE("/devices/:id/points/:pointID", h.deletePoint)
	api.POST("/devices/:id/points/import", h.importPoints)
	api.GET("/devices/:id/points/export", h.exportPoints)
}

type deletePointsRequest struct {
	IDs []int64 `json:"ids"`
}

func (h *handlers) deletePoints(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	var request deletePointsRequest
	if err := decodeJSON(c, &request); err != nil {
		failure(c, http.StatusBadRequest, "invalid_json", "invalid request JSON", nil)
		return
	}
	count, err := h.points.DeleteBatch(c.Request.Context(), id, request.IDs)
	if err != nil {
		handleError(c, err)
		return
	}
	success(c, http.StatusOK, gin.H{"deleted": count})
}
func pointIDs(c *gin.Context) (int64, int64, bool) {
	id, ok := parseID(c, "id")
	if !ok {
		return 0, 0, false
	}
	pid, ok := parseID(c, "pointID")
	return id, pid, ok
}
func (h *handlers) listPoints(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	limit := 50
	offset := 0
	var err error
	if text := c.Query("limit"); text != "" {
		limit, err = strconv.Atoi(text)
	}
	if err != nil || limit < 1 || limit > 500 {
		failure(c, http.StatusBadRequest, "invalid_pagination", "limit 必须为 1 到 500", nil)
		return
	}
	if text := c.Query("offset"); text != "" {
		offset, err = strconv.Atoi(text)
	}
	if err != nil || offset < 0 {
		failure(c, http.StatusBadRequest, "invalid_pagination", "offset 不能为负数", nil)
		return
	}
	v, e := h.points.List(c.Request.Context(), id, store.PointFilter{Search: c.Query("search"), RegType: c.Query("reg_type"), Limit: limit, Offset: offset})
	if e != nil {
		handleError(c, e)
		return
	}
	success(c, http.StatusOK, v)
}
func (h *handlers) createPoint(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	var p model.Point
	if e := decodeJSON(c, &p); e != nil {
		failure(c, http.StatusBadRequest, "invalid_json", "请求 JSON 无效", nil)
		return
	}
	v, e := h.points.Create(c.Request.Context(), id, p)
	if e != nil {
		handleError(c, e)
		return
	}
	success(c, http.StatusCreated, v)
}
func (h *handlers) getPoint(c *gin.Context) {
	id, pid, ok := pointIDs(c)
	if !ok {
		return
	}
	v, e := h.points.Get(c.Request.Context(), id, pid)
	if e != nil {
		handleError(c, e)
		return
	}
	success(c, http.StatusOK, v)
}
func (h *handlers) updatePoint(c *gin.Context) {
	id, pid, ok := pointIDs(c)
	if !ok {
		return
	}
	var p model.Point
	if e := decodeJSON(c, &p); e != nil {
		failure(c, http.StatusBadRequest, "invalid_json", "请求 JSON 无效", nil)
		return
	}
	v, e := h.points.Update(c.Request.Context(), id, pid, p)
	if e != nil {
		handleError(c, e)
		return
	}
	success(c, http.StatusOK, v)
}
func (h *handlers) deletePoint(c *gin.Context) {
	id, pid, ok := pointIDs(c)
	if !ok {
		return
	}
	if e := h.points.Delete(c.Request.Context(), id, pid); e != nil {
		handleError(c, e)
		return
	}
	success(c, http.StatusOK, gin.H{"deleted": true})
}
func (h *handlers) importPoints(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 10<<20)
	file, _, e := c.Request.FormFile("file")
	if e != nil {
		failure(c, http.StatusBadRequest, "invalid_file", "必须上传 file 字段", nil)
		return
	}
	defer file.Close()
	count, rows, e := h.points.Import(c.Request.Context(), id, file)
	if e != nil {
		handleError(c, e)
		return
	}
	if len(rows) > 0 {
		failure(c, http.StatusBadRequest, "validation_failed", "点位配置校验失败", rows)
		return
	}
	success(c, http.StatusOK, gin.H{"imported": count})
}

var unsafeFilename = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

func (h *handlers) exportPoints(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	view, e := h.devices.Get(c.Request.Context(), id)
	if e != nil {
		handleError(c, e)
		return
	}
	name := strings.Trim(unsafeFilename.ReplaceAllString(view.Device.Name, "-"), "-")
	if name == "" {
		name = fmt.Sprintf("device-%d", id)
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s-points.csv\"", name))
	if e := h.points.Export(c.Request.Context(), id, c.Writer); e != nil {
		handleError(c, e)
		return
	}
}
