package service

import (
	"context"
	"fmt"
	"io"
	"strings"

	"modbus-scan/internal/config"
	"modbus-scan/internal/model"
	"modbus-scan/internal/pointcsv"
	"modbus-scan/internal/store"
)

// PointStore is the persistence needed by PointService.
type PointStore interface {
	GetDevice(ctx context.Context, id int64) (model.Device, error)
	CreatePoint(ctx context.Context, point model.Point) (model.Point, error)
	GetPoint(ctx context.Context, deviceID, pointID int64) (model.Point, error)
	ListPoints(ctx context.Context, deviceID int64, filter store.PointFilter) (store.PointPage, error)
	ListAllPoints(ctx context.Context, deviceID int64) ([]model.Point, error)
	UpdatePoint(ctx context.Context, point model.Point) (model.Point, error)
	DeletePoint(ctx context.Context, deviceID, pointID int64) error
	DeletePoints(ctx context.Context, deviceID int64, pointIDs []int64) (int, error)
	ReplacePoints(ctx context.Context, deviceID int64, points []model.Point) error
}

// PointService manages point configuration and CSV transfer.
type PointService struct{ store PointStore }

// NewPointService creates a point service.
func NewPointService(store PointStore) *PointService { return &PointService{store: store} }

func (s *PointService) Create(ctx context.Context, deviceID int64, point model.Point) (model.Point, error) {
	if _, err := s.store.GetDevice(ctx, deviceID); err != nil {
		return model.Point{}, err
	}
	point.ID, point.DeviceID = 0, deviceID
	point.Description = strings.TrimSpace(point.Description)
	if fields := config.ValidatePoint(point); len(fields) > 0 {
		return model.Point{}, &ValidationError{Fields: fields}
	}
	return s.store.CreatePoint(ctx, point)
}

func (s *PointService) Get(ctx context.Context, deviceID, pointID int64) (model.Point, error) {
	return s.store.GetPoint(ctx, deviceID, pointID)
}

func (s *PointService) Update(ctx context.Context, deviceID, pointID int64, point model.Point) (model.Point, error) {
	point.ID, point.DeviceID = pointID, deviceID
	point.Description = strings.TrimSpace(point.Description)
	if fields := config.ValidatePoint(point); len(fields) > 0 {
		return model.Point{}, &ValidationError{Fields: fields}
	}
	return s.store.UpdatePoint(ctx, point)
}

func (s *PointService) Delete(ctx context.Context, deviceID, pointID int64) error {
	return s.store.DeletePoint(ctx, deviceID, pointID)
}

// DeleteBatch validates and atomically deletes points owned by one device.
func (s *PointService) DeleteBatch(ctx context.Context, deviceID int64, pointIDs []int64) (int, error) {
	if _, err := s.store.GetDevice(ctx, deviceID); err != nil {
		return 0, err
	}
	if len(pointIDs) < 1 || len(pointIDs) > 500 {
		return 0, &ValidationError{Fields: []model.FieldError{{Field: "ids", Message: "must contain 1 to 500 IDs"}}}
	}
	seen := make(map[int64]struct{}, len(pointIDs))
	for _, id := range pointIDs {
		if id < 1 {
			return 0, &ValidationError{Fields: []model.FieldError{{Field: "ids", Message: "IDs must be positive"}}}
		}
		if _, ok := seen[id]; ok {
			return 0, &ValidationError{Fields: []model.FieldError{{Field: "ids", Message: "IDs must be unique"}}}
		}
		seen[id] = struct{}{}
	}
	return s.store.DeletePoints(ctx, deviceID, pointIDs)
}

func (s *PointService) List(ctx context.Context, deviceID int64, filter store.PointFilter) (store.PointPage, error) {
	if _, err := s.store.GetDevice(ctx, deviceID); err != nil {
		return store.PointPage{}, err
	}
	return s.store.ListPoints(ctx, deviceID, filter)
}

func (s *PointService) Import(ctx context.Context, deviceID int64, reader io.Reader) (int, []model.RowError, error) {
	if _, err := s.store.GetDevice(ctx, deviceID); err != nil {
		return 0, nil, err
	}
	points, rowErrors, err := pointcsv.Parse(reader)
	if err != nil || len(rowErrors) > 0 {
		return 0, rowErrors, err
	}
	if err := s.store.ReplacePoints(ctx, deviceID, points); err != nil {
		return 0, nil, fmt.Errorf("replace points: %w", err)
	}
	return len(points), nil, nil
}

func (s *PointService) Export(ctx context.Context, deviceID int64, writer io.Writer) error {
	if _, err := s.store.GetDevice(ctx, deviceID); err != nil {
		return err
	}
	points, err := s.store.ListAllPoints(ctx, deviceID)
	if err != nil {
		return err
	}
	return pointcsv.Write(writer, points)
}
