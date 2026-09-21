package httpapi

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"modbus-scan/internal/realtime"
)

type wsCommand struct {
	Action   string   `json:"action"`
	DeviceID int64    `json:"device_id"`
	Tags     []string `json:"tags"`
}
type wsMessage struct {
	Type      string    `json:"type"`
	Code      string    `json:"code,omitempty"`
	Message   string    `json:"message,omitempty"`
	DeviceID  int64     `json:"device_id,omitempty"`
	TagName   string    `json:"tag_name,omitempty"`
	Value     any       `json:"value,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

var wsUpgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && strings.EqualFold(parsed.Host, r.Host)
}}

func (h *handlers) registerWebSocket(api *gin.RouterGroup, hub *realtime.Hub) {
	api.GET("/ws", func(c *gin.Context) { h.webSocket(c, hub) })
}
func (h *handlers) webSocket(c *gin.Context, hub *realtime.Hub) {
	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(16 << 10)
	sub := hub.Subscribe(64)
	defer sub.Close()
	out := make(chan wsMessage, 64)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			var command wsCommand
			if err := conn.ReadJSON(&command); err != nil {
				return
			}
			if command.DeviceID < 1 || len(command.Tags) < 1 || len(command.Tags) > 500 {
				out <- wsMessage{Type: "error", Code: "invalid_subscription", Message: "订阅参数无效"}
				continue
			}
			switch command.Action {
			case "subscribe":
				if err := sub.Set(command.DeviceID, command.Tags); err != nil {
					out <- wsMessage{Type: "error", Code: "invalid_subscription", Message: err.Error()}
					continue
				}
				snapshot, err := h.devices.Snapshot(c.Request.Context(), command.DeviceID)
				if err == nil {
					for _, tag := range command.Tags {
						if value, ok := snapshot.Values[tag]; ok {
							out <- wsMessage{Type: "value", DeviceID: command.DeviceID, TagName: tag, Value: value.Value, UpdatedAt: value.UpdatedAt}
						}
					}
				}
			case "unsubscribe":
				sub.Remove(command.DeviceID, command.Tags)
			default:
				out <- wsMessage{Type: "error", Code: "invalid_action", Message: "不支持的操作"}
			}
		}
	}()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		var message wsMessage
		select {
		case <-readDone:
			return
		case <-sub.Done():
			return
		case event := <-sub.Events():
			message = wsMessage{Type: "value", DeviceID: event.DeviceID, TagName: event.TagName, Value: event.Value, UpdatedAt: event.UpdatedAt}
		case message = <-out:
		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
			continue
		}
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := conn.WriteJSON(message); err != nil {
			return
		}
	}
}
