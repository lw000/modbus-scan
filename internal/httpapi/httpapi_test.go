package httpapi

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"modbus-scan/internal/model"
	devruntime "modbus-scan/internal/runtime"
	"modbus-scan/internal/service"
	"modbus-scan/internal/store"
)

func TestStaticAssets(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":  &fstest.MapFile{Data: []byte("<h1>devices</h1>")},
		"device.html": &fstest.MapFile{Data: []byte("<h1>device</h1>")},
		"css/app.css": &fstest.MapFile{Data: []byte("body{}")},
	}
	router := NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), &stubDevices{}, &stubPoints{}, assets)
	for _, path := range []string{"/", "/device.html", "/css/app.css"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, rec.Code)
		}
	}
}

type stubDevices struct {
	created    model.Device
	startCalls int
}

func (s *stubDevices) Create(_ context.Context, d model.Device) (model.Device, error) {
	d.ID = 1
	s.created = d
	return d, nil
}
func (s *stubDevices) Update(_ context.Context, id int64, d model.Device) (model.Device, error) {
	d.ID = id
	return d, nil
}
func (s *stubDevices) Delete(context.Context, int64) error { return nil }
func (s *stubDevices) Get(_ context.Context, id int64) (service.DeviceView, error) {
	return service.DeviceView{Device: model.Device{ID: id, Name: "plc"}}, nil
}
func (s *stubDevices) List(context.Context) ([]service.DeviceView, error) {
	return []service.DeviceView{{Device: model.Device{ID: 1, Name: "plc"}}}, nil
}
func (s *stubDevices) Start(context.Context, int64) error   { s.startCalls++; return nil }
func (s *stubDevices) Stop(context.Context, int64) error    { return nil }
func (s *stubDevices) Restart(context.Context, int64) error { return nil }
func (s *stubDevices) Snapshot(_ context.Context, id int64) (devruntime.Snapshot, error) {
	return devruntime.Snapshot{DeviceID: id, State: devruntime.StateOnline}, nil
}

type stubPoints struct {
	imported   string
	lastFilter store.PointFilter
	created    model.Point
	deletedIDs []int64
}

func (s *stubPoints) Create(_ context.Context, deviceID int64, p model.Point) (model.Point, error) {
	p.ID = 1
	p.DeviceID = deviceID
	s.created = p
	return p, nil
}
func (s *stubPoints) Get(_ context.Context, deviceID, pointID int64) (model.Point, error) {
	return model.Point{ID: pointID, DeviceID: deviceID, TagName: "A"}, nil
}
func (s *stubPoints) Update(_ context.Context, deviceID, pointID int64, p model.Point) (model.Point, error) {
	p.ID = pointID
	p.DeviceID = deviceID
	return p, nil
}
func (s *stubPoints) Delete(context.Context, int64, int64) error { return nil }
func (s *stubPoints) DeleteBatch(_ context.Context, _ int64, ids []int64) (int, error) {
	s.deletedIDs = append([]int64(nil), ids...)
	return len(ids), nil
}
func (s *stubPoints) List(_ context.Context, _ int64, filter store.PointFilter) (store.PointPage, error) {
	s.lastFilter = filter
	return store.PointPage{Items: []model.Point{}, Total: 123}, nil
}
func (s *stubPoints) Import(_ context.Context, _ int64, r io.Reader) (int, []model.RowError, error) {
	b, _ := io.ReadAll(r)
	s.imported = string(b)
	return 1, nil, nil
}
func (s *stubPoints) Export(_ context.Context, _ int64, w io.Writer) error {
	_, err := io.WriteString(w, "TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable,Description\n")
	return err
}

func TestCreatePointUsesNumericWriteable(t *testing.T) {
	points := &stubPoints{}
	router := testRouter(&stubDevices{}, points)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/1/points", strings.NewReader(`{"tag_name":"A","reg_type":"HoldingReg","data_type":"UInt16","bit_len":16,"writeable":true}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("boolean writeable status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/devices/1/points", strings.NewReader(`{"tag_name":"A","description":"泵运行","reg_type":"HoldingReg","data_type":"UInt16","bit_len":16,"writeable":1}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || points.created.Writeable != 1 || points.created.Description != "泵运行" {
		t.Fatalf("status=%d point=%#v body=%s", rec.Code, points.created, rec.Body.String())
	}
}

func TestDeletePointBatch(t *testing.T) {
	points := &stubPoints{}
	router := testRouter(&stubDevices{}, points)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/devices/1/points", strings.NewReader(`{"ids":[11,12]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"deleted":2`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(points.deletedIDs) != 2 {
		t.Fatalf("ids=%v", points.deletedIDs)
	}
}

func testRouter(devices DeviceService, points PointService) http.Handler {
	return NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), devices, points, nil)
}
func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestCreateDeviceRejectsUnknownField(t *testing.T) {
	router := testRouter(&stubDevices{}, &stubPoints{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices", strings.NewReader(`{"name":"x","unknown":1}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeviceRoutes(t *testing.T) {
	devices := &stubDevices{}
	router := testRouter(devices, &stubPoints{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "plc") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/devices/1/start", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || devices.startCalls != 1 {
		t.Fatalf("status=%d calls=%d", rec.Code, devices.startCalls)
	}
}

func TestPointCSVImportAndExport(t *testing.T) {
	points := &stubPoints{}
	router := testRouter(&stubDevices{}, points)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "points.csv")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(part, "csv-data")
	_ = writer.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/1/points/import", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || points.imported != "csv-data" {
		t.Fatalf("status=%d body=%s imported=%q", rec.Code, rec.Body.String(), points.imported)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/devices/1/points/export", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("status=%d headers=%v", rec.Code, rec.Header())
	}
}

func TestListPointsPagination(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantLimit  int
	}{
		{name: "maximum", query: "limit=500&offset=20&search=temp&reg_type=HoldingReg", wantStatus: http.StatusOK, wantLimit: 500},
		{name: "over maximum", query: "limit=501", wantStatus: http.StatusBadRequest},
		{name: "zero", query: "limit=0", wantStatus: http.StatusBadRequest},
		{name: "negative", query: "limit=-1", wantStatus: http.StatusBadRequest},
		{name: "not a number", query: "limit=many", wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			points := &stubPoints{}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/devices/1/points?"+tt.query, nil)
			rec := httptest.NewRecorder()
			testRouter(&stubDevices{}, points).ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if tt.wantStatus != http.StatusOK {
				if !strings.Contains(rec.Body.String(), `"code":"invalid_pagination"`) {
					t.Fatalf("body=%s", rec.Body.String())
				}
				return
			}
			if points.lastFilter.Limit != tt.wantLimit || points.lastFilter.Offset != 20 || points.lastFilter.Search != "temp" || points.lastFilter.RegType != "HoldingReg" {
				t.Fatalf("filter=%+v", points.lastFilter)
			}
		})
	}
}
