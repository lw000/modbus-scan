package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"modbus-scan/internal/realtime"
)

func TestWebSocketSubscribesAndFilters(t *testing.T) {
	hub := realtime.NewHub(500)
	server := httptest.NewServer(NewRouter(testLogger(), &stubDevices{}, &stubPoints{}, nil, hub))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"action": "subscribe", "device_id": 1, "tags": []string{"Speed"}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	hub.Publish(realtime.ValueEvent{DeviceID: 2, TagName: "Speed", Value: 1, UpdatedAt: time.Now()})
	hub.Publish(realtime.ValueEvent{DeviceID: 1, TagName: "Speed", Value: 2, UpdatedAt: time.Now()})
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	var message wsMessage
	if err := conn.ReadJSON(&message); err != nil {
		t.Fatal(err)
	}
	if message.DeviceID != 1 || message.TagName != "Speed" || message.Value.(float64) != 2 {
		t.Fatalf("message=%#v", message)
	}
}
