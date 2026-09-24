package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"modbus-scan/internal/model"
	"modbus-scan/internal/service"
)

type kafkaSettingsRequest struct {
	model.KafkaSettings
	SASLPassword      string `json:"sasl_password"`
	ClearSASLPassword bool   `json:"clear_sasl_password"`
}

func (h *handlers) registerKafka(api *gin.RouterGroup) {
	api.GET("/kafka", h.getKafkaSettings)
	api.PUT("/kafka", h.updateKafkaSettings)
	api.GET("/devices/:id/kafka", h.getDeviceKafkaConfig)
	api.PUT("/devices/:id/kafka", h.updateDeviceKafkaConfig)
}

func (h *handlers) getKafkaSettings(c *gin.Context) {
	value, err := h.kafka.GetSettings(c.Request.Context())
	if err != nil {
		handleError(c, err)
		return
	}
	success(c, http.StatusOK, value)
}

func (h *handlers) updateKafkaSettings(c *gin.Context) {
	var request kafkaSettingsRequest
	if err := decodeJSON(c, &request); err != nil {
		failure(c, http.StatusBadRequest, "invalid_json", "请求 JSON 无效", nil)
		return
	}
	request.KafkaSettings.SASLPassword = request.SASLPassword
	value, err := h.kafka.UpdateSettings(c.Request.Context(), service.KafkaSettingsInput{KafkaSettings: request.KafkaSettings, ClearSASLPassword: request.ClearSASLPassword})
	if err != nil {
		handleError(c, err)
		return
	}
	success(c, http.StatusOK, value)
}

func (h *handlers) getDeviceKafkaConfig(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	value, err := h.kafka.GetDeviceConfig(c.Request.Context(), id)
	if err != nil {
		handleError(c, err)
		return
	}
	success(c, http.StatusOK, value)
}

func (h *handlers) updateDeviceKafkaConfig(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	var cfg model.DeviceKafkaConfig
	if err := decodeJSON(c, &cfg); err != nil {
		failure(c, http.StatusBadRequest, "invalid_json", "请求 JSON 无效", nil)
		return
	}
	value, err := h.kafka.UpdateDeviceConfig(c.Request.Context(), id, cfg)
	if err != nil {
		handleError(c, err)
		return
	}
	success(c, http.StatusOK, value)
}
