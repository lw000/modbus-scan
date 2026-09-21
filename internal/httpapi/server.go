// Package httpapi exposes JSON APIs and static administration assets.
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"modbus-scan/internal/appconfig"
	"modbus-scan/internal/model"
	"modbus-scan/internal/realtime"
	devruntime "modbus-scan/internal/runtime"
	"modbus-scan/internal/service"
	"modbus-scan/internal/store"
)

// DeviceService is the device API use-case boundary.
type DeviceService interface {
	Create(context.Context, model.Device) (model.Device, error)
	Update(context.Context, int64, model.Device) (model.Device, error)
	Delete(context.Context, int64) error
	Get(context.Context, int64) (service.DeviceView, error)
	List(context.Context) ([]service.DeviceView, error)
	Start(context.Context, int64) error
	Stop(context.Context, int64) error
	Restart(context.Context, int64) error
	Snapshot(context.Context, int64) (devruntime.Snapshot, error)
}

// PointService is the point API use-case boundary.
type PointService interface {
	Create(context.Context, int64, model.Point) (model.Point, error)
	Get(context.Context, int64, int64) (model.Point, error)
	Update(context.Context, int64, int64, model.Point) (model.Point, error)
	Delete(context.Context, int64, int64) error
	DeleteBatch(context.Context, int64, []int64) (int, error)
	List(context.Context, int64, store.PointFilter) (store.PointPage, error)
	Import(context.Context, int64, io.Reader) (int, []model.RowError, error)
	Export(context.Context, int64, io.Writer) error
}

// NewRouter creates the Gin handler.
func NewRouter(logger *slog.Logger, devices DeviceService, points PointService, webFS fs.FS, realtimeHubs ...*realtime.Hub) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.CustomRecovery(func(c *gin.Context, recovered any) {
		logger.Error("HTTP panic", "panic", recovered)
		failure(c, http.StatusInternalServerError, "internal_error", "内部服务错误", nil)
	}))
	router.Use(func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.Info("HTTP request", "method", c.Request.Method, "path", c.Request.URL.Path, "status", c.Writer.Status(), "duration", time.Since(start), "client", c.ClientIP())
	})
	h := &handlers{devices: devices, points: points}
	api := router.Group("/api/v1")
	h.registerDevices(api)
	h.registerPoints(api)
	if len(realtimeHubs) > 0 && realtimeHubs[0] != nil {
		h.registerWebSocket(api, realtimeHubs[0])
	}
	if webFS != nil {
		router.NoRoute(func(c *gin.Context) {
			if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
				c.Status(http.StatusNotFound)
				return
			}
			name := strings.TrimPrefix(path.Clean(c.Request.URL.Path), "/")
			if name == "." || name == "" {
				name = "index.html"
			}
			if strings.HasPrefix(name, "..") {
				c.Status(http.StatusNotFound)
				return
			}
			data, err := fs.ReadFile(webFS, name)
			if err != nil {
				c.Status(http.StatusNotFound)
				return
			}
			c.Data(http.StatusOK, contentType(name), data)
		})
	}
	return router
}

func contentType(name string) string {
	switch path.Ext(name) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

// NewServer applies configured addresses and timeouts.
func NewServer(cfg appconfig.ServerConfig, handler http.Handler) *http.Server {
	return &http.Server{Addr: net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)), Handler: handler, ReadHeaderTimeout: time.Duration(cfg.ReadHeaderTimeoutSec) * time.Second, ReadTimeout: time.Duration(cfg.ReadTimeoutSec) * time.Second, WriteTimeout: time.Duration(cfg.WriteTimeoutSec) * time.Second, IdleTimeout: time.Duration(cfg.IdleTimeoutSec) * time.Second}
}

type handlers struct {
	devices DeviceService
	points  PointService
}

func parseID(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id < 1 {
		failure(c, http.StatusBadRequest, "invalid_id", fmt.Sprintf("%s 无效", name), nil)
		return 0, false
	}
	return id, true
}

func decodeJSON(c *gin.Context, target any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("decode JSON: multiple values")
	}
	return nil
}
