package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"modbus-scan/internal/service"
	"modbus-scan/internal/store"
)

type errorEnvelope struct {
	Error apiError `json:"error"`
}
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func success(c *gin.Context, status int, data any) { c.JSON(status, gin.H{"data": data}) }
func failure(c *gin.Context, status int, code, message string, details any) {
	c.AbortWithStatusJSON(status, errorEnvelope{Error: apiError{Code: code, Message: message, Details: details}})
}

func handleError(c *gin.Context, err error) {
	var validation *service.ValidationError
	switch {
	case errors.As(err, &validation):
		failure(c, http.StatusBadRequest, "validation_failed", "参数校验失败", validation.Fields)
	case errors.Is(err, store.ErrNotFound):
		failure(c, http.StatusNotFound, "not_found", "资源不存在", nil)
	case errors.Is(err, store.ErrConflict):
		failure(c, http.StatusConflict, "conflict", "资源冲突", nil)
	default:
		failure(c, http.StatusInternalServerError, "internal_error", "内部服务错误", nil)
	}
}
